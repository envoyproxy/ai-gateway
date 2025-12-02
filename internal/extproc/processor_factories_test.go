// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"log/slog"
	"testing"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

type (
	completionsRouterProcessor   = routerProcessor[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, completionsEndpointHandler]
	completionsUpstreamProcessor = upstreamProcessor[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, completionsEndpointHandler]

	embeddingsRouterProcessor   = routerProcessor[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}, embeddingsEndpointHandler]
	embeddingsUpstreamProcessor = upstreamProcessor[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}, embeddingsEndpointHandler]

	imageGenerationRouterProcessor   = routerProcessor[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}, imageGenerationEndpointHandler]
	imageGenerationUpstreamProcessor = upstreamProcessor[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}, imageGenerationEndpointHandler]

	messagesRouterProcessor   = routerProcessor[anthropic.MessagesRequest, anthropic.MessagesResponse, anthropic.MessagesStreamChunk, messagesEndpointHandler]
	messagesUpstreamProcessor = upstreamProcessor[anthropic.MessagesRequest, anthropic.MessagesResponse, anthropic.MessagesStreamChunk, messagesEndpointHandler]

	rerankRouterProcessor   = routerProcessor[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}, rerankEndpointHandler]
	rerankUpstreamProcessor = upstreamProcessor[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}, rerankEndpointHandler]
)

func TestNewFactory(t *testing.T) {
	cfg := &filterapi.RuntimeConfig{}
	headers := map[string]string{"foo": "bar"}

	t.Run("router", func(t *testing.T) {
		t.Parallel()

		factory := ChatCompletionProcessorFactory(nil, tracing.NoopChatCompletionTracer{})
		proc, err := factory(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &chatCompletionProcessorRouterFilter{}, proc)

		router := proc.(*chatCompletionProcessorRouterFilter)
		require.Equal(t, cfg, router.config)
		require.Equal(t, headers, router.requestHeaders)
		require.NotNil(t, router.logger)
		require.NotNil(t, router.tracer)
	})

	t.Run("upstream", func(t *testing.T) {
		t.Parallel()

		factory := ChatCompletionProcessorFactory(&mockMetricsFactory{}, tracing.NoopChatCompletionTracer{})
		proc, err := factory(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &chatCompletionProcessorUpstreamFilter{}, proc)

		upstream := proc.(*chatCompletionProcessorUpstreamFilter)
		require.Equal(t, headers, upstream.requestHeaders)
		require.IsType(t, &mockMetrics{}, upstream.metrics)
	})
}

func TestProcessorFactories_ReturnExpectedTypes(t *testing.T) {
	cfg := &filterapi.RuntimeConfig{}
	headers := map[string]string{"host": "example"}

	t.Run("completions", func(t *testing.T) {
		router, err := CompletionsProcessorFactory(nil, tracing.NoopCompletionTracer{})(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &completionsRouterProcessor{}, router)

		mf := &mockMetricsFactory{}
		upstream, err := CompletionsProcessorFactory(mf, tracing.NoopCompletionTracer{})(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &completionsUpstreamProcessor{}, upstream)
	})

	t.Run("embeddings", func(t *testing.T) {
		router, err := EmbeddingsProcessorFactory(nil, tracing.NoopEmbeddingsTracer{})(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &embeddingsRouterProcessor{}, router)

		mf := &mockMetricsFactory{}
		upstream, err := EmbeddingsProcessorFactory(mf, tracing.NoopEmbeddingsTracer{})(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &embeddingsUpstreamProcessor{}, upstream)
	})

	t.Run("image_generation", func(t *testing.T) {
		router, err := ImageGenerationProcessorFactory(nil, tracing.NoopImageGenerationTracer{})(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &imageGenerationRouterProcessor{}, router)

		mf := &mockMetricsFactory{}
		upstream, err := ImageGenerationProcessorFactory(mf, tracing.NoopImageGenerationTracer{})(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &imageGenerationUpstreamProcessor{}, upstream)
	})

	t.Run("messages", func(t *testing.T) {
		router, err := MessagesProcessorFactory(nil, tracing.NoopMessageTracer{})(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &messagesRouterProcessor{}, router)

		mf := &mockMetricsFactory{}
		upstream, err := MessagesProcessorFactory(mf, tracing.NoopMessageTracer{})(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &messagesUpstreamProcessor{}, upstream)
	})

	t.Run("rerank", func(t *testing.T) {
		router, err := RerankProcessorFactory(nil, tracing.NoopRerankTracer{})(cfg, headers, slog.Default(), false)
		require.NoError(t, err)
		require.IsType(t, &rerankRouterProcessor{}, router)

		mf := &mockMetricsFactory{}
		upstream, err := RerankProcessorFactory(mf, tracing.NoopRerankTracer{})(cfg, headers, slog.Default(), true)
		require.NoError(t, err)
		require.IsType(t, &rerankUpstreamProcessor{}, upstream)
	})
}

func TestChatCompletionsEndpointHandler_ParseBody(t *testing.T) {
	handler := chatCompletionsEndpointHandler{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("not-json"))
		require.ErrorContains(t, err, "failed to unmarshal chat completion request")
	})

	t.Run("streaming_without_include_usage", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4o", Stream: true}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.NotNil(t, parsed.StreamOptions)
		require.True(t, parsed.StreamOptions.IncludeUsage)
		require.NotNil(t, mutated)

		var mutatedReq openai.ChatCompletionRequest
		require.NoError(t, json.Unmarshal(mutated, &mutatedReq))
		require.NotNil(t, mutatedReq.StreamOptions)
		require.True(t, mutatedReq.StreamOptions.IncludeUsage)
	})

	t.Run("streaming_with_include_usage_already_true", func(t *testing.T) {
		req := openai.ChatCompletionRequest{
			Model:         "gpt-4.1",
			Stream:        true,
			StreamOptions: &openai.StreamOptions{IncludeUsage: true},
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		_, parsed, _, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.True(t, parsed.StreamOptions.IncludeUsage)
		require.Nil(t, mutated)
	})

	t.Run("non_streaming", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4-mini", Stream: false}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-mini", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestChatCompletionsEndpointHandler_GetTranslator(t *testing.T) {
	handler := chatCompletionsEndpointHandler{}
	supported := []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		{Name: filterapi.APISchemaAWSBedrock},
		{Name: filterapi.APISchemaAzureOpenAI, Version: "2024-02-01"},
		{Name: filterapi.APISchemaGCPVertexAI},
		{Name: filterapi.APISchemaGCPAnthropic, Version: "2024-05-01"},
	}

	for _, schema := range supported {
		s := schema
		t.Run("supported_"+string(s.Name), func(t *testing.T) {
			t.Parallel()
			translator, err := handler.GetTranslator(s, "override")
			require.NoError(t, err)
			require.NotNil(t, translator)
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: "Unknown"}, "override")
		require.ErrorContains(t, err, "unsupported API schema")
	})
}

func TestCompletionsEndpointHandler_ParseBody(t *testing.T) {
	handler := completionsEndpointHandler{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("{bad"))
		require.ErrorContains(t, err, "failed to unmarshal completion request")
	})

	t.Run("streaming", func(t *testing.T) {
		req := openai.CompletionRequest{Model: "text-davinci-003", Stream: true, Prompt: openai.PromptUnion{Value: "say hi"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "text-davinci-003", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestCompletionsEndpointHandler_GetTranslator(t *testing.T) {
	handler := completionsEndpointHandler{}

	_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestEmbeddingsEndpointHandler_ParseBody(t *testing.T) {
	handler := embeddingsEndpointHandler{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("{"))
		require.ErrorContains(t, err, "failed to unmarshal embedding request")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.EmbeddingRequest{Model: "text-embedding-3-large", Input: openai.EmbeddingRequestInput{Value: "input"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "text-embedding-3-large", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestEmbeddingsEndpointHandler_GetTranslator(t *testing.T) {
	handler := embeddingsEndpointHandler{}

	for _, schema := range []filterapi.VersionedAPISchema{{Name: filterapi.APISchemaOpenAI}, {Name: filterapi.APISchemaAzureOpenAI}} {
		translator, err := handler.GetTranslator(schema, "override")
		require.NoError(t, err)
		require.NotNil(t, translator)
	}

	_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestImageGenerationEndpointHandler_ParseBody(t *testing.T) {
	handler := imageGenerationEndpointHandler{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("{"))
		require.ErrorContains(t, err, "failed to unmarshal image generation request")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "gpt-image-1", "prompt": "cat"})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "gpt-image-1", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestImageGenerationEndpointHandler_GetTranslator(t *testing.T) {
	handler := imageGenerationEndpointHandler{}

	_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestMessagesEndpointHandler_ParseBody(t *testing.T) {
	handler := messagesEndpointHandler{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("["))
		require.ErrorContains(t, err, "failed to unmarshal Anthropic Messages body")
	})

	t.Run("missing model", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"stream": true})
		require.NoError(t, err)

		_, _, _, _, err = handler.ParseBody(body)
		require.ErrorContains(t, err, "model field is required")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "claude-3", "stream": true})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "claude-3", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestMessagesEndpointHandler_GetTranslator(t *testing.T) {
	handler := messagesEndpointHandler{}
	for _, schema := range []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaGCPAnthropic},
		{Name: filterapi.APISchemaAWSAnthropic},
		{Name: filterapi.APISchemaAnthropic},
	} {
		translator, err := handler.GetTranslator(schema, "override")
		require.NoError(t, err)
		require.NotNil(t, translator)
	}

	_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.ErrorContains(t, err, "only supports")
}

func TestRerankEndpointHandler_ParseBody(t *testing.T) {
	handler := rerankEndpointHandler{}
	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := handler.ParseBody([]byte("{"))
		require.ErrorContains(t, err, "failed to unmarshal rerank request")
	})

	t.Run("success", func(t *testing.T) {
		req := cohereschema.RerankV2Request{Model: "rerank-v3.5", Query: "foo", Documents: []string{"bar"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := handler.ParseBody(body)
		require.NoError(t, err)
		require.Equal(t, "rerank-v3.5", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestRerankEndpointHandler_GetTranslator(t *testing.T) {
	handler := rerankEndpointHandler{}

	_, err := handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.NoError(t, err)

	_, err = handler.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

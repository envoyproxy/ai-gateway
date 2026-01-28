// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package endpointspec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestChatCompletionsEndpointSpec_ParseBody(t *testing.T) {
	spec := ChatCompletionsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("not-json"), false)
		require.ErrorContains(t, err, "failed to unmarshal chat completion request")
	})

	t.Run("streaming_without_include_usage", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4o", Stream: true}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, true)
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

		_, parsed, _, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.True(t, parsed.StreamOptions.IncludeUsage)
		require.Nil(t, mutated)
	})

	t.Run("non_streaming", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4-mini", Stream: false}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-mini", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestChatCompletionsEndpointSpec_GetTranslator(t *testing.T) {
	spec := ChatCompletionsEndpointSpec{}
	supported := []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaOpenAI, Prefix: "v1"},
		{Name: filterapi.APISchemaAWSBedrock},
		{Name: filterapi.APISchemaAzureOpenAI, Version: "2024-02-01"},
		{Name: filterapi.APISchemaGCPVertexAI},
		{Name: filterapi.APISchemaGCPAnthropic, Version: "2024-05-01"},
	}

	for _, schema := range supported {
		s := schema
		t.Run("supported_"+string(s.Name), func(t *testing.T) {
			t.Parallel()
			translator, err := spec.GetTranslator(s, "override")
			require.NoError(t, err)
			require.NotNil(t, translator)
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: "Unknown"}, "override")
		require.ErrorContains(t, err, "unsupported API schema")
	})
}

func TestCompletionsEndpointSpec_ParseBody(t *testing.T) {
	spec := CompletionsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{bad"), false)
		require.ErrorContains(t, err, "failed to unmarshal completion request")
	})

	t.Run("streaming", func(t *testing.T) {
		req := openai.CompletionRequest{Model: "text-davinci-003", Stream: true, Prompt: openai.PromptUnion{Value: "say hi"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "text-davinci-003", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestCompletionsEndpointSpec_GetTranslator(t *testing.T) {
	spec := CompletionsEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestEmbeddingsEndpointSpec_ParseBody(t *testing.T) {
	spec := EmbeddingsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "failed to unmarshal embedding request")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.EmbeddingRequest{Model: "text-embedding-3-large", Input: openai.EmbeddingRequestInput{Value: "input"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "text-embedding-3-large", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestEmbeddingsEndpointSpec_GetTranslator(t *testing.T) {
	spec := EmbeddingsEndpointSpec{}

	for _, schema := range []filterapi.VersionedAPISchema{{Name: filterapi.APISchemaOpenAI}, {Name: filterapi.APISchemaAzureOpenAI}} {
		translator, err := spec.GetTranslator(schema, "override")
		require.NoError(t, err)
		require.NotNil(t, translator)
	}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestImageGenerationEndpointSpec_ParseBody(t *testing.T) {
	spec := ImageGenerationEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "failed to unmarshal image generation request")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "gpt-image-1", "prompt": "cat"})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-image-1", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestImageGenerationEndpointSpec_GetTranslator(t *testing.T) {
	spec := ImageGenerationEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestMessagesEndpointSpec_ParseBody(t *testing.T) {
	spec := MessagesEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("["), false)
		require.ErrorContains(t, err, "failed to unmarshal Anthropic Messages body")
	})

	t.Run("missing model", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"stream": true})
		require.NoError(t, err)

		_, _, _, _, err = spec.ParseBody(body, false)
		require.ErrorContains(t, err, "model field is required")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "claude-3", "stream": true})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "claude-3", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestMessagesEndpointSpec_GetTranslator(t *testing.T) {
	spec := MessagesEndpointSpec{}
	for _, schema := range []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaGCPAnthropic},
		{Name: filterapi.APISchemaAWSAnthropic},
		{Name: filterapi.APISchemaAnthropic},
	} {
		translator, err := spec.GetTranslator(schema, "override")
		require.NoError(t, err)
		require.NotNil(t, translator)
	}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.ErrorContains(t, err, "only supports")
}

func TestRerankEndpointSpec_ParseBody(t *testing.T) {
	spec := RerankEndpointSpec{}
	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "failed to unmarshal rerank request")
	})

	t.Run("success", func(t *testing.T) {
		req := cohereschema.RerankV2Request{Model: "rerank-v3.5", Query: "foo", Documents: []string{"bar"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "rerank-v3.5", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestRerankEndpointSpec_GetTranslator(t *testing.T) {
	spec := RerankEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestResponsesEndpointSpec_ParseBody(t *testing.T) {
	spec := ResponsesEndpointSpec{}
	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "failed to unmarshal responses request")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.ResponseRequest{Model: "gpt-4o", Input: openai.ResponseNewParamsInputUnion{
			OfString: ptr.To("Hi"),
		}, Stream: true}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestResponsesEndpointSpec_GetTranslator(t *testing.T) {
	spec := ResponsesEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

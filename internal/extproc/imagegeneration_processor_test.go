// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	openaisdk "github.com/openai/openai-go/v2"
)

func TestImageGeneration_Schema(t *testing.T) {
	t.Run("supported openai / on route", func(t *testing.T) {
		cfg := &processorConfig{}
		routeFilter, err := ImageGenerationProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &imageGenerationProcessorRouterFilter{}, routeFilter)
	})
	t.Run("supported openai / on upstream", func(t *testing.T) {
		cfg := &processorConfig{}
		routeFilter, err := ImageGenerationProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &imageGenerationProcessorUpstreamFilter{}, routeFilter)
	})
}

func Test_imageGenerationProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &imageGenerationProcessorRouterFilter{}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("not-json")})
		require.ErrorContains(t, err, "failed to parse request body")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/images/generations"}
		const modelKey = "x-ai-gateway-model-key"
		p := &imageGenerationProcessorRouterFilter{
			config:         &processorConfig{modelNameHeaderKey: modelKey},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         tracing.NoopTracing{}.ImageGenerationTracer(),
		}
		body := imageGenerationBodyFromModel(t, "dall-e-3")
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.RequestBody)
		setHeaders := re.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 2)
		require.Equal(t, modelKey, setHeaders[0].Header.Key)
		require.Equal(t, "dall-e-3", string(setHeaders[0].Header.RawValue))
		require.Equal(t, "x-ai-eg-original-path", setHeaders[1].Header.Key)
		require.Equal(t, "/v1/images/generations", string(setHeaders[1].Header.RawValue))
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("ok passthrough", func(t *testing.T) {
		p := &imageGenerationProcessorUpstreamFilter{metrics: &mockImageGenerationMetrics{}}
		res, err := p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{Headers: []*corev3.HeaderValue{}})
		require.NoError(t, err)
		require.NotNil(t, res)
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("non-2xx marks failure and returns mutations", func(t *testing.T) {
		mm := &mockImageGenerationMetrics{}
		p := &imageGenerationProcessorUpstreamFilter{
			metrics:         mm,
			responseHeaders: map[string]string{":status": "500"},
		}
		res, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("err"), EndOfStream: true})
		require.NoError(t, err)
		require.NotNil(t, res)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.NotNil(t, commonRes.HeaderMutation)
		require.NotNil(t, commonRes.BodyMutation)
		mm.RequireRequestFailure(t)
	})

	t.Run("200 end-of-stream records success and metadata", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte(`{"created":1,"data":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), EndOfStream: true}
		mm := &mockImageGenerationMetrics{}

		celProgInt, err := llmcostcel.NewProgram("123")
		require.NoError(t, err)

		p := &imageGenerationProcessorUpstreamFilter{
			translator: translator.NewImageGenerationOpenAIToOpenAITranslator("v1", "some_model"),
			metrics:    mm,
			logger:     slog.Default(),
			config: &processorConfig{
				metadataNamespace: "ai_gateway_llm_ns",
				requestCosts: []processorConfigRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeTotalToken, MetadataKey: "total_token_usage"}},
					{celProg: celProgInt, LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_int"}},
				},
			},
			backendName:       "some_backend",
			modelNameOverride: "some_model",
			responseHeaders:   map[string]string{":status": "200"},
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		require.NotNil(t, res)
		mm.RequireRequestSuccess(t)

		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(0), md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(0), md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["total_token_usage"].GetNumberValue())
		require.Equal(t, float64(123), md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, "some_backend", md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["backend_name"].GetStringValue())
		require.Equal(t, "some_model", md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["model_name_override"].GetStringValue())
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("ok with auth handler and header mutator", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/images/generations", "x-model": "dall-e-3"}
		mm := &mockImageGenerationMetrics{}
		p := &imageGenerationProcessorUpstreamFilter{
			config:                 &processorConfig{modelNameHeaderKey: "x-model"},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			originalRequestBodyRaw: imageGenerationBodyFromModel(t, "dall-e-3"),
			originalRequestBody:    &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModel("dall-e-3"), Prompt: "a cat"},
			handler:                &mockBackendAuthHandler{},
		}
		resp, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)
		require.NotNil(t, resp)
		mm.RequireRequestNotCompleted(t)
		mm.RequireSelectedModel(t, "dall-e-3")
	})
}

func Test_imageGenerationProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/v1/images/generations"}
	mm := &mockImageGenerationMetrics{}
	p := &imageGenerationProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        mm,
	}

	// Unsupported schema.
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:   "some-backend",
		Schema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
	}, nil, &imageGenerationProcessorRouterFilter{})
	require.ErrorContains(t, err, "unsupported API schema: backend={some-schema v10.0}")
	mm.RequireRequestFailure(t)
	mm.RequireTokensRecorded(t, 0)
	mm.RequireSelectedBackend(t, "some-backend")

	// Supported OpenAI schema.
	rp := &imageGenerationProcessorRouterFilter{originalRequestBody: &openaisdk.ImageGenerateParams{}}
	p2 := &imageGenerationProcessorUpstreamFilter{
		config:         &processorConfig{modelNameHeaderKey: "x-model-name"},
		requestHeaders: map[string]string{"x-model-name": "dall-e-2"},
		logger:         slog.Default(),
		metrics:        &mockImageGenerationMetrics{},
	}
	err = p2.SetBackend(t.Context(), &filterapi.Backend{
		Name:              "openai",
		Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		ModelNameOverride: "gpt-image-1",
	}, nil, rp)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-1", p2.requestHeaders["x-model-name"])
}

func TestImageGeneration_ParseBody(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		jsonBody := `{"model":"dall-e-2","prompt":"a cat"}`
		modelName, rb, err := parseOpenAIImageGenerationBody(&extprocv3.HttpBody{Body: []byte(jsonBody)})
		require.NoError(t, err)
		require.Equal(t, "dall-e-2", modelName)
		require.NotNil(t, rb)
		require.Equal(t, "dall-e-2", rb.Model)
		require.Equal(t, "a cat", rb.Prompt)
	})
	t.Run("error", func(t *testing.T) {
		modelName, rb, err := parseOpenAIImageGenerationBody(&extprocv3.HttpBody{})
		require.Error(t, err)
		require.Empty(t, modelName)
		require.Nil(t, rb)
	})
}

// imageGenerationBodyFromModel returns a minimal valid image generation request for tests.
func imageGenerationBodyFromModel(t *testing.T, model string) []byte {
	t.Helper()
	b, err := json.Marshal(&openaisdk.ImageGenerateParams{Model: openaisdk.ImageModel(model), Prompt: "a cat"})
	require.NoError(t, err)
	return b
}

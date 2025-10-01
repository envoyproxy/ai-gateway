// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func Test_imageGenerationProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	c := &imageGenerationProcessorUpstreamFilter{}
	t.Run("unsupported", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"})
		require.ErrorContains(t, err, "unsupported API schema: backend={Bar v123}")
	})
	t.Run("supported openai", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI})
		require.NoError(t, err)
		require.NotNil(t, c.translator)
	})
	t.Run("supported aws bedrock", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock})
		require.NoError(t, err)
		require.Nil(t, c.translator) // Placeholder implementation
	})
}

type mockImageGenerationTracer struct {
	tracing.NoopImageGenerationTracer
	startSpanCalled bool
	returnedSpan    tracing.ImageGenerationSpan
}

func (m *mockImageGenerationTracer) StartSpanAndInjectHeaders(_ context.Context, _ map[string]string, headerMutation *extprocv3.HeaderMutation, _ *openaisdk.ImageGenerateParams, _ []byte) tracing.ImageGenerationSpan {
	m.startSpanCalled = true
	headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:   "tracing-header",
			Value: "1",
		},
	})
	if m.returnedSpan != nil {
		return m.returnedSpan
	}
	return nil
}

// Mock span for image generation tests
type mockImageGenerationSpan struct {
	endSpanCalled bool
	errorStatus   int
	errBody       string
}

func (m *mockImageGenerationSpan) EndSpan() {
	m.endSpanCalled = true
}

func (m *mockImageGenerationSpan) EndSpanOnError(status int, body []byte) {
	m.errorStatus = status
	m.errBody = string(body)
}

func (m *mockImageGenerationSpan) RecordResponse(resp *openaisdk.ImagesResponse) {
	// Mock implementation
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

	t.Run("span creation", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/images/generations"}
		const modelKey = "x-ai-gateway-model-key"
		span := &mockImageGenerationSpan{}
		mockTracerInstance := &mockImageGenerationTracer{returnedSpan: span}

		p := &imageGenerationProcessorRouterFilter{
			config:         &processorConfig{modelNameHeaderKey: modelKey},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         mockTracerInstance,
		}

		body := imageGenerationBodyFromModel(t, "dall-e-3")
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, mockTracerInstance.startSpanCalled)
		require.Equal(t, span, p.span)

		// Verify headers are injected.
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		headerMutation := re.RequestBody.GetResponse().GetHeaderMutation()
		require.Contains(t, headerMutation.SetHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   "tracing-header",
				Value: "1",
			},
		})
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockImageGenerationMetrics{}
		mt := &mockImageGenerationTranslator{t: t, expHeaders: make(map[string]string)}
		p := &imageGenerationProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
			logger:     slog.Default(),
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.ErrorContains(t, err, "test error")
		mm.RequireRequestFailure(t)
	})
	t.Run("ok", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mm := &mockImageGenerationMetrics{}
		mt := &mockImageGenerationTranslator{t: t, expHeaders: expHeaders}
		p := &imageGenerationProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
			logger:     slog.Default(),
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		mm.RequireRequestNotCompleted(t)
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockImageGenerationMetrics{}
		mt := &mockImageGenerationTranslator{t: t}
		p := &imageGenerationProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
			logger:     slog.Default(),
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "test error")
		mm.RequireRequestFailure(t)
		require.Zero(t, mm.tokenUsageCount)
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("some-body"), EndOfStream: true}
		expBodyMut := &extprocv3.BodyMutation{}
		expHeadMut := &extprocv3.HeaderMutation{}
		mm := &mockImageGenerationMetrics{}
		mt := &mockImageGenerationTranslator{
			t: t, expResponseBody: inBody,
			retBodyMutation: expBodyMut, retHeaderMutation: expHeadMut,
			retUsedToken: translator.LLMTokenUsage{OutputTokens: 123, InputTokens: 1},
		}

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)
		p := &imageGenerationProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			config: &processorConfig{
				metadataNamespace:  "ai_gateway_llm_ns",
				modelNameHeaderKey: "x-aigw-model",
				requestCosts: []processorConfigRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{
						celProg:        celProgInt,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_int"},
					},
					{
						celProg:        celProgUint,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_uint"},
					},
				},
			},
			requestHeaders:    map[string]string{"x-aigw-model": "ai_gateway_llm"},
			responseHeaders:   map[string]string{":status": "200"},
			backendName:       "some_backend",
			modelNameOverride: "ai_gateway_llm",
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)
		mm.RequireRequestSuccess(t)
		require.Equal(t, 124, mm.tokenUsageCount) // 1 input + 123 output

		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(123), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["output_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
		require.Equal(t, "ai_gateway_llm", md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["model_name_override"].GetStringValue())
		require.Equal(t, "some_backend", md.Fields["ai_gateway_llm_ns"].GetStructValue().Fields["backend_name"].GetStringValue())
	})

	// Verify we record failure for non-2xx responses and do it exactly once (defer suppressed).
	t.Run("non-2xx status failure once", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("error-body"), EndOfStream: true}
		expHeadMut := &extprocv3.HeaderMutation{}
		expBodyMut := &extprocv3.BodyMutation{}
		mm := &mockImageGenerationMetrics{}
		mt := &mockImageGenerationTranslator{t: t, expResponseBody: inBody, retHeaderMutation: expHeadMut, retBodyMutation: expBodyMut}
		p := &imageGenerationProcessorUpstreamFilter{
			translator:      mt,
			metrics:         mm,
			responseHeaders: map[string]string{":status": "500"},
			logger:          slog.Default(),
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)
		mm.RequireRequestFailure(t)
	})
}

func Test_imageGenerationProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("ok with auth handler and header mutator", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/images/generations", "x-model": "dall-e-3"}
		mm := &mockImageGenerationMetrics{}
		body := &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModel("dall-e-3"), Prompt: "a cat"}
		mt := &mockImageGenerationTranslator{t: t, expRequestBody: body}
		p := &imageGenerationProcessorUpstreamFilter{
			config:                 &processorConfig{modelNameHeaderKey: "x-model"},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			originalRequestBodyRaw: imageGenerationBodyFromModel(t, "dall-e-3"),
			originalRequestBody:    body,
			handler:                &mockBackendAuthHandler{},
			translator:             mt,
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

// Mock translator for image generation tests
type mockImageGenerationTranslator struct {
	t                           *testing.T
	expRequestBody              *openaisdk.ImageGenerateParams
	expResponseBody             *extprocv3.HttpBody
	expHeaders                  map[string]string
	expForceRequestBodyMutation bool
	retErr                      error
	retHeaderMutation           *extprocv3.HeaderMutation
	retBodyMutation             *extprocv3.BodyMutation
	retUsedToken                translator.LLMTokenUsage
	retImageMetadata            translator.ImageGenerationMetadata
}

func (m *mockImageGenerationTranslator) RequestBody(original []byte, req *openaisdk.ImageGenerateParams, forceBodyMutation bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody, req)
	}
	if m.expForceRequestBodyMutation {
		require.True(m.t, forceBodyMutation)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockImageGenerationTranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	if m.expHeaders != nil {
		for k, v := range m.expHeaders {
			require.Equal(m.t, v, headers[k])
		}
	}
	return m.retHeaderMutation, m.retErr
}

func (m *mockImageGenerationTranslator) ResponseBody(headers map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, translator.LLMTokenUsage, translator.ImageGenerationMetadata, error) {
	if m.expResponseBody != nil {
		bodyBytes, _ := io.ReadAll(body)
		require.Equal(m.t, m.expResponseBody.Body, bodyBytes)
		require.Equal(m.t, m.expResponseBody.EndOfStream, endOfStream)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retImageMetadata, m.retErr
}

func (m *mockImageGenerationTranslator) ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// imageGenerationBodyFromModel returns a minimal valid image generation request for tests.
func imageGenerationBodyFromModel(t *testing.T, model string) []byte {
	t.Helper()
	b, err := json.Marshal(&openaisdk.ImageGenerateParams{Model: openaisdk.ImageModel(model), Prompt: "a cat"})
	require.NoError(t, err)
	return b
}

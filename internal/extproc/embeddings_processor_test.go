// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

func TestEmbeddings_Schema(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: "Foo", Version: "v123"}}
		_, err := EmbeddingsProcessorFactory(nil)(cfg, nil, slog.Default(), false)
		require.ErrorContains(t, err, "unsupported API schema: Foo")
	})
	t.Run("supported openai / on route", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		routeFilter, err := EmbeddingsProcessorFactory(nil)(cfg, nil, slog.Default(), false)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &embeddingsProcessorRouterFilter{}, routeFilter)
	})
	t.Run("supported openai / on upstream", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		routeFilter, err := EmbeddingsProcessorFactory(nil)(cfg, nil, slog.Default(), true)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &embeddingsProcessorUpstreamFilter{}, routeFilter)
	})
}

func Test_embeddingsProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	e := &embeddingsProcessorUpstreamFilter{}
	t.Run("unsupported", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"})
		require.ErrorContains(t, err, "unsupported API schema: backend={Bar v123}")
	})
	t.Run("supported openai", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI})
		require.NoError(t, err)
		require.NotNil(t, e.translator)
	})
}

func Test_embeddingsProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &embeddingsProcessorRouterFilter{}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})
	t.Run("router error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("test error")}
		p := &embeddingsProcessorRouterFilter{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: embeddingBodyFromModel(t, "some-model")})
		require.ErrorContains(t, err, "failed to calculate route: test error")
	})
	t.Run("router error 404", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: x.ErrNoMatchingRule}
		p := &embeddingsProcessorRouterFilter{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: embeddingBodyFromModel(t, "some-model")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		ir := resp.GetImmediateResponse()
		require.NotNil(t, ir)
		require.Equal(t, typev3.StatusCode_NotFound, ir.GetStatus().GetCode())
		require.Equal(t, x.ErrNoMatchingRule.Error(), string(ir.GetBody()))
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retRouteName: "some-route"}
		const modelKey = "x-ai-gateway-model-key"
		const modelRouteKey = "x-ai-gateway-route-key"
		p := &embeddingsProcessorRouterFilter{
			config:         &processorConfig{router: rt, modelNameHeaderKey: modelKey, selectedRouteHeaderKey: modelRouteKey},
			requestHeaders: headers,
			logger:         slog.Default(),
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: embeddingBodyFromModel(t, "some-model")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.RequestBody)
		setHeaders := re.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 3)
		require.Equal(t, modelKey, setHeaders[0].Header.Key)
		require.Equal(t, "some-model", string(setHeaders[0].Header.RawValue))
		require.Equal(t, modelRouteKey, setHeaders[1].Header.Key)
		require.Equal(t, "some-route", string(setHeaders[1].Header.RawValue))
		require.Equal(t, "x-ai-eg-original-path", setHeaders[2].Header.Key)
		require.Equal(t, "/foo", string(setHeaders[2].Header.RawValue))
	})
}

func Test_embeddingsProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockEmbeddingsMetrics{}
		mt := &mockEmbeddingTranslator{t: t, expHeaders: make(map[string]string)}
		p := &embeddingsProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
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
		mm := &mockEmbeddingsMetrics{}
		mt := &mockEmbeddingTranslator{t: t, expHeaders: expHeaders}
		p := &embeddingsProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		mm.RequireRequestNotCompleted(t)
	})
}

func embeddingBodyFromModel(_ *testing.T, model string) []byte {
	return fmt.Appendf(nil, `{"model":"%s","input":"test input"}`, model)
}

func Test_embeddingsProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockEmbeddingsMetrics{}
		mt := &mockEmbeddingTranslator{t: t}
		p := &embeddingsProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "test error")
		mm.RequireRequestFailure(t)
		mm.RequireTokensRecorded(t, 0)
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("some-body"), EndOfStream: true}
		expBodyMut := &extprocv3.BodyMutation{}
		expHeadMut := &extprocv3.HeaderMutation{}
		mm := &mockEmbeddingsMetrics{}
		mt := &mockEmbeddingTranslator{
			t: t, expResponseBody: inBody,
			retBodyMutation: expBodyMut, retHeaderMutation: expHeadMut,
			retUsedToken: translator.LLMTokenUsage{InputTokens: 123, TotalTokens: 123},
		}

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)
		p := &embeddingsProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			config: &processorConfig{
				metadataNamespace: "ai_gateway_llm_ns",
				requestCosts: []processorConfigRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeTotalToken, MetadataKey: "total_token_usage"}},
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
			backendName:       "some_backend",
			modelNameOverride: "some_model",
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)
		mm.RequireRequestSuccess(t)
		mm.RequireTokensRecorded(t, 1)

		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(123), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(123), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["total_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
		require.Equal(t, "some_backend", md.Fields["route"].
			GetStructValue().Fields["backend_name"].GetStringValue())
		require.Equal(t, "some_model", md.Fields["route"].
			GetStructValue().Fields["model_name_override"].GetStringValue())
	})
}

func Test_embeddingsProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/foo"}
	mm := &mockEmbeddingsMetrics{}
	p := &embeddingsProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        mm,
	}
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:   "some-backend",
		Schema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
	}, nil, &embeddingsProcessorRouterFilter{})
	require.ErrorContains(t, err, "unsupported API schema: backend={some-schema v10.0}")
	mm.RequireRequestFailure(t)
	mm.RequireTokensRecorded(t, 0)
	mm.RequireSelectedBackend(t, "some-backend")
}

func Test_embeddingsProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	const modelKey = "x-ai-gateway-model-key"
	t.Run("translator error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo", modelKey: "some-model"}
		someBody := embeddingBodyFromModel(t, "some-model")
		var body openai.EmbeddingRequest
		require.NoError(t, json.Unmarshal(someBody, &body))
		tr := mockEmbeddingTranslator{t: t, retErr: errors.New("test error"), expRequestBody: &body}
		mm := &mockEmbeddingsMetrics{}
		p := &embeddingsProcessorUpstreamFilter{
			config: &processorConfig{
				modelNameHeaderKey: modelKey,
			},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             tr,
			originalRequestBodyRaw: someBody,
			originalRequestBody:    &body,
		}
		_, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.ErrorContains(t, err, "failed to transform request: test error")
		mm.RequireRequestFailure(t)
		mm.RequireTokensRecorded(t, 0)
		mm.RequireSelectedModel(t, "some-model")
	})
	t.Run("ok", func(t *testing.T) {
		someBody := embeddingBodyFromModel(t, "some-model")
		headers := map[string]string{":path": "/foo", modelKey: "some-model"}
		headerMut := &extprocv3.HeaderMutation{}
		bodyMut := &extprocv3.BodyMutation{}

		var expBody openai.EmbeddingRequest
		require.NoError(t, json.Unmarshal(someBody, &expBody))
		mt := mockEmbeddingTranslator{t: t, expRequestBody: &expBody, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		mm := &mockEmbeddingsMetrics{}
		p := &embeddingsProcessorUpstreamFilter{
			config: &processorConfig{
				selectedRouteHeaderKey: "x-ai-gateway-backend-key",
				modelNameHeaderKey:     modelKey,
			},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: someBody,
			originalRequestBody:    &expBody,
		}
		resp, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)
		require.Equal(t, mt, p.translator)
		require.NotNil(t, resp)
		commonRes := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders).RequestHeaders.Response
		require.Equal(t, headerMut, commonRes.HeaderMutation)
		require.Equal(t, bodyMut, commonRes.BodyMutation)

		mm.RequireRequestNotCompleted(t)
		mm.RequireSelectedModel(t, "some-model")
	})
}

func TestEmbeddings_ParseBody(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		jsonBody := `{"model":"text-embedding-ada-002","input":"test input"}`
		modelName, rb, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{Body: []byte(jsonBody)})
		require.NoError(t, err)
		require.Equal(t, "text-embedding-ada-002", modelName)
		require.NotNil(t, rb)
		require.Equal(t, "text-embedding-ada-002", rb.Model)
		require.Equal(t, "test input", rb.Input.Value)
	})
	t.Run("error", func(t *testing.T) {
		modelName, rb, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{})
		require.Error(t, err)
		require.Empty(t, modelName)
		require.Nil(t, rb)
	})
}

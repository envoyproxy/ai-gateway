// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func TestResponsesProcessorFactory(t *testing.T) {
	cfg := &processorConfig{}
	rf, err := ResponsesProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, false)
	require.NoError(t, err)
	require.IsType(t, &responsesProcessorRouterFilter{}, rf)

	uf, err := ResponsesProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, true)
	require.NoError(t, err)
	require.IsType(t, &responsesProcessorUpstreamFilter{}, uf)
}

func Test_parseOpenAIResponseBody(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		// Use raw JSON to avoid issues with union-type marshalling in test structs.
		b := []byte(`{"model":"m1"}`)
		model, rr, err := parseOpenAIResponseBody(&extprocv3.HttpBody{Body: b})
		require.NoError(t, err)
		require.Equal(t, "m1", model)
		// Ensure parsed request has the expected model value
		require.Equal(t, "m1", rr.Model)
	})
	t.Run("error missing model", func(t *testing.T) {
		b := []byte(`{}`)
		_, _, err := parseOpenAIResponseBody(&extprocv3.HttpBody{Body: b})
		require.Error(t, err)
	})
	t.Run("error invalid json", func(t *testing.T) {
		_, _, err := parseOpenAIResponseBody(&extprocv3.HttpBody{Body: []byte("notjson")})
		require.Error(t, err)
	})
}

// mockResponsesTranslator is a lightweight translator mock for Responses.
type mockResponsesTranslator struct {
	t                 *testing.T
	expHeaders        map[string]string
	expBody           []byte
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retUsedToken      translator.LLMTokenUsage
	retResponseModel  internalapi.ResponseModel
	retErr            error
}

func (m *mockResponsesTranslator) RequestBody(raw []byte, _ *openai.ResponseRequest, _ bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.t != nil && m.expBody != nil {
		require.Equal(m.t, m.expBody, raw)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockResponsesTranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	if m.t != nil && m.expHeaders != nil {
		require.Equal(m.t, m.expHeaders, headers)
	}
	return m.retHeaderMutation, m.retErr
}

func (m *mockResponsesTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracing.ResponsesSpan) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, translator.LLMTokenUsage, internalapi.ResponseModel, error) {
	if m.t != nil && m.expBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expBody, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retResponseModel, m.retErr
}

func (m *mockResponsesTranslator) ResponseError(_ map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.t != nil && m.expBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expBody, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// mockResponsesMetrics implements metrics.ResponsesMetrics for tests.
type mockResponsesMetrics struct {
	started            bool
	originalModel      internalapi.OriginalModel
	requestModel       internalapi.RequestModel
	responseModel      internalapi.ResponseModel
	backend            string
	successCount       int
	failureCount       int
	tokenUsageCount    int
	streamingTokens    int
	timeToFirstTokenMs float64
	interTokenLatency  float64
}

func (m *mockResponsesMetrics) StartRequest(_ map[string]string) { m.started = true }
func (m *mockResponsesMetrics) SetOriginalModel(originalModel internalapi.OriginalModel) {
	m.originalModel = originalModel
}

func (m *mockResponsesMetrics) SetRequestModel(requestModel internalapi.RequestModel) {
	m.requestModel = requestModel
}

func (m *mockResponsesMetrics) SetResponseModel(responseModel internalapi.ResponseModel) {
	m.responseModel = responseModel
}
func (m *mockResponsesMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }
func (m *mockResponsesMetrics) RecordRequestCompletion(_ context.Context, success bool, _ map[string]string) {
	if success {
		m.successCount++
	} else {
		m.failureCount++
	}
}

func (m *mockResponsesMetrics) RecordTokenUsage(_ context.Context, input, _, output uint32, _ map[string]string) {
	m.tokenUsageCount += int(input + output)
}

func (m *mockResponsesMetrics) RecordTokenLatency(_ context.Context, output uint32, _ bool, _ map[string]string) {
	m.streamingTokens += int(output)
}
func (m *mockResponsesMetrics) GetTimeToFirstTokenMs() float64  { return m.timeToFirstTokenMs }
func (m *mockResponsesMetrics) GetInterTokenLatencyMs() float64 { return m.interTokenLatency }

func (m *mockResponsesMetrics) RequireRequestSuccess(t *testing.T) {
	require.Equal(t, 1, m.successCount)
	require.Zero(t, m.failureCount)
}

func (m *mockResponsesMetrics) RequireRequestFailure(t *testing.T) {
	require.Equal(t, 1, m.failureCount)
	require.Zero(t, m.successCount)
}

func Test_responsesProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &responsesProcessorRouterFilter{tracer: tracing.NoopResponsesTracer{}}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.Error(t, err)
	})

	t.Run("ok with tracer injection", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		// Build a router (using NoopResponsesTracer) and validate header mutation output
		r := &responsesProcessorRouterFilter{
			config:         &processorConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         tracing.NoopResponsesTracer{},
		}

		// call with valid body
		raw := []byte(`{"model":"m-router"}`)
		resp, err := r.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: raw})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		require.NotNil(t, re.RequestBody)
		setHeaders := re.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 2)
		require.Equal(t, internalapi.ModelNameHeaderKeyDefault, setHeaders[0].Header.Key)
		require.Equal(t, "m-router", string(setHeaders[0].Header.RawValue))
	})
}

func Test_responsesProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	c := &responsesProcessorUpstreamFilter{}
	t.Run("unsupported", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"})
		require.ErrorContains(t, err, "unsupported API schema for Responses API")
	})
	t.Run("supported openai", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI})
		require.NoError(t, err)
		require.NotNil(t, c.translator)
	})
}

func Test_responsesProcessorUpstreamFilter_ProcessResponseHeaders_and_Body(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.Error(t, err)
		mm.RequireRequestFailure(t)
	})

	t.Run("ok non-streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}}}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t, expHeaders: expHeaders}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		require.Nil(t, res.ModeOverride)
	})

	t.Run("ok streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":status", Value: "200"}, {Key: "dog", RawValue: []byte("cat")}}}
		expHeaders := map[string]string{":status": "200", "dog": "cat"}
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t, expHeaders: expHeaders}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		require.Equal(t, &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}, res.ModeOverride)
	})

	t.Run("ProcessResponseBody error translation", func(t *testing.T) {
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
		require.Error(t, err)
		mm.RequireRequestFailure(t)
	})

	t.Run("non-2xx status failure", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("error-body"), EndOfStream: true}
		expHeadMut := &extprocv3.HeaderMutation{}
		expBodyMut := &extprocv3.BodyMutation{}
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t, expBody: []byte("error-body"), retHeaderMutation: expHeadMut, retBodyMutation: expBodyMut}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm, responseHeaders: map[string]string{":status": "500"}}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)
		mm.RequireRequestFailure(t)
	})

	t.Run("streaming completion only at end", func(t *testing.T) {
		mm := &mockResponsesMetrics{}
		mt := &mockResponsesTranslator{t: t}
		p := &responsesProcessorUpstreamFilter{translator: mt, metrics: mm, stream: true, responseHeaders: map[string]string{":status": "200"}, config: &processorConfig{}}

		// First chunk (not end of stream)
		chunk := &extprocv3.HttpBody{Body: []byte("chunk-1"), EndOfStream: false}
		mt.expBody = chunk.Body
		mt.retUsedToken = translator.LLMTokenUsage{} // no usage yet
		_, err := p.ProcessResponseBody(t.Context(), chunk)
		require.NoError(t, err)
		require.Zero(t, mm.tokenUsageCount)

		// Final chunk should mark success and record usage once.
		final := &extprocv3.HttpBody{Body: []byte("chunk-final"), EndOfStream: true}
		mt.expBody = final.Body
		mt.retUsedToken = translator.LLMTokenUsage{InputTokens: 5, CachedInputTokens: 3, OutputTokens: 138}
		// Set some config costs to trigger metadata creation
		p.config = &processorConfig{requestCosts: []processorConfigRequestCost{{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage"}}}}
		p.requestHeaders = map[string]string{internalapi.ModelNameHeaderKeyDefault: "ai_gateway"}
		res, err := p.ProcessResponseBody(t.Context(), final)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.NotNil(t, commonRes)
		mm.RequireRequestSuccess(t)
		require.Equal(t, 143, mm.tokenUsageCount) // 5 input + 138 output
		// dynamic metadata should exist
		md := res.DynamicMetadata
		require.NotNil(t, md)
		got := md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields["output_token_usage"].GetNumberValue()
		require.Equal(t, float64(138), got)
	})
}

func Test_responsesProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/foo"}
	mm := &mockResponsesMetrics{}
	p := &responsesProcessorUpstreamFilter{config: &processorConfig{}, requestHeaders: headers, logger: slog.Default(), metrics: mm}
	err := p.SetBackend(t.Context(), &filterapi.Backend{Name: "some-backend", Schema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"}}, nil, &responsesProcessorRouterFilter{})
	require.Error(t, err)
	mm.RequireRequestFailure(t)

	// Success path
	headers2 := map[string]string{":path": "/foo", internalapi.ModelNameHeaderKeyDefault: "header-model"}
	mm2 := &mockResponsesMetrics{}
	p2 := &responsesProcessorUpstreamFilter{config: &processorConfig{}, requestHeaders: headers2, logger: slog.Default(), metrics: mm2}
	rp := &responsesProcessorRouterFilter{originalRequestBody: &openai.ResponseRequest{Stream: true}}
	err = p2.SetBackend(t.Context(), &filterapi.Backend{Name: "openai", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"}, ModelNameOverride: "ai_gateway_llm"}, nil, rp)
	require.NoError(t, err)
	require.Equal(t, "ai_gateway_llm", p2.requestHeaders[internalapi.ModelNameHeaderKeyDefault])
	require.True(t, p2.stream)
	require.NotNil(t, p2.translator)
}

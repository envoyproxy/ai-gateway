// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	cohere "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

func TestRerank_Schema(t *testing.T) {
	t.Run("on route", func(t *testing.T) {
		cfg := &filterapi.RuntimeConfig{}
		p, err := RerankProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.IsType(t, &rerankProcessorRouterFilter{}, p)
	})
	t.Run("on upstream", func(t *testing.T) {
		cfg := &filterapi.RuntimeConfig{}
		p, err := RerankProcessorFactory(func() metrics.RerankMetrics { return &mockRerankMetrics{} })(cfg, nil, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.IsType(t, &rerankProcessorUpstreamFilter{}, p)
	})
}

func Test_rerankProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	r := &rerankProcessorUpstreamFilter{}
	t.Run("unsupported", func(t *testing.T) {
		err := r.selectTranslator(filterapi.VersionedAPISchema{Name: "Unknown", Version: "vX"})
		require.ErrorContains(t, err, "unsupported API schema")
	})
}

func Test_rerankProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &rerankProcessorRouterFilter{
			tracer: tracing.NoopRerankTracer{},
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/cohere/v2/rerank"}
		p := &rerankProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         tracing.NoopRerankTracer{},
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: rerankBodyFromModel("rerank-english-v3")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		req := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		setHeaders := req.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 2)
		require.Equal(t, internalapi.ModelNameHeaderKeyDefault, setHeaders[0].Header.Key)
		require.Equal(t, "rerank-english-v3", string(setHeaders[0].Header.RawValue))
		require.Equal(t, originalPathHeader, setHeaders[1].Header.Key)
		require.Equal(t, "/cohere/v2/rerank", string(setHeaders[1].Header.RawValue))
	})
}

func Test_rerankProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("translator error", func(t *testing.T) {
		headers := map[string]string{":path": "/cohere/v2/rerank", internalapi.ModelNameHeaderKeyDefault: "rerank-english-v3"}
		raw := rerankBodyFromModel("rerank-english-v3")
		var body cohere.RerankV2Request
		require.NoError(t, json.Unmarshal(raw, &body))
		mt := &mockRerankTranslator{t: t, expRequestBody: &body, retErr: errors.New("boom")}
		mm := &mockRerankMetrics{}
		p := &rerankProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: raw,
			originalRequestBody:    &body,
		}
		_, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.ErrorContains(t, err, "failed to transform request: boom")
		mm.RequireRequestFailure(t)
		mm.RequireTokenUsage(t, 0)
		// Models captured even on failure
		require.Equal(t, "rerank-english-v3", mm.originalModel)
		require.Equal(t, "rerank-english-v3", mm.requestModel)
		require.Empty(t, mm.responseModel)
	})

	t.Run("ok with header mutation and auth", func(t *testing.T) {
		raw := rerankBodyFromModel("rerank-english-v3")
		var body cohere.RerankV2Request
		require.NoError(t, json.Unmarshal(raw, &body))
		headers := map[string]string{":path": "/cohere/v2/rerank", internalapi.ModelNameHeaderKeyDefault: "rerank-english-v3"}
		headerMut := []internalapi.Header{{"foo", "bar"}}
		bodyMut := []byte("patched")
		mt := &mockRerankTranslator{t: t, expRequestBody: &body, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		mm := &mockRerankMetrics{}
		p := &rerankProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: raw,
			originalRequestBody:    &body,
			handler:                &mockBackendAuthHandler{},
		}
		resp, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)
		req := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
		common := req.RequestHeaders.Response
		require.Len(t, common.HeaderMutation.SetHeaders, 2)
		require.Equal(t, "foo", common.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "bar", string(common.HeaderMutation.SetHeaders[0].Header.RawValue))
		require.Equal(t, "foo", common.HeaderMutation.SetHeaders[1].Header.Key)
		require.Equal(t, "mock-auth-handler", string(common.HeaderMutation.SetHeaders[1].Header.RawValue))
		require.Equal(t, "patched", string(common.BodyMutation.GetBody()))
		// Not completed yet
		mm.RequireRequestNotCompleted(t)
		require.Equal(t, "rerank-english-v3", mm.originalModel)
		require.Equal(t, "rerank-english-v3", mm.requestModel)
	})
}

func Test_rerankProcessorUpstreamFilter_ProcessRequestHeaders_HeaderMutatorMerge(t *testing.T) {
	// Translator returns no mutations; header mutator should still apply remove/set.
	raw := rerankBodyFromModel("rerank-english-v3")
	var body cohere.RerankV2Request
	require.NoError(t, json.Unmarshal(raw, &body))
	headers := map[string]string{":path": "/cohere/v2/rerank", "authorization": "Bearer xyz"}
	mt := &mockRerankTranslator{t: t, expRequestBody: &body}
	mm := &mockRerankMetrics{}
	mutCfg := &filterapi.HTTPHeaderMutation{
		Remove: []string{"authorization"},
		Set:    []filterapi.HTTPHeader{{Name: "x-api-key", Value: "k"}},
	}
	p := &rerankProcessorUpstreamFilter{
		config:                 &filterapi.RuntimeConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             mt,
		originalRequestBodyRaw: raw,
		originalRequestBody:    &body,
		handler:                &mockBackendAuthHandler{},
		headerMutator:          headermutator.NewHeaderMutator(mutCfg, headers),
	}
	resp, err := p.ProcessRequestHeaders(t.Context(), nil)
	require.NoError(t, err)
	req := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	common := req.RequestHeaders.Response
	require.NotNil(t, common.HeaderMutation)
	// Expect removal of authorization and setting x-api-key
	require.Contains(t, common.HeaderMutation.RemoveHeaders, "authorization")
	foundSet := false
	for _, h := range common.HeaderMutation.SetHeaders {
		if h.Header.Key == "x-api-key" && string(h.Header.RawValue) == "k" {
			foundSet = true
			break
		}
	}
	require.True(t, foundSet)
}

func Test_rerankProcessorUpstreamFilter_ProcessRequestHeaders_AuthError(t *testing.T) {
	raw := rerankBodyFromModel("rerank-english-v3")
	var body cohere.RerankV2Request
	require.NoError(t, json.Unmarshal(raw, &body))
	headers := map[string]string{":path": "/cohere/v2/rerank", internalapi.ModelNameHeaderKeyDefault: "rerank-english-v3"}
	mt := &mockRerankTranslator{t: t, expRequestBody: &body}
	mm := &mockRerankMetrics{}
	p := &rerankProcessorUpstreamFilter{
		config:                 &filterapi.RuntimeConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             mt,
		originalRequestBodyRaw: raw,
		originalRequestBody:    &body,
		handler:                &mockBackendAuthHandlerError{},
	}
	_, err := p.ProcessRequestHeaders(t.Context(), nil)
	require.ErrorContains(t, err, "failed to do auth request")
	mm.RequireRequestFailure(t)
}

func Test_rerankProcessorUpstreamFilter_ProcessRequestBody_Panic(t *testing.T) {
	p := &rerankProcessorUpstreamFilter{}
	require.Panics(t, func() { _, _ = p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{}) })
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseHeaders_ErrorAndEncoding(t *testing.T) {
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t, retErr: errors.New("hdr fail")}
	p := &rerankProcessorUpstreamFilter{translator: mt, metrics: mm}
	inHeaders := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "content-encoding", Value: "gzip"}}}
	_, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
	require.ErrorContains(t, err, "failed to transform response headers")
	mm.RequireRequestFailure(t)
}

func gzipBytes(t *testing.T, b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(b)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_DecodeAndRemoveContentEncoding(t *testing.T) {
	plain := []byte("ok")
	gz := gzipBytes(t, plain)
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{
		t:               t,
		expResponseBody: &extprocv3.HttpBody{Body: plain},
		retBodyMutation: []byte("mut"),
	}
	p := &rerankProcessorUpstreamFilter{
		translator:       mt,
		metrics:          mm,
		responseHeaders:  map[string]string{":status": "200"},
		responseEncoding: "gzip",
	}
	res, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: gz, EndOfStream: false})
	require.NoError(t, err)
	common := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
	require.Contains(t, common.HeaderMutation.RemoveHeaders, "content-encoding")
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_DecodeError(t *testing.T) {
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t}
	p := &rerankProcessorUpstreamFilter{
		translator:       mt,
		metrics:          mm,
		responseHeaders:  map[string]string{":status": "200"},
		responseEncoding: "gzip",
	}
	// Invalid gzip payload triggers decode error
	_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("not-gzip"), EndOfStream: true})
	require.Error(t, err)
	mm.RequireRequestFailure(t)
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_ErrorTransformError(t *testing.T) {
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t, retErr: errors.New("boom")}
	p := &rerankProcessorUpstreamFilter{
		translator:      mt,
		metrics:         mm,
		responseHeaders: map[string]string{":status": "500"},
	}
	_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("err"), EndOfStream: true})
	require.ErrorContains(t, err, "failed to transform response error")
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_ResponseTransformError(t *testing.T) {
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t, retErr: errors.New("boom")}
	p := &rerankProcessorUpstreamFilter{
		translator:      mt,
		metrics:         mm,
		responseHeaders: map[string]string{":status": "200"},
	}
	_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("ok"), EndOfStream: true})
	require.ErrorContains(t, err, "failed to transform response")
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_MetadataError(t *testing.T) {
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{
		t:               t,
		expResponseBody: &extprocv3.HttpBody{Body: []byte("ok")},
	}
	p := &rerankProcessorUpstreamFilter{
		translator:      mt,
		metrics:         mm,
		config:          &filterapi.RuntimeConfig{RequestCosts: []filterapi.RuntimeRequestCost{{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostType("unknown"), MetadataKey: "x"}}}},
		responseHeaders: map[string]string{":status": "200"},
	}
	_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("ok"), EndOfStream: true})
	require.ErrorContains(t, err, "failed to build dynamic metadata")
}

func Test_rerankProcessorUpstreamFilter_SetBackend_SupportedWithOverride(t *testing.T) {
	headers := map[string]string{":path": "/cohere/v2/rerank"}
	mm := &mockRerankMetrics{}
	p := &rerankProcessorUpstreamFilter{config: &filterapi.RuntimeConfig{}, requestHeaders: headers, logger: slog.Default(), metrics: mm}
	rp := &rerankProcessorRouterFilter{requestHeaders: headers}
	err := p.SetBackend(t.Context(), &filterapi.Backend{ModelNameOverride: "override", Name: "cohere-backend", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere, Version: "v2"}}, nil, rp)
	require.NoError(t, err)
	require.NotNil(t, p.translator)
	require.NotNil(t, p.headerMutator)
	require.Equal(t, "override", headers[internalapi.ModelNameHeaderKeyDefault])
	require.Equal(t, "override", mm.requestModel)
	require.Equal(t, p, rp.upstreamFilter)
}

func Test_rerankProcessorUpstreamFilter_SetBackend_PanicWrongRoute(t *testing.T) {
	p := &rerankProcessorUpstreamFilter{config: &filterapi.RuntimeConfig{}, requestHeaders: map[string]string{}, logger: slog.Default(), metrics: &mockRerankMetrics{}}
	require.Panics(t, func() {
		_ = p.SetBackend(t.Context(), &filterapi.Backend{Name: "b", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere, Version: "v2"}}, nil, &mockProcessor{})
	})
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	inHeaders := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
	exp := map[string]string{"foo": "bar"}
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t, expHeaders: exp}
	p := &rerankProcessorUpstreamFilter{translator: mt, metrics: mm}
	res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
	require.NoError(t, err)
	common := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
	require.Empty(t, common.HeaderMutation.SetHeaders)
	mm.RequireRequestNotCompleted(t)
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error path", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("err"), EndOfStream: true}
		mm := &mockRerankMetrics{}
		mt := &mockRerankTranslator{t: t, expResponseBody: inBody}
		p := &rerankProcessorUpstreamFilter{
			translator:      mt,
			metrics:         mm,
			responseHeaders: map[string]string{":status": "500"},
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		require.NotNil(t, res)
		req := res.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.NotNil(t, req.ResponseBody.Response)
		require.True(t, mt.responseErrorCalled)
		mm.RequireRequestFailure(t)
	})

	t.Run("success with metadata and token usage", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("ok"), EndOfStream: true}
		mm := &mockRerankMetrics{}
		mt := &mockRerankTranslator{
			t:               t,
			expResponseBody: inBody,
			retUsedToken: translator.LLMTokenUsage{
				InputTokens: 10,
				TotalTokens: 10,
			},
			retResponseModel: "rerank-english-v3-2025",
		}
		celProgInt, err := llmcostcel.NewProgram("123")
		require.NoError(t, err)
		p := &rerankProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.Default(),
			metrics:    mm,
			config: &filterapi.RuntimeConfig{RequestCosts: []filterapi.RuntimeRequestCost{
				{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
				{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeTotalToken, MetadataKey: "total_token_usage"}},
				{CELProg: celProgInt, LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_int"}},
			}},
			requestHeaders:  map[string]string{internalapi.ModelNameHeaderKeyDefault: "header-model"},
			backendName:     "cohere-backend",
			responseHeaders: map[string]string{":status": "200"},
		}
		resp, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		common := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.NotNil(t, common.HeaderMutation)
		require.Nil(t, common.BodyMutation)
		mm.RequireTokenUsage(t, 10)
		mm.RequireRequestSuccess(t)
		// Response model chosen is retResponseModel
		require.Equal(t, "rerank-english-v3-2025", mm.responseModel)

		md := resp.DynamicMetadata
		require.NotNil(t, md)
		inner := md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields
		require.Equal(t, float64(10), inner["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(10), inner["total_token_usage"].GetNumberValue())
		require.Equal(t, float64(123), inner["cel_int"].GetNumberValue())
		require.Equal(t, "cohere-backend", inner["backend_name"].GetStringValue())
	})

	// Ensure success is recorded only at end-of-stream
	t.Run("completion only at end", func(t *testing.T) {
		mm := &mockRerankMetrics{}
		mt := &mockRerankTranslator{t: t}
		p := &rerankProcessorUpstreamFilter{
			translator:      mt,
			logger:          slog.Default(),
			metrics:         mm,
			config:          &filterapi.RuntimeConfig{},
			backendName:     "b",
			responseHeaders: map[string]string{":status": "200"},
		}
		chunk := &extprocv3.HttpBody{Body: []byte("c1"), EndOfStream: false}
		mt.expResponseBody = chunk
		_, err := p.ProcessResponseBody(t.Context(), chunk)
		require.NoError(t, err)
		mm.RequireRequestNotCompleted(t)
		final := &extprocv3.HttpBody{Body: []byte("c2"), EndOfStream: true}
		mt.expResponseBody = final
		_, err = p.ProcessResponseBody(t.Context(), final)
		require.NoError(t, err)
		mm.RequireRequestSuccess(t)
	})
}

func Test_rerankProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/cohere/v2/rerank"}
	mm := &mockRerankMetrics{}
	p := &rerankProcessorUpstreamFilter{config: &filterapi.RuntimeConfig{}, requestHeaders: headers, logger: slog.Default(), metrics: mm}
	err := p.SetBackend(t.Context(), &filterapi.Backend{Name: "some-backend", Schema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "vX"}}, nil, &rerankProcessorRouterFilter{})
	require.ErrorContains(t, err, "unsupported API schema")
	mm.RequireRequestFailure(t)
	mm.RequireSelectedBackend(t, "some-backend")
}

func Test_rerankProcessorRouterFilter_PassthroughResponses(t *testing.T) {
	t.Run("no upstream filter", func(t *testing.T) {
		p := &rerankProcessorRouterFilter{}
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.NoError(t, err)
		_, err = p.ProcessResponseBody(t.Context(), nil)
		require.NoError(t, err)
	})

	t.Run("with upstream filter", func(t *testing.T) {
		p := &rerankProcessorRouterFilter{
			upstreamFilter: &rerankProcessorUpstreamFilter{
				translator: &mockRerankTranslator{t: t, expHeaders: map[string]string{}},
				logger:     slog.Default(),
				metrics:    &mockRerankMetrics{},
				config:     &filterapi.RuntimeConfig{},
			},
		}
		resp, err := p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{Headers: []*corev3.HeaderValue{}})
		require.NoError(t, err)
		require.NotNil(t, resp)
		resp, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("body")})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_Tracing_EndSpanOnError(t *testing.T) {
	inBody := &extprocv3.HttpBody{Body: []byte("err"), EndOfStream: true}
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{t: t, expResponseBody: inBody}
	span := &mockRerankSpan{}
	p := &rerankProcessorUpstreamFilter{
		translator:      mt,
		metrics:         mm,
		responseHeaders: map[string]string{":status": "500"},
		span:            span,
	}
	res, err := p.ProcessResponseBody(t.Context(), inBody)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, mt.responseErrorCalled)
	require.Equal(t, 500, span.endErrStatus)
	require.Equal(t, "err", span.endErrBody)
}

func Test_rerankProcessorUpstreamFilter_ProcessResponseBody_Tracing_EndSpanOnSuccess(t *testing.T) {
	inBody := &extprocv3.HttpBody{Body: []byte("ok"), EndOfStream: true}
	mm := &mockRerankMetrics{}
	mt := &mockRerankTranslator{
		t:               t,
		expResponseBody: inBody,
	}
	span := &mockRerankSpan{}
	p := &rerankProcessorUpstreamFilter{
		translator:      mt,
		logger:          slog.Default(),
		metrics:         mm,
		config:          &filterapi.RuntimeConfig{},
		responseHeaders: map[string]string{":status": "200"},
		span:            span,
	}
	_, err := p.ProcessResponseBody(t.Context(), inBody)
	require.NoError(t, err)
	require.True(t, span.endCalled)
}

// Helpers and mocks

func rerankBodyFromModel(model string) []byte {
	return []byte(`{"model":"` + model + `","query":"q","documents":["d1","d2"]}`)
}

type mockRerankTranslator struct {
	t                   *testing.T
	expHeaders          map[string]string
	expRequestBody      *cohere.RerankV2Request
	expResponseBody     *extprocv3.HttpBody
	retHeaderMutation   []internalapi.Header
	retBodyMutation     []byte
	retUsedToken        translator.LLMTokenUsage
	retResponseModel    internalapi.ResponseModel
	retErr              error
	responseErrorCalled bool
}

func (m *mockRerankTranslator) RequestBody(raw []byte, body *cohere.RerankV2Request, onRetry bool) ([]internalapi.Header, []byte, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody.Model, body.Model)
	}
	_ = raw
	_ = onRetry
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockRerankTranslator) ResponseHeaders(headers map[string]string) ([]internalapi.Header, error) {
	for k, v := range m.expHeaders {
		require.Equal(m.t, v, headers[k])
	}
	return m.retHeaderMutation, m.retErr
}

func (m *mockRerankTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) ([]internalapi.Header, []byte, translator.LLMTokenUsage, internalapi.ResponseModel, error) {
	if m.expResponseBody != nil {
		got, _ := io.ReadAll(body)
		require.True(m.t, bytes.Equal(m.expResponseBody.Body, got))
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retResponseModel, m.retErr
}

func (m *mockRerankTranslator) ResponseError(_ map[string]string, _ io.Reader) ([]internalapi.Header, []byte, error) {
	m.responseErrorCalled = true
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

type mockRerankMetrics struct {
	completed     *bool
	originalModel string
	requestModel  string
	responseModel string
	backend       string
	inputTokens   uint32
}

func (m *mockRerankMetrics) StartRequest(map[string]string) {}
func (m *mockRerankMetrics) SetOriginalModel(model internalapi.OriginalModel) {
	m.originalModel = model
}
func (m *mockRerankMetrics) SetRequestModel(model internalapi.RequestModel) { m.requestModel = model }
func (m *mockRerankMetrics) SetResponseModel(model internalapi.ResponseModel) {
	m.responseModel = model
}
func (m *mockRerankMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }
func (m *mockRerankMetrics) RecordTokenUsage(_ context.Context, input uint32, _ map[string]string) {
	m.inputTokens += input
}

func (m *mockRerankMetrics) RecordRequestCompletion(_ context.Context, success bool, _ map[string]string) {
	m.completed = &success
}

func (m *mockRerankMetrics) RequireRequestSuccess(t *testing.T) {
	require.NotNil(t, m.completed)
	require.True(t, *m.completed)
}

func (m *mockRerankMetrics) RequireRequestFailure(t *testing.T) {
	require.NotNil(t, m.completed)
	require.False(t, *m.completed)
}

func (m *mockRerankMetrics) RequireRequestNotCompleted(t *testing.T) {
	require.Nil(t, m.completed)
}

func (m *mockRerankMetrics) RequireTokenUsage(t *testing.T, input uint32) {
	require.Equal(t, input, m.inputTokens)
}

func (m *mockRerankMetrics) RequireSelectedBackend(t *testing.T, backend string) {
	require.Equal(t, backend, m.backend)
}

func TestRerankProcessorUpstreamFilter_ProcessRequestHeaders_WithBodyMutations(t *testing.T) {
	t.Run("body mutations applied correctly", func(t *testing.T) {
		headers := map[string]string{
			":path":         "/cohere/v2/rerank",
			"x-ai-eg-model": "rerank-english-v3",
		}

		requestBody := &cohere.RerankV2Request{
			Model: "rerank-english-v3",
		}
		requestBodyRaw := []byte(`{"model": "rerank-english-v3", "query": "What is AI?", "documents": ["doc1", "doc2"], "return_documents": false, "top_k": 10}`)

		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"internal_flag"},
			Set: []filterapi.HTTPBodyField{
				{Path: "top_k", Value: "20"},
				{Path: "return_documents", Value: "true"},
				{Path: "max_chunks_per_doc", Value: "5"},
			},
		}

		mockTranslator := mockRerankTranslator{
			t:               t,
			expRequestBody:  requestBody,
			retBodyMutation: requestBodyRaw,
			retErr:          nil,
		}

		rerankMetrics := &mockRerankMetrics{}

		p := &rerankProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                rerankMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			translator:             &mockTranslator,
			handler:                &mockBackendAuthHandler{},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere},
			BodyMutation: bodyMutations,
		}

		rp := &rerankProcessorRouterFilter{
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			requestHeaders:         headers,
		}

		err := p.SetBackend(context.Background(), backend, &mockBackendAuthHandler{}, rp)
		require.NoError(t, err)

		require.NotNil(t, p.bodyMutator)

		ctx := context.Background()
		response, err := p.ProcessRequestHeaders(ctx, nil)
		require.NoError(t, err)
		require.NotNil(t, response)

		testBodyMutation := []byte(`{"model": "rerank-english-v3", "query": "What is AI?", "documents": ["doc1", "doc2"], "return_documents": false, "top_k": 10, "internal_flag": true}`)
		mutatedBody, err := p.bodyMutator.Mutate(testBodyMutation, false)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, float64(20), result["top_k"])
		require.Equal(t, true, result["return_documents"])
		require.Equal(t, float64(5), result["max_chunks_per_doc"])
		require.NotContains(t, result, "internal_flag")
		require.Equal(t, "rerank-english-v3", result["model"])
		require.Equal(t, "What is AI?", result["query"])
	})

	t.Run("body mutator with retry", func(t *testing.T) {
		headers := map[string]string{":path": "/cohere/v2/rerank"}
		rerankMetrics := &mockRerankMetrics{}

		originalRequestBodyRaw := []byte(`{"model": "rerank-english-v3", "query": "Original query", "return_documents": false}`)
		requestBody := &cohere.RerankV2Request{
			Model: "rerank-english-v3",
		}

		p := &rerankProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                rerankMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
		}

		bodyMutations := &filterapi.HTTPBodyMutation{
			Set: []filterapi.HTTPBodyField{
				{Path: "return_documents", Value: "true"},
				{Path: "top_k", Value: "15"},
			},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere},
			BodyMutation: bodyMutations,
		}

		rp := &rerankProcessorRouterFilter{
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
			requestHeaders:         headers,
			upstreamFilterCount:    2,
		}

		err := p.SetBackend(context.Background(), backend, &mockBackendAuthHandler{}, rp)
		require.NoError(t, err)

		require.NotNil(t, p.bodyMutator)
		require.True(t, p.onRetry)

		modifiedBody := []byte(`{"model": "rerank-english-v3", "query": "Modified query", "return_documents": false, "extra": "field"}`)
		mutatedBody, err := p.bodyMutator.Mutate(modifiedBody, true)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, true, result["return_documents"])
		require.Equal(t, float64(15), result["top_k"])
		require.Equal(t, "rerank-english-v3", result["model"])
		require.NotContains(t, result, "extra")

		require.Equal(t, "Original query", result["query"])
	})
}

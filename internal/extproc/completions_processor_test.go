// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func TestCompletions_Schema(t *testing.T) {
	tests := []struct {
		name         string
		onUpstream   bool
		expectedType interface{}
	}{
		{
			name:         "supported openai / on route",
			onUpstream:   false,
			expectedType: &completionsProcessorRouterFilter{},
		},
		{
			name:         "supported openai / on upstream",
			onUpstream:   true,
			expectedType: &completionsProcessorUpstreamFilter{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &filterapi.RuntimeConfig{}
			filter, err := CompletionsProcessorFactory(&mockMetricsFactory{}, tracing.NoopCompletionTracer{})(cfg, nil, slog.Default(), tt.onUpstream)
			require.NoError(t, err)
			require.NotNil(t, filter)
			require.IsType(t, tt.expectedType, filter)
		})
	}
}

func Test_completionsProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	tests := []struct {
		name          string
		schema        filterapi.VersionedAPISchema
		expectedError string
	}{
		{
			name:          "unsupported",
			schema:        filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"},
			expectedError: "unsupported API schema: backend={Bar v123}",
		},
		{
			name:          "supported openai",
			schema:        filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &completionsProcessorUpstreamFilter{}
			err := c.selectTranslator(tt.schema)
			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, c.translator)
			}
		})
	}
}

func Test_completionsProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &completionsProcessorRouterFilter{}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.EqualError(t, err, "failed to parse request body: failed to unmarshal body: invalid character 'o' in literal null (expecting 'u')")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		p := &completionsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         tracing.NoopTracing{}.CompletionTracer(),
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: completionBodyFromModel(t, "some-model")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.RequestBody)
		setHeaders := re.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 2)
		require.Equal(t, internalapi.ModelNameHeaderKeyDefault, setHeaders[0].Header.Key)
		require.Equal(t, "some-model", string(setHeaders[0].Header.RawValue))
		require.Equal(t, "x-ai-eg-original-path", setHeaders[1].Header.Key)
		require.Equal(t, "/foo", string(setHeaders[1].Header.RawValue))
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t, expHeaders: make(map[string]string)}
		p := &completionsProcessorUpstreamFilter{
			translator: mt,
		}
		const headerName = ":test-header:"
		const headerValue = ":test-header-value:"
		mt.expHeaders[headerName] = headerValue
		mt.resHeaderMutation = []internalapi.Header{{headerName, headerValue}}

		resp, err := p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{
				{Key: headerName, RawValue: []byte(headerValue)},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseHeaders)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseHeaders)
		setHeaders := re.ResponseHeaders.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 1)
		require.Equal(t, headerName, setHeaders[0].Header.Key)
		require.Equal(t, headerValue, string(setHeaders[0].Header.RawValue))
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error response", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		mm := &mockMetrics{}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "400"},
			metrics:         mm,
		}

		mt.resErrorHeaderMutation = []internalapi.Header{{"test", "error"}}
		mt.resErrorBodyMutation = []byte("test error")

		resp, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("test error")})
		require.NoError(t, err)
		require.NotNil(t, resp)

		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseBody)
		require.Len(t, re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders, 1)
		require.Equal(t, "test", re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders[0].Header.Key)
		require.Equal(t, "error", string(re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders[0].Header.RawValue))
		require.Equal(t, "test error", string(re.ResponseBody.GetResponse().GetBodyMutation().GetBody()))
	})

	t.Run("successful response with token usage", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		mm := &mockMetrics{}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			logger:          slog.Default(),
			metrics:         mm,
		}

		mt.resHeaderMutation = []internalapi.Header{{"test", "success"}}
		mt.resBodyMutation = []byte("response body")
		mt.resTokenUsage.SetInputTokens(10)
		mt.resTokenUsage.SetOutputTokens(20)
		mt.resTokenUsage.SetTotalTokens(30)
		mt.resModel = "gpt-4"

		resp, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("test"), EndOfStream: true})
		require.NoError(t, err)
		require.NotNil(t, resp)

		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseBody)
		require.Len(t, re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders, 1)
		require.Equal(t, "test", re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders[0].Header.Key)
		require.Equal(t, "success", string(re.ResponseBody.GetResponse().GetHeaderMutation().SetHeaders[0].Header.RawValue))
		require.Equal(t, "response body", string(re.ResponseBody.GetResponse().GetBodyMutation().GetBody()))

		in, ok := p.costs.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), in)
		out, ok := p.costs.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(20), out)
		total, ok := p.costs.TotalTokens()
		require.True(t, ok)
		require.Equal(t, uint32(30), total)
	})
}

func Test_completionsProcessorUpstreamFilter_SetBackend(t *testing.T) {
	tests := []struct {
		name                        string
		routeFilterCount            int
		backend                     *filterapi.Backend
		expectedModelOverride       string
		expectedBackendName         string
		expectedOnRetry             bool
		expectedUpstreamFilterCount int
	}{
		{
			name:             "set backend with model override",
			routeFilterCount: 0,
			backend: &filterapi.Backend{
				Name:              "test-backend",
				ModelNameOverride: "override-model",
				Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
				HeaderMutation: &filterapi.HTTPHeaderMutation{
					Set: []filterapi.HTTPHeader{
						{Name: "backend-header", Value: "value"},
					},
				},
			},
			expectedModelOverride:       "override-model",
			expectedBackendName:         "test-backend",
			expectedOnRetry:             false,
			expectedUpstreamFilterCount: 1,
		},
		{
			name:             "retry request",
			routeFilterCount: 1,
			backend: &filterapi.Backend{
				Name:   "retry-backend",
				Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			},
			expectedModelOverride:       "",
			expectedBackendName:         "retry-backend",
			expectedOnRetry:             true,
			expectedUpstreamFilterCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routeFilter := &completionsProcessorRouterFilter{
				config:                 &filterapi.RuntimeConfig{},
				requestHeaders:         make(map[string]string),
				originalRequestBody:    &openai.CompletionRequest{Model: "test-model"},
				originalRequestBodyRaw: []byte(`{"model":"test-model"}`),
				upstreamFilterCount:    tt.routeFilterCount,
			}
			upstreamFilter := &completionsProcessorUpstreamFilter{
				config:         routeFilter.config,
				requestHeaders: routeFilter.requestHeaders,
				metrics:        &mockMetrics{},
			}

			err := upstreamFilter.SetBackend(t.Context(), tt.backend, nil, routeFilter)
			require.NoError(t, err)
			require.Equal(t, tt.expectedModelOverride, upstreamFilter.modelNameOverride)
			require.Equal(t, tt.expectedBackendName, upstreamFilter.backendName)
			require.Equal(t, routeFilter.originalRequestBody, upstreamFilter.originalRequestBody)
			require.Equal(t, routeFilter.originalRequestBodyRaw, upstreamFilter.originalRequestBodyRaw)
			require.Equal(t, tt.expectedOnRetry, upstreamFilter.onRetry)
			require.NotNil(t, upstreamFilter.translator)
			if tt.backend.HeaderMutation != nil {
				require.NotNil(t, upstreamFilter.headerMutator)
			}
			if tt.expectedModelOverride != "" {
				require.Equal(t, tt.expectedModelOverride, upstreamFilter.requestHeaders[internalapi.ModelNameHeaderKeyDefault])
			}
			require.Equal(t, upstreamFilter, routeFilter.upstreamFilter)
			require.Equal(t, tt.expectedUpstreamFilterCount, routeFilter.upstreamFilterCount)
		})
	}
}

func Test_completionsProcessorRouterFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t, expHeaders: make(map[string]string)}
		upstreamFilter := &completionsProcessorUpstreamFilter{
			translator: mt,
		}
		routeFilter := &completionsProcessorRouterFilter{
			upstreamFilter: upstreamFilter,
		}

		headers := &corev3.HeaderMap{}
		resp, err := routeFilter.ProcessResponseHeaders(t.Context(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		routeFilter := &completionsProcessorRouterFilter{}
		headers := &corev3.HeaderMap{}
		resp, err := routeFilter.ProcessResponseHeaders(t.Context(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func Test_completionsProcessorRouterFilter_ProcessResponseBody(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		upstreamFilter := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "200"},
			metrics:         &mockMetrics{},
			config:          &filterapi.RuntimeConfig{},
		}
		routeFilter := &completionsProcessorRouterFilter{
			upstreamFilter: upstreamFilter,
		}

		body := &extprocv3.HttpBody{Body: []byte("test")}
		resp, err := routeFilter.ProcessResponseBody(t.Context(), body)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		routeFilter := &completionsProcessorRouterFilter{}
		body := &extprocv3.HttpBody{Body: []byte("test")}
		resp, err := routeFilter.ProcessResponseBody(t.Context(), body)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	mt := &mockCompletionTranslator{t: t}
	upstreamFilter := &completionsProcessorUpstreamFilter{
		config:                 &filterapi.RuntimeConfig{},
		requestHeaders:         make(map[string]string),
		originalRequestBody:    &openai.CompletionRequest{Model: "test"},
		originalRequestBodyRaw: []byte(`{"model":"test"}`),
		translator:             mt,
		metrics:                &mockMetrics{},
	}

	headers := &corev3.HeaderMap{}
	resp, err := upstreamFilter.ProcessRequestHeaders(t.Context(), headers)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify CONTINUE_AND_REPLACE status
	rh := resp.GetRequestHeaders()
	require.NotNil(t, rh)
	require.Equal(t, extprocv3.CommonResponse_CONTINUE_AND_REPLACE, rh.GetResponse().GetStatus())
}

func TestCompletionsProcessorUpstreamFilter_UnimplementedMethods(t *testing.T) {
	p := &completionsProcessorUpstreamFilter{}
	_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{})
	require.ErrorIs(t, err, errUnexpectedCall)
}

// Helper function to create completion request body
func completionBodyFromModel(t *testing.T, model string) []byte {
	t.Helper()
	req := openai.CompletionRequest{
		Model:  model,
		Prompt: openai.PromptUnion{Value: "test prompt"},
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return b
}

// Mock translator for testing
type mockCompletionTranslator struct {
	t                      *testing.T
	expHeaders             map[string]string
	resHeaderMutation      []internalapi.Header
	resBodyMutation        []byte
	resErrorHeaderMutation []internalapi.Header
	resErrorBodyMutation   []byte
	resTokenUsage          metrics.TokenUsage
	resModel               internalapi.ResponseModel
	err                    error
}

func (m *mockCompletionTranslator) RequestBody([]byte, *openai.CompletionRequest, bool) ([]internalapi.Header, []byte, error) {
	return nil, nil, m.err
}

func (m *mockCompletionTranslator) ResponseHeaders(headers map[string]string) ([]internalapi.Header, error) {
	for k, v := range m.expHeaders {
		require.Equal(m.t, v, headers[k])
	}
	return m.resHeaderMutation, m.err
}

func (m *mockCompletionTranslator) ResponseBody(map[string]string, io.Reader, bool, tracing.CompletionSpan) ([]internalapi.Header, []byte, metrics.TokenUsage, internalapi.ResponseModel, error) {
	return m.resHeaderMutation, m.resBodyMutation, m.resTokenUsage, m.resModel, m.err
}

func (m *mockCompletionTranslator) ResponseError(map[string]string, io.Reader) ([]internalapi.Header, []byte, error) {
	return m.resErrorHeaderMutation, m.resErrorBodyMutation, m.err
}

// mockCompletionTracer implements tracing.CompletionTracer for testing span creation.
type mockCompletionTracer struct {
	tracing.NoopTracer[openai.CompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponse]
	startSpanCalled bool
	returnedSpan    tracing.CompletionSpan
}

func (m *mockCompletionTracer) StartSpanAndInjectHeaders(_ context.Context, _ map[string]string, carrier propagation.TextMapCarrier, _ *openai.CompletionRequest, _ []byte) tracing.CompletionSpan {
	m.startSpanCalled = true
	carrier.Set("tracing-header", "1")
	if m.returnedSpan != nil {
		return m.returnedSpan
	}
	return nil
}

func Test_completionsProcessorRouterFilter_ProcessRequestBody_SpanCreation(t *testing.T) {
	t.Run("span creation", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/completions"}
		span := &mockCompletionSpan{}
		mockTracerInstance := &mockCompletionTracer{returnedSpan: span}

		p := &completionsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         mockTracerInstance,
		}

		// Test with non-streaming request.
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: completionBodyFromModel(t, "test-model")})
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
				Key:      "tracing-header",
				RawValue: []byte("1"),
			},
		})
	})
}

// mockCompletionSpan implements tracing.CompletionSpan for testing.
type mockCompletionSpan struct {
	recordedChunks []*openai.CompletionResponse
	recordedResp   *openai.CompletionResponse
	endSpanCalled  bool
	errorStatus    int
	errBody        string
}

func (m *mockCompletionSpan) RecordResponseChunk(chunk *openai.CompletionResponse) {
	m.recordedChunks = append(m.recordedChunks, chunk)
}

func (m *mockCompletionSpan) RecordResponse(resp *openai.CompletionResponse) {
	m.recordedResp = resp
}

func (m *mockCompletionSpan) EndSpan() {
	m.endSpanCalled = true
}

func (m *mockCompletionSpan) EndSpanOnError(statusCode int, body []byte) {
	m.errorStatus = statusCode
	m.errBody = string(body)
	m.endSpanCalled = true
}

func TestCompletionsProcessorRouterFilter_ProcessResponseBody_SpanHandling(t *testing.T) {
	t.Run("passthrough without span", func(t *testing.T) {
		p := &completionsProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.CompletionRequest{},
		}
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true, Body: []byte("response")})
		require.NoError(t, err)
		// Should not panic when span is nil.
	})

	t.Run("upstream filter span success", func(t *testing.T) {
		span := &mockCompletionSpan{}
		mt := &mockCompletionTranslator{t: t}
		p := &completionsProcessorRouterFilter{
			originalRequestBody: &openai.CompletionRequest{},
			upstreamFilter: &completionsProcessorUpstreamFilter{
				responseHeaders: map[string]string{":status": "200"},
				translator:      mt,
				logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				config:          &filterapi.RuntimeConfig{},
				span:            span,
				metrics:         &mockMetrics{},
			},
		}

		finalBody := &extprocv3.HttpBody{EndOfStream: true, Body: []byte("final")}
		_, err := p.ProcessResponseBody(t.Context(), finalBody)
		require.NoError(t, err)
		require.True(t, span.endSpanCalled)
	})

	t.Run("upstream filter error with span", func(t *testing.T) {
		span := &mockCompletionSpan{}
		p := &completionsProcessorRouterFilter{
			originalRequestBody: &openai.CompletionRequest{},
			upstreamFilter: &completionsProcessorUpstreamFilter{
				responseHeaders: map[string]string{":status": "500"},
				translator:      &mockCompletionTranslator{t: t},
				logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				config:          &filterapi.RuntimeConfig{},
				span:            span,
				metrics:         &mockMetrics{},
			},
		}

		errorBody := "error response"
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true, Body: []byte(errorBody)})
		require.NoError(t, err)
		require.Equal(t, 500, span.errorStatus)
		require.Equal(t, errorBody, span.errBody)
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseHeaders_Streaming(t *testing.T) {
	t.Run("ok/non-streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mt := &mockCompletionTranslator{t: t, expHeaders: expHeaders}
		p := &completionsProcessorUpstreamFilter{
			translator: mt,
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation.SetHeaders)
		require.Nil(t, res.ModeOverride)
	})

	t.Run("ok/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "200"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "200", "dog": "cat"}
		mt := &mockCompletionTranslator{t: t, expHeaders: expHeaders}
		p := &completionsProcessorUpstreamFilter{translator: mt, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation.SetHeaders)
		require.Equal(t, &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}, res.ModeOverride)
	})

	t.Run("error/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "500"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "500", "dog": "cat"}
		mt := &mockCompletionTranslator{t: t, expHeaders: expHeaders}
		p := &completionsProcessorUpstreamFilter{translator: mt, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation.SetHeaders)
		require.Nil(t, res.ModeOverride)
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseBody_Streaming(t *testing.T) {
	t.Run("streaming completion only at end", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		mm := &mockMetrics{}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			stream:          true,
			responseHeaders: map[string]string{":status": "200"},
			logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:          &filterapi.RuntimeConfig{},
			metrics:         mm,
		}
		// First chunk (not end of stream) should not complete the request.
		chunk := &extprocv3.HttpBody{Body: []byte("chunk-1"), EndOfStream: false}
		mt.resTokenUsage = metrics.TokenUsage{} // no usage yet in early chunks.
		_, err := p.ProcessResponseBody(t.Context(), chunk)
		require.NoError(t, err)
		mm.RequireRequestNotCompleted(t)
		require.Zero(t, mm.streamingOutputTokens) // first chunk has 0 output tokens

		// Final chunk should mark success and record usage once.
		final := &extprocv3.HttpBody{Body: []byte("chunk-final"), EndOfStream: true}
		mt.resTokenUsage.SetInputTokens(5)
		mt.resTokenUsage.SetOutputTokens(138)
		mt.resTokenUsage.SetTotalTokens(143)
		_, err = p.ProcessResponseBody(t.Context(), final)
		require.NoError(t, err)
		mm.RequireRequestSuccess(t)
		require.Equal(t, 5, mm.inputTokenCount)
		require.Equal(t, 138, mm.outputTokenCount)
		require.Equal(t, 138, mm.streamingOutputTokens) // accumulated output tokens from stream
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseBody_NonSuccess(t *testing.T) {
	// Verify we record failure for non-2xx responses and do it exactly once (defer suppressed).
	t.Run("non-2xx status failure once", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("error-body"), EndOfStream: true}
		mm := &mockMetrics{}
		mt := &mockCompletionTranslator{t: t, resErrorHeaderMutation: []internalapi.Header{
			{"foo", "bar"},
		}, resErrorBodyMutation: []byte("abcd")}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			metrics:         mm,
			responseHeaders: map[string]string{":status": "500"},
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, "abcd", string(commonRes.BodyMutation.GetBody()))
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "bar", string(commonRes.HeaderMutation.SetHeaders[0].Header.RawValue))
		mm.RequireRequestFailure(t)
	})
}

func Test_completionsProcessorUpstreamFilter_SetBackend_Failure(t *testing.T) {
	headers := map[string]string{":path": "/foo"}
	mm := &mockMetrics{}
	p := &completionsProcessorUpstreamFilter{
		config:         &filterapi.RuntimeConfig{},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        mm,
	}
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:              "some-backend",
		Schema:            filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
		ModelNameOverride: "ai_gateway_llm",
	}, nil, &completionsProcessorRouterFilter{
		originalRequestBody:    &openai.CompletionRequest{},
		originalRequestBodyRaw: []byte(`{}`),
	})
	require.EqualError(t, err, "failed to select translator: unsupported API schema: backend={some-schema v10.0}")
	mm.RequireRequestFailure(t)
	require.Zero(t, mm.inputTokenCount)
	mm.RequireSelectedBackend(t, "some-backend")
	require.False(t, p.stream) // On error, stream should be false regardless of the input.
}

func Test_completionsProcessorUpstreamFilter_SetBackend_Success(t *testing.T) {
	tests := []struct {
		name           string
		stream         bool
		expectedStream bool
	}{
		{
			name:           "non-streaming",
			stream:         false,
			expectedStream: false,
		},
		{
			name:           "streaming",
			stream:         true,
			expectedStream: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{":path": "/foo", internalapi.ModelNameHeaderKeyDefault: "some-model"}
			mm := &mockMetrics{}
			p := &completionsProcessorUpstreamFilter{
				config:         &filterapi.RuntimeConfig{},
				requestHeaders: headers,
				logger:         slog.Default(),
				metrics:        mm,
			}
			rp := &completionsProcessorRouterFilter{
				originalRequestBody: &openai.CompletionRequest{Stream: tt.stream},
			}

			backend := &filterapi.Backend{
				Name:              "openai",
				Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
				ModelNameOverride: "ai_gateway_llm",
			}

			err := p.SetBackend(t.Context(), backend, nil, rp)

			require.NoError(t, err)
			mm.RequireSelectedBackend(t, "openai")
			require.Equal(t, "ai_gateway_llm", p.requestHeaders[internalapi.ModelNameHeaderKeyDefault])
			require.Equal(t, tt.expectedStream, p.stream)
			require.NotNil(t, p.translator)
		})
	}
}

func TestCompletions_ParseBody(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		original := openai.CompletionRequest{
			Model:  "gpt-3.5-turbo-instruct",
			Prompt: openai.PromptUnion{Value: "test prompt"},
		}
		bytes, err := json.Marshal(original)
		require.NoError(t, err)

		modelName, rb, err := parseOpenAICompletionBody(&extprocv3.HttpBody{Body: bytes})
		require.NoError(t, err)
		require.Equal(t, "gpt-3.5-turbo-instruct", modelName)
		require.NotNil(t, rb)
	})

	t.Run("error", func(t *testing.T) {
		modelName, rb, err := parseOpenAICompletionBody(&extprocv3.HttpBody{})
		require.Error(t, err)
		require.Empty(t, modelName)
		require.Nil(t, rb)
	})
}

func Test_completionsProcessorUpstreamFilter_CELCostEvaluation(t *testing.T) {
	// Using exactly the same test data as chat completion CEL test
	t.Run("CEL expressions with token usage", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("response-body"), EndOfStream: true}
		expBody := []byte("response-body")
		mm := &mockMetrics{}
		mt := &mockCompletionTranslator{
			t:                 t,
			resBodyMutation:   expBody,
			resHeaderMutation: []internalapi.Header{{"foo", "bar"}},
		}
		mt.resTokenUsage.SetOutputTokens(123)
		mt.resTokenUsage.SetInputTokens(1)

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)

		p := &completionsProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			stream:     false,
			config: &filterapi.RuntimeConfig{
				RequestCosts: []filterapi.RuntimeRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{
						CELProg:        celProgInt,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_int"},
					},
					{
						CELProg:        celProgUint,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCEL, MetadataKey: "cel_uint"},
					},
				},
			},
			requestHeaders:    map[string]string{internalapi.ModelNameHeaderKeyDefault: "ai_gateway_llm"},
			responseHeaders:   map[string]string{":status": "200"},
			backendName:       "some_backend",
			modelNameOverride: "ai_gateway_llm",
		}

		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, string(expBody), string(commonRes.BodyMutation.GetBody()))
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "bar", string(commonRes.HeaderMutation.SetHeaders[0].Header.RawValue))
		mm.RequireRequestSuccess(t)
		require.Equal(t, 123, mm.outputTokenCount)
		require.Equal(t, 1, mm.inputTokenCount)
		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(123), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["output_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
		require.Equal(t, "ai_gateway_llm", md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields["model_name_override"].GetStringValue())
		require.Equal(t, "some_backend", md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields["backend_name"].GetStringValue())
	})
}

func Test_completionsProcessorUpstreamFilter_SensitiveHeaders_RemoveAndRestore(t *testing.T) {
	headerMutation := filterapi.HTTPHeaderMutation{
		Remove: []string{"authorization", "x-api-key"},
		Set:    []filterapi.HTTPHeader{{Name: "x-new-header", Value: "new-value"}},
	}
	originalHeaders := map[string]string{
		"authorization": "secret",
		"x-api-key":     "key123",
		"other":         "value",
	}
	body := openai.CompletionRequest{Model: "test-model"}
	raw := []byte(`{"model":"test-model"}`)

	t.Run("remove headers", func(t *testing.T) {
		p := &completionsProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"authorization": "secret", "x-api-key": "key123", "other": "value"},
			headerMutator:          headermutator.NewHeaderMutator(&headerMutation, originalHeaders),
			onRetry:                true,
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockCompletionTranslator{t: t},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
			metrics:                &mockMetrics{},
		}

		resp, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)

		headerMutation := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders).RequestHeaders.Response.HeaderMutation
		require.NotNil(t, headerMutation)
		require.ElementsMatch(t, []string{"authorization", "x-api-key"}, headerMutation.RemoveHeaders)
		// Sensitive headers remain locally for metrics, but will be stripped upstream by Envoy.
		require.Equal(t, "secret", p.requestHeaders["authorization"])
		require.Equal(t, "key123", p.requestHeaders["x-api-key"])
		require.Equal(t, "value", p.requestHeaders["other"])
	})

	t.Run("set headers", func(t *testing.T) {
		// Simulate that sensitive headers were removed and now need to be restored.
		p := &completionsProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"other": "value"},
			headerMutator:          headermutator.NewHeaderMutator(&filterapi.HTTPHeaderMutation{Set: headerMutation.Set}, originalHeaders),
			onRetry:                true, // not a retry, so should restore.
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockCompletionTranslator{t: t},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
			metrics:                &mockMetrics{},
		}

		// Call the actual method to trigger restoration logic.
		_, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)

		// Now check that sensitive headers are restored.
		require.Equal(t, "secret", p.requestHeaders["authorization"])
		require.Equal(t, "key123", p.requestHeaders["x-api-key"])
		require.Equal(t, "value", p.requestHeaders["other"])

		// Now check the set headers in the mutation.
		require.Equal(t, "new-value", p.requestHeaders["x-new-header"])
	})

	t.Run("restore headers", func(t *testing.T) {
		// Simulate that sensitive headers were removed and now need to be restored.
		p := &completionsProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"other": "value"},
			onRetry:                true, // not a retry, so should restore.
			headerMutator:          headermutator.NewHeaderMutator(nil, originalHeaders),
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockCompletionTranslator{t: t},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
			metrics:                &mockMetrics{},
		}

		// Call the actual method to trigger restoration logic.
		_, err := p.ProcessRequestHeaders(t.Context(), nil)
		require.NoError(t, err)

		// Now check that sensitive headers are restored.
		require.Equal(t, "secret", p.requestHeaders["authorization"])
		require.Equal(t, "key123", p.requestHeaders["x-api-key"])
		require.Equal(t, "value", p.requestHeaders["other"])
	})
}

func Test_completionsProcessorUpstreamFilter_ModelTracking(t *testing.T) {
	t.Run("sets request model in ProcessRequestHeaders", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/completions", internalapi.ModelNameHeaderKeyDefault: "header-model"}
		body := openai.CompletionRequest{Model: "body-model"}
		raw, _ := json.Marshal(body)
		mm := &mockMetrics{}
		p := &completionsProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             &mockCompletionTranslator{t: t},
			originalRequestBodyRaw: raw,
			originalRequestBody:    &body,
		}
		_, _ = p.ProcessRequestHeaders(t.Context(), nil)
		// Should use the override model from the header, as that's what is sent upstream.
		require.Equal(t, "body-model", mm.originalModel)
		require.Equal(t, "header-model", mm.requestModel)
		// Response model is not set until we get actual response
		require.Empty(t, mm.responseModel)
	})

	t.Run("uses actual response model from API", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/completions"}
		body := openai.CompletionRequest{Model: "gpt-3.5-turbo-instruct"}
		raw, _ := json.Marshal(body)
		mm := &mockMetrics{}
		// Create a mock translator that returns token usage with response model
		// Simulating OpenAI's automatic routing where gpt-3.5-turbo-instruct routes to gpt-3.5-turbo-instruct-0914
		mt := &mockCompletionTranslator{
			t:        t,
			resModel: "gpt-3.5-turbo-instruct-0914",
		}
		mt.resTokenUsage.SetOutputTokens(20)
		mt.resTokenUsage.SetInputTokens(10)

		p := &completionsProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			responseHeaders:        map[string]string{":status": "200"},
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: raw,
			originalRequestBody:    &body,
		}
		// First process request headers
		_, _ = p.ProcessRequestHeaders(t.Context(), nil)
		require.Equal(t, "gpt-3.5-turbo-instruct", mm.requestModel)

		// Then process response body
		resp := &extprocv3.HttpBody{Body: []byte("response"), EndOfStream: true}
		_, _ = p.ProcessResponseBody(t.Context(), resp)

		// Verify metrics have all three models
		mm.RequireSelectedModel(t, "gpt-3.5-turbo-instruct", "gpt-3.5-turbo-instruct", "gpt-3.5-turbo-instruct-0914")
	})
}

func Test_completionsProcessorUpstreamFilter_TokenLatencyMetadata(t *testing.T) {
	tests := []struct {
		name              string
		existingMetadata  map[string]*structpb.Value
		expectedTTFT      float64
		expectedITL       float64
		preservesExisting bool
	}{
		{
			name:              "empty metadata",
			existingMetadata:  nil,
			expectedTTFT:      1000.0,
			expectedITL:       500.0,
			preservesExisting: false,
		},
		{
			name: "existing metadata preserved",
			existingMetadata: map[string]*structpb.Value{
				"tokenCost":        {Kind: &structpb.Value_NumberValue{NumberValue: float64(200)}},
				"inputTokenUsage":  {Kind: &structpb.Value_NumberValue{NumberValue: float64(300)}},
				"outputTokenUsage": {Kind: &structpb.Value_NumberValue{NumberValue: float64(400)}},
			},
			expectedTTFT:      1000.0,
			expectedITL:       500.0,
			preservesExisting: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := &mockMetrics{}
			mt := &mockCompletionTranslator{t: t}
			p := &completionsProcessorUpstreamFilter{
				translator: mt,
				logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				metrics:    mm,
				stream:     true,
				config:     &filterapi.RuntimeConfig{},
			}

			// Create metadata with existing fields if specified
			var metadata *structpb.Struct
			if tt.existingMetadata != nil {
				existingInner := &structpb.Struct{Fields: tt.existingMetadata}
				metadata = &structpb.Struct{Fields: map[string]*structpb.Value{
					internalapi.AIGatewayFilterMetadataNamespace: structpb.NewStructValue(existingInner),
				}}
			} else {
				metadata = &structpb.Struct{Fields: map[string]*structpb.Value{}}
			}

			// Call the method being tested
			p.mergeWithTokenLatencyMetadata(metadata)

			// Verify the namespace exists
			val, ok := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace]
			require.True(t, ok)
			inner := val.GetStructValue()
			require.NotNil(t, inner)

			// Verify token latency metadata - matching the chat completion processor pattern
			ttftVal := inner.Fields["token_latency_ttft"]
			require.NotNil(t, ttftVal)
			require.Equal(t, tt.expectedTTFT, ttftVal.GetNumberValue())

			itlVal := inner.Fields["token_latency_itl"]
			require.NotNil(t, itlVal)
			require.Equal(t, tt.expectedITL, itlVal.GetNumberValue())

			// If existing metadata should be preserved, check it
			if tt.preservesExisting {
				for key, expectedValue := range tt.existingMetadata {
					actualValue, exists := inner.Fields[key]
					require.True(t, exists, "expected field %s to be preserved", key)
					require.Equal(t, expectedValue.GetNumberValue(), actualValue.GetNumberValue())
				}
			}
		})
	}
}

func Test_completionsProcessorUpstreamFilter_StreamingTokenLatencyTracking(t *testing.T) {
	t.Run("tracks token latency during streaming", func(t *testing.T) {
		mm := &mockMetrics{
			timeToFirstTokenMs:  750.0,
			interTokenLatencyMs: 250.0,
		}
		mt := &mockCompletionTranslator{
			t: t,
		}
		mt.resTokenUsage.SetOutputTokens(20)
		mt.resTokenUsage.SetInputTokens(5)

		// Build config with token metadata
		requestCosts := []filterapi.RuntimeRequestCost{
			{
				LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_tokens"},
			},
		}

		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:         mm,
			stream:          true,
			config:          &filterapi.RuntimeConfig{RequestCosts: requestCosts},
			responseHeaders: map[string]string{":status": "200"},
		}

		// Process multiple chunks
		chunks := []struct {
			body        []byte
			endOfStream bool
		}{
			{body: []byte("chunk1"), endOfStream: false},
			{body: []byte("chunk2"), endOfStream: false},
			{body: []byte("final"), endOfStream: true},
		}

		var lastResp *extprocv3.ProcessingResponse
		for _, chunk := range chunks {
			resp, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{
				Body:        chunk.body,
				EndOfStream: chunk.endOfStream,
			})
			require.NoError(t, err)
			if chunk.endOfStream {
				lastResp = resp
			}
		}

		// Verify metadata includes token latency on final chunk
		require.NotNil(t, lastResp)
		md := lastResp.DynamicMetadata
		require.NotNil(t, md)

		ns := md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue()
		require.NotNil(t, ns)

		// Check token latency metadata - matching the chat completion processor pattern
		ttftVal := ns.Fields["token_latency_ttft"]
		require.NotNil(t, ttftVal)
		require.Equal(t, 750.0, ttftVal.GetNumberValue())

		itlVal := ns.Fields["token_latency_itl"]
		require.NotNil(t, itlVal)
		require.Equal(t, 250.0, itlVal.GetNumberValue())
	})
}

func Test_completionsProcessorRouterFilter_ProcessResponseHeaders_ProcessResponseBody(t *testing.T) {
	t.Run("no ok path with passthrough", func(t *testing.T) {
		p := &completionsProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.CompletionRequest{},
		}
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.NoError(t, err)
		_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true})
		require.NoError(t, err)
	})

	t.Run("ok path with upstream filter", func(t *testing.T) {
		p := &completionsProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.CompletionRequest{},
			upstreamFilter: &completionsProcessorUpstreamFilter{
				translator: &mockCompletionTranslator{t: t, expHeaders: map[string]string{}},
				logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				config:     &filterapi.RuntimeConfig{},
				metrics:    &mockMetrics{},
			},
		}
		resp, err := p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{Headers: []*corev3.HeaderValue{}})
		require.NoError(t, err)
		require.NotNil(t, resp)

		resp, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("some body")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseBody)
		require.NotNil(t, re.ResponseBody.Response)
		require.IsType(t, &extprocv3.BodyMutation{}, re.ResponseBody.Response.BodyMutation)
		require.IsType(t, &extprocv3.HeaderMutation{}, re.ResponseBody.Response.HeaderMutation)
	})
}

func TestCompletionsProcessorUpstreamFilter_ProcessRequestHeaders_WithBodyMutations(t *testing.T) {
	t.Run("body mutations applied correctly", func(t *testing.T) {
		headers := map[string]string{
			":path":         "/v1/completions",
			"x-ai-eg-model": "gpt-3.5-turbo-instruct",
		}

		requestBody := &openai.CompletionRequest{
			Model: "gpt-3.5-turbo-instruct",
		}
		requestBodyRaw := []byte(`{"model": "gpt-3.5-turbo-instruct", "prompt": "Hello", "max_tokens": 1000, "temperature": 0.5}`)

		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"internal_flag"},
			Set: []filterapi.HTTPBodyField{
				{Path: "max_tokens", Value: "2000"},
				{Path: "temperature", Value: "0.8"},
				{Path: "suffix", Value: "\"\\n\\nGenerated by AI.\""},
			},
		}

		mockTranslator := mockCompletionTranslator{
			t:               t,
			resBodyMutation: requestBodyRaw,
			err:             nil,
		}

		completionMetrics := &mockMetrics{}

		p := &completionsProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                completionMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			translator:             &mockTranslator,
			handler:                &mockBackendAuthHandler{},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			BodyMutation: bodyMutations,
		}

		rp := &completionsProcessorRouterFilter{
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

		testBodyMutation := []byte(`{"model": "gpt-3.5-turbo-instruct", "prompt": "Hello", "max_tokens": 1000, "temperature": 0.5, "internal_flag": true}`)
		mutatedBody, err := p.bodyMutator.Mutate(testBodyMutation, false)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, float64(2000), result["max_tokens"])
		require.Equal(t, 0.8, result["temperature"])
		require.Equal(t, "\n\nGenerated by AI.", result["suffix"])
		require.NotContains(t, result, "internal_flag")
		require.Equal(t, "gpt-3.5-turbo-instruct", result["model"])
		require.Equal(t, "Hello", result["prompt"])
	})

	t.Run("body mutator with retry", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/completions"}
		completionMetrics := &mockMetrics{}

		originalRequestBodyRaw := []byte(`{"model": "gpt-3.5-turbo-instruct", "prompt": "Hello", "temperature": 0.5}`)
		requestBody := &openai.CompletionRequest{
			Model: "gpt-3.5-turbo-instruct",
		}

		p := &completionsProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                completionMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
		}

		bodyMutations := &filterapi.HTTPBodyMutation{
			Set: []filterapi.HTTPBodyField{
				{Path: "temperature", Value: "0.9"},
				{Path: "max_tokens", Value: "1500"},
			},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			BodyMutation: bodyMutations,
		}

		rp := &completionsProcessorRouterFilter{
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
			requestHeaders:         headers,
			upstreamFilterCount:    2,
		}

		err := p.SetBackend(context.Background(), backend, &mockBackendAuthHandler{}, rp)
		require.NoError(t, err)

		require.NotNil(t, p.bodyMutator)
		require.True(t, p.onRetry)

		modifiedBody := []byte(`{"model": "gpt-3.5-turbo-instruct", "prompt": "Modified prompt", "temperature": 1.0, "extra": "field"}`)
		mutatedBody, err := p.bodyMutator.Mutate(modifiedBody, true)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, 0.9, result["temperature"])
		require.Equal(t, float64(1500), result["max_tokens"])
		require.Equal(t, "gpt-3.5-turbo-instruct", result["model"])
		require.NotContains(t, result, "extra")

		require.Equal(t, "Hello", result["prompt"])
	})
}

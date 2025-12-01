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
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func TestChatCompletion_Schema(t *testing.T) {
	t.Run("supported openai / on route", func(t *testing.T) {
		cfg := &filterapi.RuntimeConfig{}
		routeFilter, err := ChatCompletionProcessorFactory(nil)(cfg, nil, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &chatCompletionProcessorRouterFilter{}, routeFilter)
	})
	t.Run("supported openai / on upstream", func(t *testing.T) {
		cfg := &filterapi.RuntimeConfig{}
		routeFilter, err := ChatCompletionProcessorFactory(&mockMetricsFactory{})(cfg, nil, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &chatCompletionProcessorUpstreamFilter{}, routeFilter)
	})
}

func Test_chatCompletionProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	c := &chatCompletionProcessorUpstreamFilter{}
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
		require.NotNil(t, c.translator)
	})
	t.Run("supported azure openai", func(t *testing.T) {
		err := c.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI})
		require.NoError(t, err)
		require.NotNil(t, c.translator)
	})
}

type mockTracer struct {
	tracing.NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	startSpanCalled bool
	returnedSpan    tracing.ChatCompletionSpan
}

func (m *mockTracer) StartSpanAndInjectHeaders(_ context.Context, _ map[string]string, carrier propagation.TextMapCarrier, _ *openai.ChatCompletionRequest, _ []byte) tracing.ChatCompletionSpan {
	m.startSpanCalled = true
	carrier.Set("tracing-header", "1")
	if m.returnedSpan != nil {
		return m.returnedSpan
	}
	return nil
}

func Test_chatCompletionProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &chatCompletionProcessorRouterFilter{
			tracer: tracing.NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{},
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		p := &chatCompletionProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         tracing.NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{},
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model", false, nil)})
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

	t.Run("span creation", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/chat/completions"}
		span := &testotel.MockSpan{}
		mockTracerInstance := &mockTracer{returnedSpan: span}

		p := &chatCompletionProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			tracer:         mockTracerInstance,
		}

		// Test with streaming request.
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "test-model", true, nil)})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, mockTracerInstance.startSpanCalled)
		require.Equal(t, span, p.span)
		require.True(t, p.originalRequestBody.Stream)

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

	t.Run("ok_stream_without_include_usage", func(t *testing.T) {
		for _, opt := range []*openai.StreamOptions{nil, {IncludeUsage: false}} {
			headers := map[string]string{":path": "/foo"}
			p := &chatCompletionProcessorRouterFilter{
				config: &filterapi.RuntimeConfig{
					// Ensure that the stream_options.include_usage be forced to true.
					RequestCosts: []filterapi.RuntimeRequestCost{{}},
				},
				requestHeaders: headers,
				logger:         slog.Default(),
				tracer:         tracing.NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{},
			}
			resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model", true, opt)})
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, p.originalRequestBody.StreamOptions)
			require.True(t, p.forcedStreamOptionIncludeUsage)
			require.True(t, p.originalRequestBody.StreamOptions.IncludeUsage)
			require.Contains(t, string(p.originalRequestBodyRaw), `"stream_options":{"include_usage":true}`)
		}
	})
}

func Test_chatCompletionProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t, expHeaders: make(map[string]string)}
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.ErrorContains(t, err, "test error")
		mm.RequireRequestFailure(t)
	})
	t.Run("ok/non-streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation.SetHeaders)
		mm.RequireRequestNotCompleted(t)
		require.Nil(t, res.ModeOverride)
	})
	t.Run("ok/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "200"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "200", "dog": "cat"}
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessorUpstreamFilter{translator: mt, metrics: mm, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation)
		require.Equal(t, &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}, res.ModeOverride)
	})
	t.Run("error/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "500"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "500", "dog": "cat"}
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessorUpstreamFilter{translator: mt, metrics: mm, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Empty(t, commonRes.HeaderMutation)
		require.Nil(t, res.ModeOverride)
	})
}

func Test_chatCompletionProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t}
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			metrics:    mm,
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "test error")
		mm.RequireRequestFailure(t)
		require.Zero(t, mm.inputTokenCount)
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("some-body"), EndOfStream: true}
		mm := &mockMetrics{}
		mt := &mockTranslator{
			t: t, expResponseBody: inBody,
			retHeaderMutation: []internalapi.Header{{"foo", "bar"}},
		}
		mt.retUsedToken.SetOutputTokens(123)
		mt.retUsedToken.SetInputTokens(1)
		mt.retUsedToken.SetCachedInputTokens(1)

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			stream:     true,
			config: &filterapi.RuntimeConfig{
				RequestCosts: []filterapi.RuntimeRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCachedInputToken, MetadataKey: "cached_input_token_usage"}},
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
		require.Nil(t, commonRes.BodyMutation)
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "bar", string(commonRes.HeaderMutation.SetHeaders[0].Header.RawValue))
		mm.RequireRequestSuccess(t)
		require.Equal(t, 1, mm.inputTokenCount)
		require.Equal(t, 123, mm.outputTokenCount)

		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(123), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["output_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["cached_input_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields[internalapi.AIGatewayFilterMetadataNamespace].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
		require.Equal(t, "ai_gateway_llm", md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields["model_name_override"].GetStringValue())
		require.Equal(t, "some_backend", md.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue().Fields["backend_name"].GetStringValue())
	})

	// Verify we record failure for non-2xx responses and do it exactly once (defer suppressed).
	t.Run("non-2xx status failure once", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("error-body"), EndOfStream: true}
		expHeadMut := []internalapi.Header{{"foo", "bar"}}
		expBodyMut := []byte("error-body")
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t, expResponseBody: inBody, retHeaderMutation: expHeadMut, retBodyMutation: expBodyMut}
		p := &chatCompletionProcessorUpstreamFilter{
			translator:      mt,
			metrics:         mm,
			responseHeaders: map[string]string{":status": "500"},
		}
		res, err := p.ProcessResponseBody(t.Context(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, "error-body", string(commonRes.BodyMutation.GetBody()))
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, []byte("bar"), commonRes.HeaderMutation.SetHeaders[0].Header.RawValue)
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, []byte("bar"), commonRes.HeaderMutation.SetHeaders[0].Header.RawValue)
		mm.RequireRequestFailure(t)
	})

	// Verify streaming only records completion on EndOfStream.
	t.Run("streaming completion only at end", func(t *testing.T) {
		mm := &mockMetrics{}
		mt := &mockTranslator{t: t}
		p := &chatCompletionProcessorUpstreamFilter{
			translator:      mt,
			metrics:         mm,
			stream:          true,
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
		}
		// First chunk (not end of stream) should not complete the request.
		chunk := &extprocv3.HttpBody{Body: []byte("chunk-1"), EndOfStream: false}
		mt.expResponseBody = chunk
		mt.retUsedToken = metrics.TokenUsage{} // no usage yet in early chunks.
		_, err := p.ProcessResponseBody(t.Context(), chunk)
		require.NoError(t, err)
		mm.RequireRequestNotCompleted(t)
		require.Zero(t, mm.inputTokenCount)
		require.Zero(t, mm.streamingOutputTokens) // first chunk has 0 output tokens

		// Final chunk should mark success and record usage once.
		final := &extprocv3.HttpBody{Body: []byte("chunk-final"), EndOfStream: true}
		mt.expResponseBody = final
		mt.retUsedToken.SetInputTokens(5)
		mt.retUsedToken.SetCachedInputTokens(3)
		mt.retUsedToken.SetOutputTokens(138)
		mt.retUsedToken.SetTotalTokens(143)
		_, err = p.ProcessResponseBody(t.Context(), final)
		require.NoError(t, err)
		mm.RequireRequestSuccess(t)
		require.Equal(t, 5, mm.inputTokenCount)
		require.Equal(t, 138, mm.outputTokenCount)
		require.Equal(t, 138, mm.streamingOutputTokens) // accumulated output tokens from stream
	})
}

func bodyFromModel(t *testing.T, model string, stream bool, streamOptions *openai.StreamOptions) []byte {
	openAIReq := &openai.ChatCompletionRequest{}
	openAIReq.Model = model
	openAIReq.Stream = stream
	openAIReq.StreamOptions = streamOptions
	bytes, err := json.Marshal(openAIReq)
	require.NoError(t, err)
	return bytes
}

func Test_chatCompletionProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/foo"}
	mm := &mockMetrics{}
	p := &chatCompletionProcessorUpstreamFilter{
		config: &filterapi.RuntimeConfig{
			RequestCosts: []filterapi.RuntimeRequestCost{
				{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage", CEL: "15"}},
			},
		},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        mm,
	}
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:              "some-backend",
		Schema:            filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
		ModelNameOverride: "ai_gateway_llm",
	}, nil, &chatCompletionProcessorRouterFilter{})
	require.ErrorContains(t, err, "unsupported API schema: backend={some-schema v10.0}")
	mm.RequireRequestFailure(t)
	require.Zero(t, mm.inputTokenCount)
	mm.RequireSelectedBackend(t, "some-backend")
	require.False(t, p.stream) // On error, stream should be false regardless of the input.
}

func Test_chatCompletionProcessorUpstreamFilter_SetBackend_Success(t *testing.T) {
	headers := map[string]string{":path": "/foo", internalapi.ModelNameHeaderKeyDefault: "some-model"}
	mm := &mockMetrics{}
	p := &chatCompletionProcessorUpstreamFilter{
		config:         &filterapi.RuntimeConfig{},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        mm,
	}
	rp := &chatCompletionProcessorRouterFilter{
		originalRequestBody: &openai.ChatCompletionRequest{Stream: true},
	}
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:              "openai",
		Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		ModelNameOverride: "ai_gateway_llm",
	}, nil, rp)
	require.NoError(t, err)
	mm.RequireSelectedBackend(t, "openai")
	require.Equal(t, "ai_gateway_llm", p.requestHeaders[internalapi.ModelNameHeaderKeyDefault])
	require.True(t, p.stream)
	require.NotNil(t, p.translator)
}

func Test_chatCompletionProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		stream, forcedIncludeUsage bool
	}{
		{name: "non-streaming", stream: false, forcedIncludeUsage: false},
		{name: "streaming", stream: true, forcedIncludeUsage: false},
		{name: "streaming with forced include usage", stream: true, forcedIncludeUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("translator error", func(t *testing.T) {
				headers := map[string]string{":path": "/foo", internalapi.ModelNameHeaderKeyDefault: "some-model"}
				someBody := bodyFromModel(t, "some-model", tc.stream, nil)
				var body openai.ChatCompletionRequest
				require.NoError(t, json.Unmarshal(someBody, &body))
				tr := mockTranslator{t: t, retErr: errors.New("test error"), expRequestBody: &body}
				mm := &mockMetrics{}
				p := &chatCompletionProcessorUpstreamFilter{
					config:                 &filterapi.RuntimeConfig{},
					requestHeaders:         headers,
					logger:                 slog.Default(),
					metrics:                mm,
					translator:             tr,
					originalRequestBodyRaw: someBody,
					originalRequestBody:    &body,
					stream:                 tc.stream,
				}
				_, err := p.ProcessRequestHeaders(t.Context(), nil)
				require.ErrorContains(t, err, "failed to transform request: test error")
				mm.RequireRequestFailure(t)
				require.Zero(t, mm.inputTokenCount)
				// Verify models were set even though processing failed
				require.Equal(t, "some-model", mm.originalModel)
				require.Equal(t, "some-model", mm.requestModel)
			})
			t.Run("ok", func(t *testing.T) {
				someBody := bodyFromModel(t, "some-model", tc.stream, nil)
				headers := map[string]string{":path": "/foo", internalapi.ModelNameHeaderKeyDefault: "some-model"}
				headerMut := []internalapi.Header{{"a", "b"}}
				bodyMut := []byte("some body")

				var expBody openai.ChatCompletionRequest
				require.NoError(t, json.Unmarshal(someBody, &expBody))
				if tc.stream && tc.forcedIncludeUsage {
					expBody.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
				}

				mt := mockTranslator{
					t: t, expRequestBody: &expBody, retHeaderMutation: headerMut,
					retBodyMutation: bodyMut, expForceRequestBodyMutation: tc.forcedIncludeUsage,
				}
				mm := &mockMetrics{}
				p := &chatCompletionProcessorUpstreamFilter{
					config:                         &filterapi.RuntimeConfig{},
					requestHeaders:                 headers,
					logger:                         slog.Default(),
					metrics:                        mm,
					translator:                     mt,
					originalRequestBodyRaw:         someBody,
					originalRequestBody:            &expBody,
					stream:                         tc.stream,
					forcedStreamOptionIncludeUsage: tc.forcedIncludeUsage,
					handler:                        &mockBackendAuthHandler{},
				}
				resp, err := p.ProcessRequestHeaders(t.Context(), nil)
				require.NoError(t, err)
				require.Equal(t, mt, p.translator)
				require.NotNil(t, resp)
				commonRes := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders).RequestHeaders.Response
				require.Equal(t, string(bodyMut), string(commonRes.BodyMutation.GetBody()))
				require.Len(t, commonRes.HeaderMutation.SetHeaders, 2)
				require.Equal(t, "a", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
				require.Equal(t, []byte("b"), commonRes.HeaderMutation.SetHeaders[0].Header.RawValue)
				require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[1].Header.Key)
				require.Equal(t, "mock-auth-handler", string(commonRes.HeaderMutation.SetHeaders[1].Header.RawValue))

				mm.RequireRequestNotCompleted(t)
				// Verify models were set
				require.Equal(t, "some-model", mm.originalModel)
				require.Equal(t, "some-model", mm.requestModel)
				// Response model not set yet - only set when we get actual response
				require.Empty(t, mm.responseModel)
				require.Equal(t, tc.stream, p.stream)
			})
		})
	}
}

func TestChatCompletion_ParseBody(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		original := openai.ChatCompletionRequest{Model: "llama3.3"}
		bytes, err := json.Marshal(original)
		require.NoError(t, err)

		modelName, rb, err := parseOpenAIChatCompletionBody(&extprocv3.HttpBody{Body: bytes})
		require.NoError(t, err)
		require.Equal(t, "llama3.3", modelName)
		require.NotNil(t, rb)
	})
	t.Run("error", func(t *testing.T) {
		modelName, rb, err := parseOpenAIChatCompletionBody(&extprocv3.HttpBody{})
		require.Error(t, err)
		require.Empty(t, modelName)
		require.Nil(t, rb)
	})
}

func Test_chatCompletionProcessorUpstreamFilter_MergeWithTokenLatencyMetadata(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		mm := &mockMetrics{}
		mt := &mockTranslator{}
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			stream:     true,
			config:     &filterapi.RuntimeConfig{},
		}
		metadata := &structpb.Struct{Fields: map[string]*structpb.Value{}}
		p.mergeWithTokenLatencyMetadata(metadata)

		val, ok := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace]
		require.True(t, ok)

		inner := val.GetStructValue()
		require.NotNil(t, inner)
		require.Equal(t, 1000.0, inner.Fields["token_latency_ttft"].GetNumberValue())
		require.Equal(t, 500.0, inner.Fields["token_latency_itl"].GetNumberValue())
	})
	t.Run("existing metadata", func(t *testing.T) {
		mm := &mockMetrics{}
		mt := &mockTranslator{}
		p := &chatCompletionProcessorUpstreamFilter{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			stream:     true,
			config:     &filterapi.RuntimeConfig{},
		}
		existingInner := &structpb.Struct{Fields: map[string]*structpb.Value{
			"tokenCost":        {Kind: &structpb.Value_NumberValue{NumberValue: float64(200)}},
			"inputTokenUsage":  {Kind: &structpb.Value_NumberValue{NumberValue: float64(300)}},
			"outputTokenUsage": {Kind: &structpb.Value_NumberValue{NumberValue: float64(400)}},
		}}
		metadata := &structpb.Struct{Fields: map[string]*structpb.Value{
			internalapi.AIGatewayFilterMetadataNamespace: structpb.NewStructValue(existingInner),
		}}
		p.mergeWithTokenLatencyMetadata(metadata)

		val, ok := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace]
		require.True(t, ok)
		inner := val.GetStructValue()
		require.NotNil(t, inner)
		require.Equal(t, 1000.0, inner.Fields["token_latency_ttft"].GetNumberValue())
		require.Equal(t, 500.0, inner.Fields["token_latency_itl"].GetNumberValue())
		require.Equal(t, 200.0, inner.Fields["tokenCost"].GetNumberValue())
		require.Equal(t, 300.0, inner.Fields["inputTokenUsage"].GetNumberValue())
		require.Equal(t, 400.0, inner.Fields["outputTokenUsage"].GetNumberValue())
	})
}

func TestChatCompletionsProcessorRouterFilter_ProcessResponseHeaders_ProcessResponseBody(t *testing.T) {
	t.Run("no ok path with passthrough", func(t *testing.T) {
		p := &chatCompletionProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.ChatCompletionRequest{Stream: true},
		}
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.NoError(t, err)
		_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true})
		require.NoError(t, err)
	})
	t.Run("ok path with upstream filter", func(t *testing.T) {
		p := &chatCompletionProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.ChatCompletionRequest{Stream: true},
			upstreamFilter: &chatCompletionProcessorUpstreamFilter{
				translator: &mockTranslator{t: t, expHeaders: map[string]string{}},
				logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				metrics:    &mockMetrics{},
				config:     &filterapi.RuntimeConfig{},
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

func TestChatCompletionProcessorRouterFilter_ProcessResponseBody_SpanHandling(t *testing.T) {
	t.Run("passthrough without span", func(t *testing.T) {
		p := &chatCompletionProcessorRouterFilter{
			span:                nil,
			originalRequestBody: &openai.ChatCompletionRequest{Stream: false},
		}
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true, Body: []byte("response")})
		require.NoError(t, err)
		// Should not panic when span is nil.
	})

	t.Run("upstream filter span", func(t *testing.T) {
		span := &testotel.MockSpan{}
		mt := &mockTranslator{t: t}
		p := &chatCompletionProcessorRouterFilter{
			originalRequestBody: &openai.ChatCompletionRequest{Stream: true},
			upstreamFilter: &chatCompletionProcessorUpstreamFilter{
				responseHeaders: map[string]string{":status": "200"},
				translator:      mt,
				logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				metrics:         &mockMetrics{},
				config:          &filterapi.RuntimeConfig{},
				span:            span,
			},
		}

		finalBody := &extprocv3.HttpBody{EndOfStream: true, Body: []byte("final")}
		mt.expResponseBody = finalBody
		_, err := p.ProcessResponseBody(t.Context(), finalBody)
		require.NoError(t, err)
		require.True(t, span.EndSpanCalled)
	})

	t.Run("upstream filter error with span", func(t *testing.T) {
		span := &testotel.MockSpan{}
		p := &chatCompletionProcessorRouterFilter{
			originalRequestBody: &openai.ChatCompletionRequest{Stream: false},
			upstreamFilter: &chatCompletionProcessorUpstreamFilter{
				responseHeaders: map[string]string{":status": "500"},
				translator:      &mockTranslator{t: t},
				logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
				metrics:         &mockMetrics{},
				config:          &filterapi.RuntimeConfig{},
				span:            span,
			},
		}

		errorBody := "error response"
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{EndOfStream: true, Body: []byte(errorBody)})
		require.NoError(t, err)
		require.Equal(t, 500, span.ErrorStatus)
		require.Equal(t, errorBody, span.ErrBody)
	})
}

func Test_chatCompletionProcessorUpstreamFilter_SensitiveHeaders_RemoveAndRestore(t *testing.T) {
	headerMutation := filterapi.HTTPHeaderMutation{
		Remove: []string{"authorization", "x-api-key"},
		Set:    []filterapi.HTTPHeader{{Name: "x-new-header", Value: "new-value"}},
	}
	originalHeaders := map[string]string{
		"authorization": "secret",
		"x-api-key":     "key123",
		"other":         "value",
	}
	body := openai.ChatCompletionRequest{Model: "test-model"}
	raw := []byte(`{"model":"test-model"}`)

	t.Run("remove headers", func(t *testing.T) {
		p := &chatCompletionProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"authorization": "secret", "x-api-key": "key123", "other": "value"},
			headerMutator:          headermutator.NewHeaderMutator(&headerMutation, originalHeaders),
			onRetry:                true,
			metrics:                &mockMetrics{},
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockTranslator{t: t, expForceRequestBodyMutation: true, expRequestBody: &body},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
		}

		resp, err := p.ProcessRequestHeaders(context.Background(), nil)
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
		p := &chatCompletionProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"other": "value"},
			headerMutator:          headermutator.NewHeaderMutator(&filterapi.HTTPHeaderMutation{Set: headerMutation.Set}, originalHeaders),
			onRetry:                true, // not a retry, so should restore.
			metrics:                &mockMetrics{},
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockTranslator{t: t, expForceRequestBodyMutation: true, expRequestBody: &body},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
		}

		// Call the actual method to trigger restoration logic.
		_, err := p.ProcessRequestHeaders(context.Background(), nil)
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
		p := &chatCompletionProcessorUpstreamFilter{
			requestHeaders:         map[string]string{"other": "value"},
			onRetry:                true, // not a retry, so should restore.
			headerMutator:          headermutator.NewHeaderMutator(nil, originalHeaders),
			metrics:                &mockMetrics{},
			logger:                 slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config:                 &filterapi.RuntimeConfig{},
			translator:             &mockTranslator{t: t, expForceRequestBodyMutation: true, expRequestBody: &body},
			originalRequestBody:    &body,
			originalRequestBodyRaw: raw,
		}

		// Call the actual method to trigger restoration logic.
		_, err := p.ProcessRequestHeaders(context.Background(), nil)
		require.NoError(t, err)

		// Now check that sensitive headers are restored.
		require.Equal(t, "secret", p.requestHeaders["authorization"])
		require.Equal(t, "key123", p.requestHeaders["x-api-key"])
		require.Equal(t, "value", p.requestHeaders["other"])
	})
}

func Test_ProcessRequestHeaders_SetsRequestModel(t *testing.T) {
	headers := map[string]string{":path": "/v1/chat/completions", internalapi.ModelNameHeaderKeyDefault: "header-model"}
	body := openai.ChatCompletionRequest{Model: "body-model"}
	raw, _ := json.Marshal(body)
	mm := &mockMetrics{}
	p := &chatCompletionProcessorUpstreamFilter{
		config:                 &filterapi.RuntimeConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             &mockTranslator{t: t, expRequestBody: &body},
		originalRequestBodyRaw: raw,
		originalRequestBody:    &body,
	}
	_, _ = p.ProcessRequestHeaders(t.Context(), nil)
	// Should use the override model from the header, as that's what is sent upstream.
	require.Equal(t, "body-model", mm.originalModel)
	require.Equal(t, "header-model", mm.requestModel)
	// Response model is not set until we get actual response
	require.Empty(t, mm.responseModel)
}

// Test_ProcessResponseBody_UsesActualResponseModel verifies that
// the actual response model from the API response is used for metrics, not the request model.
// This is important because OpenAI may return a more specific model version than what was
// requested (e.g., "gpt-5-nano-2025-08-07" instead of "gpt-5-nano"), as described in the
// model virtualization documentation.
func Test_ProcessResponseBody_UsesActualResponseModel(t *testing.T) {
	headers := map[string]string{":path": "/v1/chat/completions"}
	body := openai.ChatCompletionRequest{Model: "gpt-5-nano"}
	raw, _ := json.Marshal(body)
	mm := &mockMetrics{}

	// Create a mock translator that returns token usage with response model
	// Simulating OpenAI's automatic routing where gpt-5-nano routes to gpt-5-nano-2025-08-07
	mt := &mockTranslator{
		t:                t,
		expRequestBody:   &body,
		expHeaders:       map[string]string{":status": "200"},
		retResponseModel: "gpt-5-nano-2025-08-07",
	}
	mt.retUsedToken.SetInputTokens(10)
	mt.retUsedToken.SetOutputTokens(20)

	p := &chatCompletionProcessorUpstreamFilter{
		config:                 &filterapi.RuntimeConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             mt,
		originalRequestBodyRaw: raw,
		originalRequestBody:    &body,
	}

	// First process request headers
	_, _ = p.ProcessRequestHeaders(t.Context(), nil)

	// Process response headers (required before body)
	responseHeaders := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: ":status", Value: "200"},
		},
	}
	_, err := p.ProcessResponseHeaders(t.Context(), responseHeaders)
	require.NoError(t, err)

	// Now process response body (should override with actual response model)
	// Simple response JSON that the translator will parse
	responseBytes := []byte(`{"model":"gpt-5-nano-2025-08-07","choices":[{"message":{"content":"test"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`)
	_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{
		Body:        responseBytes,
		EndOfStream: true,
	})
	require.NoError(t, err)

	// Verify that response model was set from the actual API response
	// Request was for gpt-5-nano but OpenAI returned the versioned gpt-5-nano-2025-08-07
	// In this test, original and request are the same (no override)
	mm.RequireSelectedModel(t, "gpt-5-nano", "gpt-5-nano", "gpt-5-nano-2025-08-07")
	require.Equal(t, 10, mm.inputTokenCount)
	require.Equal(t, 20, mm.outputTokenCount)
	mm.RequireRequestSuccess(t)
}

func TestChatCompletionProcessorUpstreamFilter_ProcessRequestHeaders_WithBodyMutations(t *testing.T) {
	t.Run("body mutations applied correctly", func(t *testing.T) {
		headers := map[string]string{
			":path":         "/v1/chat/completions",
			"x-ai-eg-model": "gpt-4",
		}

		requestBody := &openai.ChatCompletionRequest{
			Model: "gpt-4",
		}
		requestBodyRaw := []byte(`{"model": "gpt-4", "messages": [{"role": "user", "content": "Hello"}], "service_tier": "default", "max_tokens": 1000}`)

		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"internal_flag"},
			Set: []filterapi.HTTPBodyField{
				{Path: "service_tier", Value: "\"scale\""},
				{Path: "max_tokens", Value: "2000"},
				{Path: "temperature", Value: "0.8"},
			},
		}

		translator := mockTranslator{
			t:                           t,
			expRequestBody:              requestBody,
			expForceRequestBodyMutation: false,
			retBodyMutation:             requestBodyRaw,
			retErr:                      nil,
		}

		chatMetrics := &mockMetrics{}
		p := &chatCompletionProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                chatMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			translator:             &translator,
			handler:                &mockBackendAuthHandler{},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			BodyMutation: bodyMutations,
		}

		rp := &chatCompletionProcessorRouterFilter{
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

		testBodyMutation := []byte(`{"model": "gpt-4", "messages": [{"role": "user", "content": "Hello"}], "service_tier": "default", "internal_flag": true, "max_tokens": 1000}`)
		mutatedBody, err := p.bodyMutator.Mutate(testBodyMutation, false)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, "scale", result["service_tier"])
		require.Equal(t, float64(2000), result["max_tokens"])
		require.Equal(t, 0.8, result["temperature"])
		require.NotContains(t, result, "internal_flag")
		require.Equal(t, "gpt-4", result["model"])
	})

	t.Run("body mutator with retry", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/chat/completions"}
		chatMetrics := &mockMetrics{}

		originalRequestBodyRaw := []byte(`{"model": "gpt-4", "service_tier": "default", "messages": [{"role": "user", "content": "Hello"}]}`)
		requestBody := &openai.ChatCompletionRequest{
			Model: "gpt-4",
		}

		p := &chatCompletionProcessorUpstreamFilter{
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                chatMetrics,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
		}

		bodyMutations := &filterapi.HTTPBodyMutation{
			Set: []filterapi.HTTPBodyField{
				{Path: "service_tier", Value: "\"premium\""},
				{Path: "temperature", Value: "0.7"},
			},
		}

		backend := &filterapi.Backend{
			Name:         "test-backend",
			Schema:       filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			BodyMutation: bodyMutations,
		}

		rp := &chatCompletionProcessorRouterFilter{
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: originalRequestBodyRaw,
			requestHeaders:         headers,
			upstreamFilterCount:    2,
		}

		err := p.SetBackend(context.Background(), backend, &mockBackendAuthHandler{}, rp)
		require.NoError(t, err)

		require.NotNil(t, p.bodyMutator)
		require.True(t, p.onRetry)

		modifiedBody := []byte(`{"model": "gpt-4", "service_tier": "modified", "extra": "field", "messages": [{"role": "user", "content": "Modified"}]}`)
		mutatedBody, err := p.bodyMutator.Mutate(modifiedBody, true)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutatedBody, &result)
		require.NoError(t, err)

		require.Equal(t, "premium", result["service_tier"])
		require.Equal(t, 0.7, result["temperature"])
		require.Equal(t, "gpt-4", result["model"])
		require.NotContains(t, result, "extra")

		messages, ok := result["messages"].([]interface{})
		require.True(t, ok)
		require.Len(t, messages, 1)
		firstMessage, ok := messages[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "Hello", firstMessage["content"])
	})
}

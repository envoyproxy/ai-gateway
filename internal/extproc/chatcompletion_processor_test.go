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
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/dynlb"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

func TestChatCompletion_Schema(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: "Foo", Version: "v123"}}
		_, err := ChatCompletionProcessorFactory(nil)(cfg, nil, nil)
		require.ErrorContains(t, err, "unsupported API schema: Foo")
	})
	t.Run("supported openai", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		_, err := ChatCompletionProcessorFactory(nil)(cfg, nil, nil)
		require.NoError(t, err)
	})
}

func TestChatCompletion_SelectTranslator(t *testing.T) {
	c := &chatCompletionProcessor{}
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

func TestChatCompletion_ProcessRequestHeaders(t *testing.T) {
	mm := &mockChatCompletionMetrics{}
	p := &chatCompletionProcessor{
		metrics: mm,
	}
	res, err := p.ProcessRequestHeaders(t.Context(), &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}},
	})
	require.NoError(t, err)
	_, ok := res.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	require.True(t, ok)
	require.NotZero(t, mm.requestStart)
	mm.RequireRequestNotCompleted(t)
}

func TestChatCompletion_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{t: t, expHeaders: make(map[string]string)}
		p := &chatCompletionProcessor{
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
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessor{
			translator: mt,
			metrics:    mm,
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		mm.RequireRequestNotCompleted(t)
		require.Nil(t, res.ModeOverride)
	})
	t.Run("ok/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "200"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "200", "dog": "cat"}
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessor{translator: mt, metrics: mm, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		require.Equal(t, &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}, res.ModeOverride)
	})
	t.Run("error/streaming", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: ":status", Value: "500"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{":status": "500", "dog": "cat"}
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessor{translator: mt, metrics: mm, stream: true}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
		require.Nil(t, res.ModeOverride)
	})
}

func TestChatCompletion_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{t: t}
		p := &chatCompletionProcessor{
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
		mm := &mockChatCompletionMetrics{}
		mt := &mockTranslator{
			t: t, expResponseBody: inBody,
			retBodyMutation: expBodyMut, retHeaderMutation: expHeadMut,
			retUsedToken: translator.LLMTokenUsage{OutputTokens: 123, InputTokens: 1},
		}

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)
		p := &chatCompletionProcessor{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			metrics:    mm,
			stream:     true,
			config: &processorConfig{
				metadataNamespace: "ai_gateway_llm_ns",
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
			GetStructValue().Fields["output_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
	})
}

func TestChatCompletion_ProcessRequestBody(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream%v", stream), func(t *testing.T) {
			bodyFromModel := func(t *testing.T, model string) []byte {
				var openAIReq openai.ChatCompletionRequest
				openAIReq.Model = model
				openAIReq.Stream = stream
				bytes, err := json.Marshal(openAIReq)
				require.NoError(t, err)
				return bytes
			}
			t.Run("body parser error", func(t *testing.T) {
				mm := &mockChatCompletionMetrics{}
				p := &chatCompletionProcessor{
					metrics: mm,
				}
				_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
				require.ErrorContains(t, err, "invalid character 'o' in literal null")
				mm.RequireRequestFailure(t)
				mm.RequireTokensRecorded(t, 0)
				mm.RequireSelected(t, "", "")
				require.False(t, p.stream) // On error, stream should be false regardless of the input.
			})
			t.Run("router error", func(t *testing.T) {
				headers := map[string]string{":path": "/foo"}
				rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("test error")}
				mm := &mockChatCompletionMetrics{}
				p := &chatCompletionProcessor{
					config:         &processorConfig{router: rt},
					requestHeaders: headers,
					logger:         slog.Default(),
					metrics:        mm,
				}
				_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
				require.ErrorContains(t, err, "failed to calculate route: test error")
				mm.RequireRequestFailure(t)
				mm.RequireTokensRecorded(t, 0)
				mm.RequireSelected(t, "some-model", "")
				require.False(t, p.stream) // On error, stream should be false regardless of the input.
			})
			t.Run("router error 404", func(t *testing.T) {
				headers := map[string]string{":path": "/foo"}
				rt := mockRouter{t: t, expHeaders: headers, retErr: x.ErrNoMatchingRule}
				mm := &mockChatCompletionMetrics{}
				p := &chatCompletionProcessor{
					config:         &processorConfig{router: rt},
					requestHeaders: headers,
					logger:         slog.Default(),
					metrics:        mm,
				}
				resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
				require.NoError(t, err)
				require.NotNil(t, resp)
				ir := resp.GetImmediateResponse()
				require.NotNil(t, ir)
				require.Equal(t, typev3.StatusCode_NotFound, ir.GetStatus().GetCode())
				require.Equal(t, x.ErrNoMatchingRule.Error(), string(ir.GetBody()))
				mm.RequireRequestFailure(t)
				mm.RequireTokensRecorded(t, 0)
				mm.RequireSelected(t, "some-model", "")
				require.False(t, p.stream) // On error, stream should be false regardless of the input.
			})
			t.Run("translator not found", func(t *testing.T) {
				headers := map[string]string{":path": "/foo"}
				rt := mockRouter{
					t: t, expHeaders: headers, retBackendName: "some-backend",
					retVersionedAPISchema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
				}
				mm := &mockChatCompletionMetrics{}
				p := &chatCompletionProcessor{
					config:         &processorConfig{router: rt},
					requestHeaders: headers,
					logger:         slog.Default(),
					metrics:        mm,
				}
				_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
				require.ErrorContains(t, err, "unsupported API schema: backend={some-schema v10.0}")
				mm.RequireRequestFailure(t)
				mm.RequireTokensRecorded(t, 0)
				mm.RequireSelected(t, "some-model", "some-backend")
				require.False(t, p.stream) // On error, stream should be false regardless of the input.
			})
			t.Run("translator error", func(t *testing.T) {
				headers := map[string]string{":path": "/foo"}
				someBody := bodyFromModel(t, "some-model")
				rt := mockRouter{
					t: t, expHeaders: headers, retBackendName: "some-backend",
					retVersionedAPISchema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
				}
				var body openai.ChatCompletionRequest
				require.NoError(t, json.Unmarshal(someBody, &body))
				tr := mockTranslator{t: t, retErr: errors.New("test error"), expRequestBody: &body}
				mm := &mockChatCompletionMetrics{}
				p := &chatCompletionProcessor{
					config:         &processorConfig{router: rt},
					requestHeaders: headers,
					logger:         slog.Default(),
					metrics:        mm,
					translator:     tr,
				}
				_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
				require.ErrorContains(t, err, "failed to transform request: test error")
				mm.RequireRequestFailure(t)
				mm.RequireTokensRecorded(t, 0)
				mm.RequireSelected(t, "some-model", "some-backend")
				require.False(t, p.stream) // On error, stream should be false regardless of the input.
			})
			t.Run("ok", func(t *testing.T) {
				for _, tc := range []struct {
					name  string
					dynlb *mockDynamicLB
				}{
					{name: "no-dynlb"},
					{name: "dynlb", dynlb: &mockDynamicLB{
						backedName: "some-backend",
						headers:    []*corev3.HeaderValueOption{{Header: &corev3.HeaderValue{Key: "foo", Value: "bar"}}},
					}},
				} {
					t.Run(tc.name, func(t *testing.T) {
						someBody := bodyFromModel(t, "some-model")
						headers := map[string]string{":path": "/foo"}
						dynLb := &filterapi.DynamicLoadBalancing{}
						rt := mockRouter{
							t: t, expHeaders: headers, retBackendName: "some-backend",
							retVersionedAPISchema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
						}
						if tc.dynlb != nil {
							rt.retBackendDynamicLB = dynLb
						}
						headerMut := &extprocv3.HeaderMutation{}
						bodyMut := &extprocv3.BodyMutation{}

						var expBody openai.ChatCompletionRequest
						require.NoError(t, json.Unmarshal(someBody, &expBody))
						mt := mockTranslator{t: t, expRequestBody: &expBody, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
						mm := &mockChatCompletionMetrics{}
						p := &chatCompletionProcessor{
							config: &processorConfig{
								router:                   rt,
								selectedBackendHeaderKey: "x-ai-gateway-backend-key",
								modelNameHeaderKey:       "x-ai-gateway-model-key",
								dynamicLoadBalancers: map[*filterapi.DynamicLoadBalancing]dynlb.DynamicLoadBalancer{
									dynLb: tc.dynlb,
								},
							},
							requestHeaders: headers,
							logger:         slog.Default(),
							metrics:        mm,
							translator:     mt,
						}
						resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
						require.NoError(t, err)
						require.Equal(t, mt, p.translator)
						require.NotNil(t, resp)
						commonRes := resp.Response.(*extprocv3.ProcessingResponse_RequestBody).RequestBody.Response
						require.Equal(t, headerMut, commonRes.HeaderMutation)
						require.Equal(t, bodyMut, commonRes.BodyMutation)

						mm.RequireRequestNotCompleted(t)
						mm.RequireSelected(t, "some-model", "some-backend")

						// Check the model and backend headers are set in headerMut.
						hdrs := headerMut.SetHeaders
						if tc.dynlb != nil {
							require.Len(t, hdrs, 3)
							require.Equal(t, "x-ai-gateway-model-key", hdrs[0].Header.Key)
							require.Equal(t, "some-model", string(hdrs[0].Header.RawValue))
							require.Equal(t, "x-ai-gateway-backend-key", hdrs[1].Header.Key)
							require.Equal(t, "original_destination_cluster", string(hdrs[1].Header.RawValue))
							require.Equal(t, "foo", hdrs[2].Header.Key)
							require.Equal(t, "bar", hdrs[2].Header.Value)
						} else {
							require.Len(t, hdrs, 2)
							require.Equal(t, "x-ai-gateway-model-key", hdrs[0].Header.Key)
							require.Equal(t, "some-model", string(hdrs[0].Header.RawValue))
							require.Equal(t, "x-ai-gateway-backend-key", hdrs[1].Header.Key)
							require.Equal(t, "some-backend", string(hdrs[1].Header.RawValue))
						}
						require.Equal(t, stream, p.stream)
					})
				}
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

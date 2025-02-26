// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"errors"
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
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestChatCompletion_Scheema(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: "Foo", Version: "v123"}}
		_, err := NewChatCompletionProcessor(cfg, nil, nil)
		require.ErrorContains(t, err, "unsupported API schema: Foo")
	})
	t.Run("supported openai", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		_, err := NewChatCompletionProcessor(cfg, nil, nil)
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
}

func TestChatCompletion_ProcessRequestHeaders(t *testing.T) {
	p := &chatCompletionProcessor{
		metrics: metrics.NewProcessorMetrics(),
	}
	res, err := p.ProcessRequestHeaders(t.Context(), &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}},
	})
	require.NoError(t, err)
	_, ok := res.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	require.True(t, ok)
}

// TestMetricsAreRecorded tests that metrics are properly recorded
func TestMetricsAreRecorded(t *testing.T) {
	// Test verifies that the processor correctly calls the metrics methods.
	// Create a custom mock translator that doesn't validate the body.
	customMockTranslator := &struct {
		translator.OpenAIChatCompletionTranslator
	}{
		OpenAIChatCompletionTranslator: &mockTranslator{
			t:                 t,
			retBodyMutation:   &extprocv3.BodyMutation{},
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retUsedToken:      translator.LLMTokenUsage{OutputTokens: 100, InputTokens: 50, TotalTokens: 150},
		},
	}

	// Override the ResponseBody method to not validate the body
	customMockTranslator.OpenAIChatCompletionTranslator = &mockTranslator{
		t:                 t,
		retBodyMutation:   &extprocv3.BodyMutation{},
		retHeaderMutation: &extprocv3.HeaderMutation{},
		retUsedToken:      translator.LLMTokenUsage{OutputTokens: 100, InputTokens: 50, TotalTokens: 150},
	}

	// Create a processor with our mock translator
	p := &chatCompletionProcessor{
		translator:  customMockTranslator.OpenAIChatCompletionTranslator,
		metrics:     metrics.NewProcessorMetrics(),
		backendName: "test-backend",
		modelName:   "test-model",
		logger:      slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
		config: &processorConfig{
			metadataNamespace: "test-namespace",
		},
	}

	// Process a response body - this should call metrics methods
	_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("test-body"), EndOfStream: true})
	require.NoError(t, err)

	// Verify token metrics were recorded correctly
	metricsRegistry := metrics.GetRegistry()
	metricFamilies, err := metricsRegistry.Gather()
	require.NoError(t, err)

	// Find and verify token metrics
	var foundCompletionTokens, foundPromptTokens, foundTotalTokens bool
	for _, metricFamily := range metricFamilies {
		if metricFamily.GetName() == "aigateway_model_tokens_total" {
			for _, metric := range metricFamily.GetMetric() {
				labels := make(map[string]string)
				for _, label := range metric.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}

				// Check if this is one of our metrics
				if labels["backend"] == "test-backend" && labels["model"] == "test-model" {
					switch labels["type"] {
					case "completion":
						require.Equal(t, float64(100), metric.GetCounter().GetValue(), "Completion tokens should be 100")
						foundCompletionTokens = true
					case "prompt":
						require.Equal(t, float64(50), metric.GetCounter().GetValue(), "Prompt tokens should be 50")
						foundPromptTokens = true
					case "total":
						require.Equal(t, float64(150), metric.GetCounter().GetValue(), "Total tokens should be 150")
						foundTotalTokens = true
					}
				}
			}
		}
	}

	// Ensure we found all the metrics we expected
	require.True(t, foundCompletionTokens, "Completion tokens metric not found")
	require.True(t, foundPromptTokens, "Prompt tokens metric not found")
	require.True(t, foundTotalTokens, "Total tokens metric not found")

	// Test error case
	customMockTranslator.OpenAIChatCompletionTranslator = &mockTranslator{
		t:      t,
		retErr: errors.New("test error"),
	}
	p.translator = customMockTranslator.OpenAIChatCompletionTranslator
	_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "test error")

}

func TestChatCompletion_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t, expHeaders: make(map[string]string)}
		p := &chatCompletionProcessor{
			translator: mt,
			metrics:    metrics.NewProcessorMetrics(),
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseHeaders(t.Context(), nil)
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &chatCompletionProcessor{
			translator: mt,
			metrics:    metrics.NewProcessorMetrics(),
		}
		res, err := p.ProcessResponseHeaders(t.Context(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
	})
}

func TestChatCompletion_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t}
		p := &chatCompletionProcessor{
			translator: mt,
			metrics:    metrics.NewProcessorMetrics(),
		}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("some-body"), EndOfStream: true}
		expBodyMut := &extprocv3.BodyMutation{}
		expHeadMut := &extprocv3.HeaderMutation{}
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
			metrics:    metrics.NewProcessorMetrics(),
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
	bodyFromModel := func(t *testing.T, model string) []byte {
		var openAIReq openai.ChatCompletionRequest
		openAIReq.Model = model
		bytes, err := json.Marshal(openAIReq)
		require.NoError(t, err)
		return bytes
	}
	t.Run("body parser error", func(t *testing.T) {
		p := &chatCompletionProcessor{
			metrics: metrics.NewProcessorMetrics(),
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})
	t.Run("router error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("test error")}
		p := &chatCompletionProcessor{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.NewProcessorMetrics(),
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
		require.ErrorContains(t, err, "failed to calculate route: test error")
	})
	t.Run("router error 404", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: x.ErrNoMatchingRule}
		p := &chatCompletionProcessor{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.NewProcessorMetrics(),
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		ir := resp.GetImmediateResponse()
		require.NotNil(t, ir)
		require.Equal(t, typev3.StatusCode_NotFound, ir.GetStatus().GetCode())
		require.Equal(t, x.ErrNoMatchingRule.Error(), string(ir.GetBody()))
	})
	t.Run("translator not found", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
		}
		p := &chatCompletionProcessor{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.NewProcessorMetrics(),
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: bodyFromModel(t, "some-model")})
		require.ErrorContains(t, err, "unsupported API schema: backend={some-schema v10.0}")
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
		p := &chatCompletionProcessor{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.NewProcessorMetrics(),
			translator:     tr,
		}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
		require.ErrorContains(t, err, "failed to transform request: test error")
	})
	t.Run("ok", func(t *testing.T) {
		someBody := bodyFromModel(t, "some-model")
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{Name: "some-schema", Version: "v10.0"},
		}
		headerMut := &extprocv3.HeaderMutation{}
		bodyMut := &extprocv3.BodyMutation{}

		var expBody openai.ChatCompletionRequest
		require.NoError(t, json.Unmarshal(someBody, &expBody))
		mt := mockTranslator{t: t, expRequestBody: &expBody, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		p := &chatCompletionProcessor{
			config: &processorConfig{
				router:                   rt,
				selectedBackendHeaderKey: "x-ai-gateway-backend-key",
				modelNameHeaderKey:       "x-ai-gateway-model-key",
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.NewProcessorMetrics(),
			translator:     mt,
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
		require.NoError(t, err)
		require.Equal(t, mt, p.translator)
		require.NotNil(t, resp)
		commonRes := resp.Response.(*extprocv3.ProcessingResponse_RequestBody).RequestBody.Response
		require.Equal(t, headerMut, commonRes.HeaderMutation)
		require.Equal(t, bodyMut, commonRes.BodyMutation)

		// Check the model and backend headers are set in headerMut.
		hdrs := headerMut.SetHeaders
		require.Len(t, hdrs, 2)
		require.Equal(t, "x-ai-gateway-model-key", hdrs[0].Header.Key)
		require.Equal(t, "some-model", string(hdrs[0].Header.RawValue))
		require.Equal(t, "x-ai-gateway-backend-key", hdrs[1].Header.Key)
		require.Equal(t, "some-backend", string(hdrs[1].Header.RawValue))
	})
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
		require.Equal(t, "", modelName)
		require.Nil(t, rb)
	})
}

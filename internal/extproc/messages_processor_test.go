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
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/types/known/structpb"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func TestMessagesProcessorFactory(t *testing.T) {
	m := metrics.NewMessagesFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})
	factory := MessagesProcessorFactory(m)
	require.NotNil(t, factory, "MessagesProcessorFactory should return a non-nil factory")

	// Test creating a router filter.
	config := &processorConfig{}
	headers := map[string]string{
		":path":         "/anthropic/v1/messages",
		"authorization": "Bearer token",
	}
	logger := slog.Default()

	routerProcessor, err := factory(config, headers, logger, tracing.NoopTracing{}, false)
	require.NoError(t, err, "Factory should create router processor without error")
	require.NotNil(t, routerProcessor, "Router processor should not be nil")
	require.IsType(t, &messagesProcessorRouterFilter{}, routerProcessor, "Should return router filter type")

	// Test creating an upstream filter.
	upstreamProcessor, err := factory(config, headers, logger, tracing.NoopTracing{}, true)
	require.NoError(t, err, "Factory should create upstream processor without error")
	require.NotNil(t, upstreamProcessor, "Upstream processor should not be nil")
	require.IsType(t, &messagesProcessorUpstreamFilter{}, upstreamProcessor, "Should return upstream filter type")
}

func TestParseAnthropicMessagesBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		expectError bool
		checkFields func(t *testing.T, req *anthropicschema.MessagesRequest)
	}{
		{
			name: "valid_anthropic_request",
			body: `{
				"model": "claude-3-sonnet",
				"max_tokens": 1000,
				"messages": [{"role": "user", "content": "Hello"}],
				"stream": false
			}`,
			expectError: false,
			checkFields: func(t *testing.T, req *anthropicschema.MessagesRequest) {
				require.Equal(t, "claude-3-sonnet", req.GetModel())
				require.Equal(t, 1000, req.GetMaxTokens())
				require.False(t, req.GetStream())
			},
		},
		{
			name: "streaming_request",
			body: `{
				"model": "claude-3-sonnet",
				"max_tokens": 1000,
				"messages": [{"role": "user", "content": "Hello"}],
				"stream": true
			}`,
			expectError: false,
			checkFields: func(t *testing.T, req *anthropicschema.MessagesRequest) {
				require.True(t, req.GetStream())
			},
		},
		{
			name:        "invalid_json",
			body:        `{"invalid": json}`,
			expectError: true,
		},
		{
			name:        "empty_body",
			body:        "",
			expectError: true,
		},
		{
			name: "request_with_tools",
			body: `{
				"model": "claude-3-sonnet",
				"max_tokens": 1000,
				"messages": [{"role": "user", "content": "Hello"}],
				"tools": [{"type": "function", "function": {"name": "test"}}]
			}`,
			expectError: false,
			checkFields: func(t *testing.T, req *anthropicschema.MessagesRequest) {
				tools, ok := (*req)["tools"]
				require.True(t, ok)
				require.NotNil(t, tools)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes := []byte(tt.body)
			body := &extprocv3.HttpBody{Body: bodyBytes}

			modelName, req, err := parseAnthropicMessagesBody(body)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, req)
			} else {
				require.NoError(t, err)
				require.NotNil(t, req)
				require.NotEmpty(t, modelName)
				if tt.checkFields != nil {
					tt.checkFields(t, req)
				}
			}
		})
	}
}

func TestMessagesProcessorRouterFilter_ProcessRequestHeaders(t *testing.T) {
	processor := &messagesProcessorRouterFilter{
		config: &processorConfig{},
		logger: slog.Default(),
	}

	ctx := context.Background()
	headers := &corev3.HeaderMap{}

	response, err := processor.ProcessRequestHeaders(ctx, headers)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, response.Response)
	require.NotNil(t, response.Response.(*extprocv3.ProcessingResponse_RequestHeaders))
}

func TestMessagesProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		expectError bool
		expectModel string
	}{
		{
			name: "valid anthropic request",
			body: `{
				"model": "claude-3-sonnet-20240229",
				"max_tokens": 1000,
				"messages": [{"role": "user", "content": "Hello"}]
			}`,
			expectError: false,
			expectModel: "claude-3-sonnet-20240229",
		},
		{
			name: "missing model field",
			body: `{
				"max_tokens": 1000,
				"messages": [{"role": "user", "content": "Hello"}]
			}`,
			expectError: true,
		},
		{
			name:        "invalid json",
			body:        `{invalid json`,
			expectError: true,
		},
		{
			name:        "empty body",
			body:        "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &messagesProcessorRouterFilter{
				config:         &processorConfig{},
				requestHeaders: make(map[string]string),
				logger:         slog.Default(),
			}

			ctx := context.Background()
			httpBody := &extprocv3.HttpBody{
				Body: []byte(tt.body),
			}

			response, err := processor.ProcessRequestBody(ctx, httpBody)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.NotNil(t, response)
				require.Equal(t, tt.expectModel, processor.requestHeaders["x-ai-eg-model"])
				require.NotNil(t, processor.originalRequestBody)
				require.NotEmpty(t, processor.originalRequestBodyRaw)
			}
		})
	}
}

func TestMessagesProcessorRouterFilter_UnimplementedMethods(t *testing.T) {
	processor := &messagesProcessorRouterFilter{
		config: &processorConfig{},
		logger: slog.Default(),
	}

	ctx := context.Background()

	// Test ProcessResponseHeaders.
	respHeaders, err := processor.ProcessResponseHeaders(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, respHeaders)

	// Test ProcessResponseBody.
	respBody, err := processor.ProcessResponseBody(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, respBody)

	// Test SetBackend.
	err = processor.SetBackend(ctx, nil, nil, nil)
	require.NoError(t, err)
}

func TestMessagesProcessorUpstreamFilter_ProcessRequestBody_ShouldPanic(t *testing.T) {
	processor := &messagesProcessorUpstreamFilter{
		config: &processorConfig{},
		logger: slog.Default(),
	}

	ctx := context.Background()
	httpBody := &extprocv3.HttpBody{
		Body: []byte(`{"messages": []}`),
	}

	// This method should panic as upstream filters don't process request bodies.
	require.Panics(t, func() {
		_, _ = processor.ProcessRequestBody(ctx, httpBody)
	})
}

func TestSelectTranslator(t *testing.T) {
	processor := &messagesProcessorUpstreamFilter{
		config: &processorConfig{
			backends: map[string]*processorConfigBackend{
				"gcp": {
					b: &filterapi.Backend{
						Name: "gcp",
						Schema: filterapi.VersionedAPISchema{
							Name:    filterapi.APISchemaGCPAnthropic,
							Version: "vertex-2023-10-16",
						},
					},
				},
				"anthropic": {
					b: &filterapi.Backend{
						Name: "anthropic",
						Schema: filterapi.VersionedAPISchema{
							Name: filterapi.APISchemaAnthropic,
						},
					},
				},
			},
		},
		logger: slog.Default(),
	}

	tests := []struct {
		name        string
		backend     string
		expectError bool
	}{
		{
			name:        "gcp backend",
			backend:     "gcp",
			expectError: false,
		},
		{
			name:        "anthropic backend",
			backend:     "anthropic",
			expectError: false,
		},
		{
			name:        "unsupported backend",
			backend:     "unknown",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor.backendName = tt.backend
			backend := processor.config.backends[tt.backend]
			if backend != nil {
				err := processor.selectTranslator(backend.b.Schema)
				if tt.expectError {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.NotNil(t, processor.translator)
				}
			} else {
				// For unknown backend, we expect error.
				require.True(t, tt.expectError)
			}
		})
	}
}

// mockAnthropicTranslator implements [translator.AnthropicMessagesTranslator] for testing.
type mockAnthropicTranslator struct {
	t                           *testing.T
	expRequestBody              *anthropicschema.MessagesRequest
	expForceRequestBodyMutation bool
	retHeaderMutation           []internalapi.Header
	retBodyMutation             []byte
	retTokenUsage               translator.LLMTokenUsage
	retResponseModel            internalapi.ResponseModel
	retErr                      error
}

// RequestBody implements [translator.AnthropicMessagesTranslator].
func (m mockAnthropicTranslator) RequestBody(_ []byte, body *anthropicschema.MessagesRequest, forceRequestBodyMutation bool) ([]internalapi.Header, []byte, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody, body)
	}
	require.Equal(m.t, m.expForceRequestBodyMutation, forceRequestBodyMutation)
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// ResponseHeaders implements [translator.AnthropicMessagesTranslator].
func (m mockAnthropicTranslator) ResponseHeaders(_ map[string]string) ([]internalapi.Header, error) {
	return m.retHeaderMutation, m.retErr
}

// ResponseBody implements [translator.AnthropicMessagesTranslator].
func (m mockAnthropicTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool) ([]internalapi.Header, []byte, translator.LLMTokenUsage, string, error) {
	return m.retHeaderMutation, m.retBodyMutation, m.retTokenUsage, m.retResponseModel, m.retErr
}

func TestMessagesProcessorUpstreamFilter_ProcessRequestHeaders_WithMocks(t *testing.T) {
	tests := []struct {
		name               string
		translatorErr      error
		expectError        bool
		forcedIncludeUsage bool
	}{
		{
			name:               "successful processing",
			translatorErr:      nil,
			expectError:        false,
			forcedIncludeUsage: false,
		},
		{
			name:               "translator error",
			translatorErr:      errors.New("test translator error"),
			expectError:        true,
			forcedIncludeUsage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{":path": "/anthropic/v1/messages", "x-ai-eg-model": "claude-3-sonnet"}

			// Create request body.
			requestBody := &anthropicschema.MessagesRequest{
				"model":      "claude-3-sonnet",
				"max_tokens": 1000,
				"messages":   []any{map[string]any{"role": "user", "content": "Hello"}},
			}
			requestBodyRaw := []byte(`{"model": "claude-3-sonnet", "max_tokens": 1000, "messages": [{"role": "user", "content": "Hello"}]}`)

			// Create mock translator.
			mockTranslator := mockAnthropicTranslator{
				t:                           t,
				expRequestBody:              requestBody,
				expForceRequestBodyMutation: tt.forcedIncludeUsage,
				retErr:                      tt.translatorErr,
			}

			// Create mock metrics.
			chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()

			// Create processor.
			processor := &messagesProcessorUpstreamFilter{
				config:                 &processorConfig{},
				requestHeaders:         headers,
				logger:                 slog.Default(),
				metrics:                chatMetrics,
				translator:             mockTranslator,
				originalRequestBody:    requestBody,
				originalRequestBodyRaw: requestBodyRaw,
				onRetry:                tt.forcedIncludeUsage,
			}

			ctx := context.Background()
			response, err := processor.ProcessRequestHeaders(ctx, nil)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.NotNil(t, response)
			}
		})
	}
}

func TestMessagesProcessorUpstreamFilter_ProcessResponseHeaders_WithMocks(t *testing.T) {
	mockTranslator := mockAnthropicTranslator{
		t:      t,
		retErr: nil,
	}

	chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
	processor := &messagesProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: make(map[string]string),
		logger:         slog.Default(),
		metrics:        chatMetrics,
		translator:     mockTranslator,
	}

	ctx := context.Background()
	response, err := processor.ProcessResponseHeaders(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, response)
}

func TestMessagesProcessorUpstreamFilter_ProcessResponseBody_WithMocks(t *testing.T) {
	// Create a simple test for the method that passes through.
	mockTranslator := mockAnthropicTranslator{
		t:                t,
		retResponseModel: "test-model",
		retErr:           nil,
	}

	chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
	processor := &messagesProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: make(map[string]string),
		logger:         slog.Default(),
		metrics:        chatMetrics,
		translator:     mockTranslator,
	}

	ctx := context.Background()
	httpBody := &extprocv3.HttpBody{Body: []byte(`{"test": "response"}`)}
	response, err := processor.ProcessResponseBody(ctx, httpBody)
	require.NoError(t, err)
	require.NotNil(t, response)
}

func TestMessagesProcessorUpstreamFilter_ProcessResponseBody_ErrorRecordsFailure(t *testing.T) {
	// Translator returns error; ensure failure is recorded.
	mockTranslator := mockAnthropicTranslator{
		t:      t,
		retErr: errors.New("translate error"),
	}

	mm := &mockChatCompletionMetrics{}
	processor := &messagesProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: make(map[string]string),
		logger:         slog.Default(),
		metrics:        mm,
		translator:     mockTranslator,
	}

	ctx := context.Background()
	_, err := processor.ProcessResponseBody(ctx, &extprocv3.HttpBody{})
	require.Error(t, err)
	mm.RequireRequestFailure(t)
}

func TestMessagesProcessorUpstreamFilter_ProcessResponseBody_CompletionOnlyAtEnd(t *testing.T) {
	// Verify success is recorded only at EndOfStream by checking that no error occurs mid-stream
	// and the call completes successfully at end.
	mockTranslator := mockAnthropicTranslator{
		t:      t,
		retErr: nil,
	}

	mm := &mockChatCompletionMetrics{}
	processor := &messagesProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: make(map[string]string),
		logger:         slog.Default(),
		metrics:        mm,
		translator:     mockTranslator,
		stream:         true,
	}

	ctx := context.Background()
	// Mid-stream.
	_, err := processor.ProcessResponseBody(ctx, &extprocv3.HttpBody{Body: []byte("chunk"), EndOfStream: false})
	require.NoError(t, err)
	mm.RequireRequestNotCompleted(t)

	// End-of-stream.
	_, err = processor.ProcessResponseBody(ctx, &extprocv3.HttpBody{Body: []byte("final"), EndOfStream: true})
	require.NoError(t, err)
	mm.RequireRequestSuccess(t)
}

func TestMessagesProcessorUpstreamFilter_MergeWithTokenLatencyMetadata(t *testing.T) {
	chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
	processor := &messagesProcessorUpstreamFilter{
		config:  &processorConfig{},
		logger:  slog.Default(),
		metrics: chatMetrics,
		costs:   translator.LLMTokenUsage{InputTokens: 100, OutputTokens: 50},
	}

	// Test with valid metadata structure.
	metadata := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			internalapi.AIGatewayFilterMetadataNamespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{},
					},
				},
			},
		},
	}

	// This method doesn't return anything, just test it doesn't panic.
	require.NotPanics(t, func() {
		processor.mergeWithTokenLatencyMetadata(metadata)
	})
}

func TestMessagesProcessorUpstreamFilter_SetBackend(t *testing.T) {
	headers := map[string]string{":path": "/anthropic/v1/messages"}
	chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
	processor := &messagesProcessorUpstreamFilter{
		config: &processorConfig{
			requestCosts: []processorConfigRequestCost{
				{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage", CEL: "15"}},
			},
		},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        chatMetrics,
	}

	// Test with unsupported schema (should error).
	err := processor.SetBackend(context.Background(), &filterapi.Backend{
		Name:              "some-backend",
		Schema:            filterapi.VersionedAPISchema{Name: "some-unsupported-schema", Version: "v10.0"},
		ModelNameOverride: "claude-override",
	}, nil, &messagesProcessorRouterFilter{
		config: &processorConfig{},
		logger: slog.Default(),
	})
	require.ErrorContains(t, err, "only supports backends that return native Anthropic format")
}

func Test_messagesProcessorUpstreamFilter_SetBackend_Success(t *testing.T) {
	headers := map[string]string{":path": "/anthropic/v1/messages", internalapi.ModelNameHeaderKeyDefault: "claude"}
	chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
	p := &messagesProcessorUpstreamFilter{
		config:         &processorConfig{},
		requestHeaders: headers,
		logger:         slog.Default(),
		metrics:        chatMetrics,
	}
	rp := &messagesProcessorRouterFilter{
		originalRequestBody: &anthropicschema.MessagesRequest{"model": "claude", "stream": true},
	}
	err := p.SetBackend(t.Context(), &filterapi.Backend{
		Name:              "gcp",
		Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPAnthropic, Version: "vertex-2023-10-16"},
		ModelNameOverride: "claude-vertex",
	}, nil, rp)
	require.NoError(t, err)
	require.Equal(t, "claude-vertex", p.requestHeaders[internalapi.ModelNameHeaderKeyDefault])
	require.True(t, p.stream)
	require.NotNil(t, p.translator)
}

func TestMessages_ProcessRequestHeaders_SetsRequestModel(t *testing.T) {
	headers := map[string]string{":path": "/anthropic/v1/messages", internalapi.ModelNameHeaderKeyDefault: "header-model"}
	requestBody := &anthropicschema.MessagesRequest{"model": "body-model", "messages": []any{"hello"}}
	requestBodyRaw := []byte(`{"model":"body-model","messages":["hello"]}`)
	mm := &mockChatCompletionMetrics{}
	p := &messagesProcessorUpstreamFilter{
		config:                 &processorConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             mockAnthropicTranslator{t: t, expRequestBody: requestBody},
		originalRequestBodyRaw: requestBodyRaw,
		originalRequestBody:    requestBody,
	}
	_, _ = p.ProcessRequestHeaders(t.Context(), nil)
	// Should use the override model from the header, as that's what is sent upstream.
	require.Equal(t, "body-model", mm.originalModel)
	require.Equal(t, "header-model", mm.requestModel)
	// Response model is not set until we get actual response
	require.Empty(t, mm.responseModel)
}

// TestMessages_ProcessResponseBody_UsesActualResponseModelOverHeaderOverride verifies that
// the actual response model from the API response is used for metrics, not the header override.
// This is important because the backend may return a more specific model version than what was
// requested (e.g., "claude-3-opus-20240229" instead of "claude-3-opus").
func TestMessages_ProcessResponseBody_UsesActualResponseModelOverHeaderOverride(t *testing.T) {
	headers := map[string]string{":path": "/v1/messages", internalapi.ModelNameHeaderKeyDefault: "header-model"}
	requestBody := &anthropicschema.MessagesRequest{"model": "body-model"}
	requestBodyRaw := []byte(`{"model": "body-model"}`)
	mm := &mockChatCompletionMetrics{}

	// Create a mock translator that returns token usage with response model
	mt := &mockAnthropicTranslator{
		t:              t,
		expRequestBody: requestBody,
		retTokenUsage: translator.LLMTokenUsage{
			InputTokens:  25,
			OutputTokens: 35,
		},
		retResponseModel: "actual-anthropic-model",
	}

	p := &messagesProcessorUpstreamFilter{
		config:                 &processorConfig{},
		requestHeaders:         headers,
		logger:                 slog.Default(),
		metrics:                mm,
		translator:             mt,
		originalRequestBodyRaw: requestBodyRaw,
		originalRequestBody:    requestBody,
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

	// Now process response body (should set response model from response)
	responseBytes := []byte(`{"model": "actual-anthropic-model", "content": [{"type": "text", "text": "test"}], "usage": {"input_tokens": 25, "output_tokens": 35}}`)
	_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{
		Body:        responseBytes,
		EndOfStream: true,
	})
	require.NoError(t, err)

	// Should use the override model from the header, as that's what is sent upstream.
	// Original model is from request body, request model is from header (override)
	mm.RequireSelectedModel(t, "body-model", "header-model", "actual-anthropic-model")
	// For non-streaming, only usage is recorded, not latency
	require.Equal(t, 60, mm.tokenUsageCount)
	mm.RequireRequestSuccess(t)
}

func TestMessagesProcessorUpstreamFilter_ProcessRequestHeaders_WithHeaderMutations(t *testing.T) {
	t.Run("header mutations applied correctly", func(t *testing.T) {
		headers := map[string]string{
			":path":         "/anthropic/v1/messages",
			"x-ai-eg-model": "claude-3-sonnet",
			"authorization": "bearer token123",
			"x-api-key":     "secret-key",
			"x-custom":      "custom-value",
		}

		// Create request body.
		requestBody := &anthropicschema.MessagesRequest{
			"model":      "claude-3-sonnet",
			"max_tokens": 1000,
			"messages":   []any{map[string]any{"role": "user", "content": "Hello"}},
		}
		requestBodyRaw := []byte(`{"model": "claude-3-sonnet", "max_tokens": 1000, "messages": [{"role": "user", "content": "Hello"}]}`)

		// Create header mutations.
		headerMutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"authorization", "x-api-key"},
			Set:    []filterapi.HTTPHeader{{Name: "x-new-header", Value: "new-value"}},
		}

		// Create mock translator.
		mockTranslator := mockAnthropicTranslator{
			t:                           t,
			expRequestBody:              requestBody,
			expForceRequestBodyMutation: false,
			retErr:                      nil,
		}

		// Create mock metrics.
		chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()

		// Create processor.
		processor := &messagesProcessorUpstreamFilter{
			config:                 &processorConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                chatMetrics,
			translator:             mockTranslator,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			handler:                &mockBackendAuthHandler{},
		}

		// Set header mutator.
		originalHeaders := map[string]string{
			"authorization": "bearer original-token",
			"x-api-key":     "original-secret",
		}
		processor.headerMutator = headermutator.NewHeaderMutator(headerMutations, originalHeaders)

		ctx := context.Background()
		response, err := processor.ProcessRequestHeaders(ctx, nil)

		require.NoError(t, err)
		require.NotNil(t, response)

		commonRes := response.Response.(*extprocv3.ProcessingResponse_RequestHeaders).RequestHeaders.Response

		// Check that header mutations were applied.
		require.NotNil(t, commonRes.HeaderMutation)
		require.ElementsMatch(t, []string{"authorization", "x-api-key"}, commonRes.HeaderMutation.RemoveHeaders)
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 2)
		require.Equal(t, "x-new-header", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, []byte("new-value"), commonRes.HeaderMutation.SetHeaders[0].Header.RawValue)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[1].Header.Key)
		require.Equal(t, []byte("mock-auth-handler"), commonRes.HeaderMutation.SetHeaders[1].Header.RawValue)

		// Check that headers were modified in the request headers.
		require.Equal(t, "new-value", headers["x-new-header"])
		// Sensitive headers remain locally for metrics, but will be stripped upstream by Envoy.
		require.Equal(t, "bearer token123", headers["authorization"])
		require.Equal(t, "secret-key", headers["x-api-key"])
		// x-custom remains unchanged since it wasn't in the mutations.
		require.Equal(t, "custom-value", headers["x-custom"])
	})

	t.Run("no header mutations when mutator is nil", func(t *testing.T) {
		headers := map[string]string{
			":path":         "/anthropic/v1/messages",
			"x-ai-eg-model": "claude-3-sonnet",
			"authorization": "bearer token123",
		}

		// Create request body.
		requestBody := &anthropicschema.MessagesRequest{
			"model":      "claude-3-sonnet",
			"max_tokens": 1000,
			"messages":   []any{map[string]any{"role": "user", "content": "Hello"}},
		}
		requestBodyRaw := []byte(`{"model": "claude-3-sonnet", "max_tokens": 1000, "messages": [{"role": "user", "content": "Hello"}]}`)

		// Create mock translator.
		mockTranslator := mockAnthropicTranslator{
			t:                           t,
			expRequestBody:              requestBody,
			expForceRequestBodyMutation: false,
			retErr:                      nil,
		}

		// Create mock metrics.
		chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()

		// Create processor.
		processor := &messagesProcessorUpstreamFilter{
			config:                 &processorConfig{},
			requestHeaders:         headers,
			logger:                 slog.Default(),
			metrics:                chatMetrics,
			translator:             mockTranslator,
			originalRequestBody:    requestBody,
			originalRequestBodyRaw: requestBodyRaw,
			handler:                &mockBackendAuthHandler{},
			headerMutator:          nil, // No header mutator.
		}

		ctx := context.Background()
		response, err := processor.ProcessRequestHeaders(ctx, nil)

		require.NoError(t, err)
		require.NotNil(t, response)

		commonRes := response.Response.(*extprocv3.ProcessingResponse_RequestHeaders).RequestHeaders.Response

		// Check that no header mutations were applied.
		require.NotNil(t, commonRes.HeaderMutation)
		require.Empty(t, commonRes.HeaderMutation.RemoveHeaders)
		require.Len(t, commonRes.HeaderMutation.SetHeaders, 1)
		require.Equal(t, "foo", commonRes.HeaderMutation.SetHeaders[0].Header.Key)
		require.Equal(t, []byte("mock-auth-handler"), commonRes.HeaderMutation.SetHeaders[0].Header.RawValue)

		// Check that original headers remain unchanged.
		require.Equal(t, "bearer token123", headers["authorization"])
	})
}

func TestMessagesProcessorUpstreamFilter_SetBackend_WithHeaderMutations(t *testing.T) {
	t.Run("header mutator created correctly", func(t *testing.T) {
		headers := map[string]string{":path": "/anthropic/v1/messages"}
		chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
		p := &messagesProcessorUpstreamFilter{
			config:         &processorConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        chatMetrics,
		}

		// Create backend with header mutations.
		headerMutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"x-sensitive"},
			Set:    []filterapi.HTTPHeader{{Name: "x-backend", Value: "backend-value"}},
		}

		// Original headers from router filter.
		originalHeaders := map[string]string{
			":path":       "/anthropic/v1/messages",
			"x-sensitive": "original-secret",
			"x-existing":  "original-value",
		}

		rp := &messagesProcessorRouterFilter{
			requestHeaders: originalHeaders,
			originalRequestBody: &anthropicschema.MessagesRequest{
				"model":  "claude-3-sonnet",
				"stream": false,
			},
			upstreamFilterCount: 0,
		}

		err := p.SetBackend(t.Context(), &filterapi.Backend{
			Name:           "test-backend",
			Schema:         filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPAnthropic, Version: "vertex-2023-10-16"},
			HeaderMutation: headerMutations,
		}, nil, rp)
		require.NoError(t, err)

		// Verify header mutator was created.
		require.NotNil(t, p.headerMutator)
	})

	t.Run("header mutator with original headers", func(t *testing.T) {
		headers := map[string]string{":path": "/anthropic/v1/messages"}
		chatMetrics := metrics.NewChatCompletionFactory(noop.NewMeterProvider().Meter("test"), map[string]string{})()
		p := &messagesProcessorUpstreamFilter{
			config:         &processorConfig{},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        chatMetrics,
		}

		// Create backend with header mutations that don't remove x-custom.
		headerMutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"authorization"},
		}

		// Original headers from router filter (simulate what would be in rp.requestHeaders).
		originalHeaders := map[string]string{
			":path":         "/anthropic/v1/messages",
			"authorization": "bearer original-token", // This will be removed, so won't be restored.
			"x-custom":      "original-value",        // This won't be removed, so can be restored.
			"x-existing":    "existing-value",        // This won't be removed, so can be restored.
		}

		rp := &messagesProcessorRouterFilter{
			requestHeaders: originalHeaders,
			originalRequestBody: &anthropicschema.MessagesRequest{
				"model":  "claude-3-sonnet",
				"stream": false,
			},
			upstreamFilterCount: 0,
		}

		err := p.SetBackend(t.Context(), &filterapi.Backend{
			Name:           "test-backend",
			Schema:         filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPAnthropic, Version: "vertex-2023-10-16"},
			HeaderMutation: headerMutations,
		}, nil, rp)
		require.NoError(t, err)

		// Verify header mutator was created with original headers.
		require.NotNil(t, p.headerMutator)
	})
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/responses"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

var (
	basicResponseReq = &openai.ResponseRequest{
		Model: openai.ModelGPT5Nano,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: param.Opt[string]{Value: "Hi"},
		},
	}
	basicResponseReqBody = mustJSON(basicResponseReq)

	basicResponseResp = &openai.Response{
		ID:    "resp-123",
		Model: openai.ModelGPT5Nano,
		Output: []responses.ResponseOutputItemUnion{
			{
				ID:   "msg_01",
				Type: "message",
				Role: "assistant",
				Content: []responses.ResponseOutputMessageContentUnion{
					{
						Type: "output_text",
						Text: "Hello, how can I help?",
					},
				},
			},
		},
		Usage: &openai.ResponseUsage{
			InputTokens: 20,
			InputTokensDetails: openai.ResponseUsageInputTokensDetails{
				CachedTokens: 2,
			},
			OutputTokens: 10,
			TotalTokens:  30,
		},
	}
	basicResponseRespBody = mustJSON(basicResponseResp)

	responseReqWithStreaming = &openai.ResponseRequest{
		Model: openai.ModelGPT5Nano,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: param.Opt[string]{Value: "Hi"},
		},
		Stream: true,
	}
	responseReqWithStreamingBody = mustJSON(responseReqWithStreaming)
)

func TestResponsesRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.ResponseRequest
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicResponseReq,
			reqBody:          basicResponseReqBody,
			expectedSpanName: "Responses",
		},
		{
			name:             "streaming request",
			req:              responseReqWithStreaming,
			reqBody:          responseReqWithStreamingBody,
			expectedSpanName: "Responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewResponsesRecorderFromEnv()

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestResponsesRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ResponseRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicResponseReq,
			reqBody: basicResponseReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(basicResponseReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:    "streaming request",
			req:     responseReqWithStreaming,
			reqBody: responseReqWithStreamingBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(responseReqWithStreamingBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewResponsesRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestResponsesRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.Response
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicResponseResp,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.Int(openinference.LLMTokenCountPrompt, 20),
				attribute.Int(openinference.LLMTokenCountCompletion, 10),
				attribute.Int(openinference.LLMTokenCountTotal, 30),
				attribute.Int(openinference.LLMTokenCountPromptCacheHit, 2),
				attribute.String(openinference.OutputValue, string(basicResponseRespBody)),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewResponsesRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestResponsesRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "400 bad request error",
			statusCode: 400,
			errorBody:  []byte(`{"error":{"message":"Invalid request","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 400 - {\"error\":{\"message\":\"Invalid request\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "500 internal server error",
			statusCode: 500,
			errorBody:  []byte(`{"error":{"message":"Internal server error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 500 - {\"error\":{\"message\":\"Internal server error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "401 authentication error",
			statusCode: 401,
			errorBody:  []byte(`{"error":{"message":"Unauthorized","type":"authentication_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 401 - {\"error\":{\"message\":\"Unauthorized\",\"type\":\"authentication_error\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewResponsesRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponseOnError(span, tt.statusCode, tt.errorBody)
				return false
			})

			require.Equal(t, tt.expectedStatus, actualSpan.Status)
			require.Len(t, actualSpan.Events, tt.expectedEvents)
			if tt.expectedEvents > 0 {
				require.Equal(t, "exception", actualSpan.Events[0].Name)
			}
		})
	}
}

func TestResponsesRecorder_RecordResponseChunks_Empty(t *testing.T) {
	recorder := NewResponsesRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseChunks(span, []*openai.ResponseStreamEventUnion{})
		return false
	})

	// Empty chunks should not produce any events
	openinference.RequireEventsEqual(t, []trace.Event{}, actualSpan.Events)
}

func TestResponsesRecorder_WithConfig_HideInputs(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "hide input value",
			config: &openinference.TraceConfig{
				HideInputs: true,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "show input value",
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(basicResponseReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewResponsesRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, basicResponseReq, basicResponseReqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
		})
	}
}

func TestResponsesRecorder_ConfigFromEnvironment(t *testing.T) {
	// Test that recorder uses environment variables when config is nil.
	t.Setenv(openinference.EnvHideInputs, "true")

	recorder := NewResponsesRecorderFromEnv()

	// Request test.
	reqSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, basicResponseReq, basicResponseReqBody)
		return false
	})

	// Verify input is hidden.
	attrs := make(map[string]attribute.Value)
	for _, kv := range reqSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue].AsString())
}

func TestResponsesRecorder_NewResponsesRecorder_NilConfig(t *testing.T) {
	// Test that NewResponsesRecorder uses NewTraceConfigFromEnv() when config is nil
	recorder := NewResponsesRecorder(nil)

	require.NotNil(t, recorder)

	// Verify it's a ResponsesRecorder
	_, ok := recorder.(*ResponsesRecorder)
	require.True(t, ok)
}

func TestResponsesRecorder_EmptyResponseBody(t *testing.T) {
	emptyResp := &openai.Response{
		ID:    "resp-empty",
		Model: openai.ModelGPT5Nano,
	}

	recorder := NewResponsesRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, emptyResp)
		return false
	})

	// Should have LLMModelName and OutputValue (since token counts are 0)
	attrs := make(map[string]attribute.Value)
	for _, kv := range actualSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openai.ModelGPT5Nano, attrs[openinference.LLMModelName].AsString())
	require.NotEmpty(t, attrs[openinference.OutputValue].AsString())
	require.Equal(t, codes.Ok, actualSpan.Status.Code)
}

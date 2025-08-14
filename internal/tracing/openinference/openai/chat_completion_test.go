// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func TestChatCompletionRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.ChatCompletionRequest
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicReq,
			reqBody:          basicReqBody,
			expectedSpanName: "ChatCompletion",
		},
		{
			name:             "streaming request",
			req:              streamingReq,
			reqBody:          streamingReqBody,
			expectedSpanName: "ChatCompletion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewChatCompletionRecorderFromEnv()

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestChatCompletionRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ChatCompletionRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicReq,
			reqBody: basicReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(basicReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "Hello!"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewChatCompletionRecorderFromEnv()

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

func TestChatCompletionRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		respBody       []byte
		expectedAttrs  []attribute.KeyValue
		expectedEvents []trace.Event
		expectedStatus trace.Status
	}{
		{
			name:       "successful response",
			statusCode: 200,
			respBody:   basicRespBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageRole), "assistant"),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageContent), "Hello! How can I help you today?"),
				attribute.Int(openinference.LLMTokenCountPrompt, 20),
				attribute.Int(openinference.LLMTokenCountCompletion, 10),
				attribute.Int(openinference.LLMTokenCountTotal, 30),
				attribute.String(openinference.OutputValue, string(basicRespBody)),
			},
			expectedEvents: nil,
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:          "error response",
			statusCode:    400,
			respBody:      []byte(`{"error":{"message":"Invalid request","type":"invalid_request_error"}}`),
			expectedAttrs: []attribute.KeyValue{},
			expectedEvents: []trace.Event{{
				Name: "exception",
				Attributes: []attribute.KeyValue{
					attribute.String("exception.type", "BadRequestError"),
					attribute.String("exception.message", `Error code: 400 - {"error":{"message":"Invalid request","type":"invalid_request_error"}}`),
				},
				Time: time.Time{},
			}},
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: `Error code: 400 - {"error":{"message":"Invalid request","type":"invalid_request_error"}}`,
			},
		},
		{
			name:       "partial/invalid JSON response",
			statusCode: 200,
			respBody:   []byte(`{"id":"chatcmpl-123","object":"chat.completion"`),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputValue, `{"id":"chatcmpl-123","object":"chat.completion"`),
			},
			expectedEvents: []trace.Event{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewChatCompletionRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.statusCode, tt.respBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			openinference.RequireEventsEqual(t, tt.expectedEvents, actualSpan.Events)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestChatCompletionRecorder_RecordChunk(t *testing.T) {
	recorder := NewChatCompletionRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordChunk(span, 0) // First chunk adds event.
		recorder.RecordChunk(span, 1) // Subsequent chunks do not.
		return false
	})

	openinference.RequireEventsEqual(t, []trace.Event{
		{
			Name: "First Token Stream Event",
			Time: time.Time{},
		},
	}, actualSpan.Events)
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func ptr[T any](v T) *T {
	return &v
}

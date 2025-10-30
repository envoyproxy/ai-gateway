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

func TestResponsesRecorder_StartParams(t *testing.T) {
	recorder := NewResponsesRecorderFromEnv()

	spanName, opts := recorder.StartParams(&openai.ResponseRequest{}, []byte("{}"))
	actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

	require.Equal(t, "CreateResponse", actualSpan.Name)
	require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
	_ = actualSpan
}

func TestResponsesRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ResponseRequest
		body          []byte
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "model and inputs visible",
			req:    &openai.ResponseRequest{Model: "gpt-test"},
			body:   []byte(`{"input":"hello"}`),
			config: &openinference.TraceConfig{HideInputs: false},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-test"),
				attribute.String(openinference.InputValue, string([]byte(`{"input":"hello"}`))),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "model present inputs hidden",
			req:    &openai.ResponseRequest{Model: "gpt-test"},
			body:   []byte(`{"input":"secret"}`),
			config: &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-test"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "no model inputs visible empty body",
			req:    &openai.ResponseRequest{},
			body:   []byte{},
			config: &openinference.TraceConfig{HideInputs: false},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, ""),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recorder *ResponsesRecorder
			if tt.config == nil {
				recorder = NewResponsesRecorderFromEnv().(*ResponsesRecorder)
			} else {
				recorder = NewResponsesRecorder(tt.config).(*ResponsesRecorder)
			}

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.body)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestResponsesRecorder_RecordResponse(t *testing.T) {
	recorder := NewResponsesRecorderFromEnv()

	resp := &openai.ResponseResponse{}
	resp.Model = "m-1"
	resp.Usage = &openai.ResponseUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}

	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, resp)
		return false
	})

	expectedAttrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, "m-1"),
		attribute.Int(openinference.LLMTokenCountPrompt, 1),
		attribute.Int(openinference.LLMTokenCountCompletion, 2),
		attribute.Int(openinference.LLMTokenCountTotal, 3),
		attribute.String(openinference.OutputValue, string(respBytes)),
	}

	openinference.RequireAttributesEqual(t, expectedAttrs, actualSpan.Attributes)
	require.Equal(t, trace.Status{Code: codes.Ok, Description: ""}, actualSpan.Status)
}

func TestResponsesRecorder_RecordResponse_HideOutputs(t *testing.T) {
	cfg := openinference.NewTraceConfig()
	cfg.HideOutputs = true
	recorder := NewResponsesRecorder(cfg)

	resp := &openai.ResponseResponse{Model: "m-2", Usage: &openai.ResponseUsage{InputTokens: 4, OutputTokens: 5, TotalTokens: 9}}

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, resp)
		return false
	})

	expectedAttrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, "m-2"),
		attribute.Int(openinference.LLMTokenCountPrompt, 4),
		attribute.Int(openinference.LLMTokenCountCompletion, 5),
		attribute.Int(openinference.LLMTokenCountTotal, 9),
		attribute.String(openinference.OutputValue, openinference.RedactedValue),
	}

	openinference.RequireAttributesEqual(t, expectedAttrs, actualSpan.Attributes)
	require.Equal(t, trace.Status{Code: codes.Ok, Description: ""}, actualSpan.Status)
}

func TestResponsesRecorder_RecordResponseOnError(t *testing.T) {
	recorder := NewResponsesRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseOnError(span, 400, []byte(`{"error":"invalid"}`))
		return false
	})

	openinference.RequireAttributesEqual(t, []attribute.KeyValue{}, actualSpan.Attributes)
	openinference.RequireEventsEqual(t, []trace.Event{{
		Name: "exception",
		Attributes: []attribute.KeyValue{
			attribute.String("exception.type", "BadRequestError"),
			attribute.String("exception.message", "Error code: 400 - {\"error\":\"invalid\"}"),
		},
		Time: time.Time{},
	}}, actualSpan.Events)
	require.Equal(t, trace.Status{Code: codes.Error, Description: "Error code: 400 - {\"error\":\"invalid\"}"}, actualSpan.Status)
}

func TestResponsesRecorder_RecordResponseChunk(t *testing.T) {
	recorder := NewResponsesRecorderFromEnv()

	resp := openai.ResponseResponse{Model: "m-chunk", Usage: &openai.ResponseUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
	chunk := &openai.ResponseCompletedEvent{Response: resp}

	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseChunk(span, chunk)
		return false
	})

	// Expect event for completion and attributes from RecordResponse
	openinference.RequireEventsEqual(t, []trace.Event{{
		Name: "Response Completed Event",
		Time: time.Time{},
	}}, actualSpan.Events)

	expectedAttrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, "m-chunk"),
		attribute.Int(openinference.LLMTokenCountPrompt, 2),
		attribute.Int(openinference.LLMTokenCountCompletion, 3),
		attribute.Int(openinference.LLMTokenCountTotal, 5),
		attribute.String(openinference.OutputValue, string(respBytes)),
	}
	openinference.RequireAttributesEqual(t, expectedAttrs, actualSpan.Attributes)
}

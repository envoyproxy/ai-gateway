// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

var (
	basicTranslationReq = &openai.TranslationRequest{
		Model:          "whisper-1",
		ResponseFormat: "json",
		FileName:       "audio.mp3",
		FileSize:       88200,
	}

	translationReqNoModel = &openai.TranslationRequest{
		ResponseFormat: "json",
		FileName:       "audio.mp3",
		FileSize:       88200,
	}

	basicTranslationResp = &openai.TranslationResponse{
		Text: "The sun rises in the east.",
	}
)

func TestNewTranslationRecorderFromEnv(t *testing.T) {
	recorder := NewTranslationRecorderFromEnv()
	require.NotNil(t, recorder)
	require.IsType(t, &TranslationRecorder{}, recorder)
}

func TestNewTranslationRecorder(t *testing.T) {
	tests := []struct {
		name   string
		config *openinference.TraceConfig
	}{
		{name: "nil config", config: nil},
		{name: "empty config", config: &openinference.TraceConfig{}},
		{name: "hide inputs", config: &openinference.TraceConfig{HideInputs: true}},
		{name: "hide outputs", config: &openinference.TraceConfig{HideOutputs: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranslationRecorder(tt.config)
			require.NotNil(t, recorder)
			require.IsType(t, &TranslationRecorder{}, recorder)
		})
	}
}

func TestTranslationRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.TranslationRequest
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicTranslationReq,
			expectedSpanName: "Translation",
		},
		{
			name:             "empty request",
			req:              &openai.TranslationRequest{},
			expectedSpanName: "Translation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranslationRecorderFromEnv()
			spanName, opts := recorder.StartParams(tt.req, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestTranslationRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.TranslationRequest
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "basic request with model",
			req:    basicTranslationReq,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "whisper-1"),
				attribute.String(openinference.InputValue,
					fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"response_format":"%s"}`,
						"whisper-1", "audio.mp3", int64(88200), "json")),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "request without model",
			req:    translationReqNoModel,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue,
					fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"response_format":"%s"}`,
						"", "audio.mp3", int64(88200), "json")),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "hidden inputs",
			req:    basicTranslationReq,
			config: &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "whisper-1"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranslationRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, nil)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestTranslationRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.TranslationResponse
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicTranslationResp,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputValue, "The sun rises in the east."),
				attribute.String(openinference.OutputMimeType, "text/plain"),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           basicTranslationResp,
			config:         &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "nil response",
			resp:           nil,
			config:         &openinference.TraceConfig{},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranslationRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestTranslationRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "400 bad request",
			statusCode: 400,
			errorBody:  []byte(`{"error":{"message":"Invalid audio format"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 400 - {\"error\":{\"message\":\"Invalid audio format\"}}",
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
			name:       "429 rate limit exceeded",
			statusCode: 429,
			errorBody:  []byte(`{"error":{"message":"Rate limit exceeded"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 429 - {\"error\":{\"message\":\"Rate limit exceeded\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranslationRecorderFromEnv()

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

var _ tracingapi.TranslationRecorder = (*TranslationRecorder)(nil)

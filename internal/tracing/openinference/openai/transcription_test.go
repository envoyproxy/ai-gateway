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
	basicTranscriptionReq = &openai.TranscriptionRequest{
		Model:          "whisper-1",
		Language:       "en",
		ResponseFormat: "json",
		FileName:       "test.wav",
		FileSize:       44100,
	}

	transcriptionReqNoModel = &openai.TranscriptionRequest{
		Language:       "en",
		ResponseFormat: "json",
		FileName:       "test.wav",
		FileSize:       44100,
	}

	basicTranscriptionResp = &openai.TranscriptionResponse{
		Text:     "Hello world, this is a test.",
		Language: "en",
		Duration: 5.5,
	}

	transcriptionRespMinimal = &openai.TranscriptionResponse{
		Text: "Minimal transcription.",
	}
)

func TestNewTranscriptionRecorderFromEnv(t *testing.T) {
	recorder := NewTranscriptionRecorderFromEnv()
	require.NotNil(t, recorder)
	require.IsType(t, &TranscriptionRecorder{}, recorder)
}

func TestNewTranscriptionRecorder(t *testing.T) {
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
			recorder := NewTranscriptionRecorder(tt.config)
			require.NotNil(t, recorder)
			require.IsType(t, &TranscriptionRecorder{}, recorder)
		})
	}
}

func TestTranscriptionRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.TranscriptionRequest
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicTranscriptionReq,
			expectedSpanName: "Transcription",
		},
		{
			name:             "empty request",
			req:              &openai.TranscriptionRequest{},
			expectedSpanName: "Transcription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewTranscriptionRecorderFromEnv()
			spanName, opts := recorder.StartParams(tt.req, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestTranscriptionRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.TranscriptionRequest
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "basic request with model",
			req:    basicTranscriptionReq,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "whisper-1"),
				attribute.String(openinference.InputValue,
					fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"language":"%s","response_format":"%s"}`,
						"whisper-1", "test.wav", int64(44100), "en", "json")),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "request without model",
			req:    transcriptionReqNoModel,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue,
					fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"language":"%s","response_format":"%s"}`,
						"", "test.wav", int64(44100), "en", "json")),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:   "hidden inputs",
			req:    basicTranscriptionReq,
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
			recorder := NewTranscriptionRecorder(tt.config)

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

func TestTranscriptionRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.TranscriptionResponse
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "full response with duration and language",
			resp:   basicTranscriptionResp,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputValue, "Hello world, this is a test."),
				attribute.String(openinference.OutputMimeType, "text/plain"),
				attribute.Float64("output.audio_duration", 5.5),
				attribute.String("output.language", "en"),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:   "minimal response without duration or language",
			resp:   transcriptionRespMinimal,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputValue, "Minimal transcription."),
				attribute.String(openinference.OutputMimeType, "text/plain"),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           basicTranscriptionResp,
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
			recorder := NewTranscriptionRecorder(tt.config)

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

func TestTranscriptionRecorder_RecordResponseOnError(t *testing.T) {
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
			recorder := NewTranscriptionRecorderFromEnv()

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

var _ tracingapi.TranscriptionRecorder = (*TranscriptionRecorder)(nil)

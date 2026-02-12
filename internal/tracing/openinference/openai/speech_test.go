// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// Test data.
var (
	basicSpeechReq = &openai.SpeechRequest{
		Model: "tts-1",
		Input: "Hello, how are you?",
		Voice: "alloy",
	}
	basicSpeechReqBody = []byte(`{"model":"tts-1","input":"Hello, how are you?","voice":"alloy"}`)

	advancedSpeechReq = func() *openai.SpeechRequest {
		responseFormat := "opus"
		speed := 1.5
		instructions := "Speak clearly and slowly."
		return &openai.SpeechRequest{
			Model:          "tts-1-hd",
			Input:          "The quick brown fox jumps over the lazy dog.",
			Voice:          "nova",
			ResponseFormat: &responseFormat,
			Speed:          &speed,
			Instructions:   &instructions,
		}
	}()
	advancedSpeechReqBody = []byte(`{"model":"tts-1-hd","input":"The quick brown fox jumps over the lazy dog.","voice":"nova","response_format":"opus","speed":1.5,"instructions":"Speak clearly and slowly."}`)

	basicAudioResponse = []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x00} // Sample MP3 header bytes
)

func TestNewSpeechRecorderFromEnv(t *testing.T) {
	recorder := NewSpeechRecorderFromEnv()
	require.NotNil(t, recorder)
	require.IsType(t, &SpeechRecorder{}, recorder)
}

func TestNewSpeechRecorder(t *testing.T) {
	tests := []struct {
		name   string
		config *openinference.TraceConfig
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name:   "empty config",
			config: &openinference.TraceConfig{},
		},
		{
			name: "config with hide inputs",
			config: &openinference.TraceConfig{
				HideInputs: true,
			},
		},
		{
			name: "config with hide outputs",
			config: &openinference.TraceConfig{
				HideOutputs: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewSpeechRecorder(tt.config)
			require.NotNil(t, recorder)
			require.IsType(t, &SpeechRecorder{}, recorder)
		})
	}
}

func TestSpeechRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.SpeechRequest
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicSpeechReq,
			reqBody:          basicSpeechReqBody,
			expectedSpanName: "AudioSpeech",
		},
		{
			name:             "advanced request",
			req:              advancedSpeechReq,
			reqBody:          advancedSpeechReqBody,
			expectedSpanName: "AudioSpeech",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewSpeechRecorderFromEnv()

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestSpeechRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.SpeechRequest
		reqBody       []byte
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicSpeechReq,
			reqBody: basicSpeechReqBody,
			config:  &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, `{"input":"Hello, how are you?","voice":"alloy","response_format":null,"speed":null,"instructions":null}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, string(basicSpeechReqBody)),
			},
		},
		{
			name:    "advanced request with all fields",
			req:     advancedSpeechReq,
			reqBody: advancedSpeechReqBody,
			config:  &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1-hd"),
				attribute.String(openinference.InputValue, `{"input":"The quick brown fox jumps over the lazy dog.","voice":"nova","response_format":"opus","speed":1.5,"instructions":"Speak clearly and slowly."}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, string(advancedSpeechReqBody)),
			},
		},
		{
			name:    "hidden inputs",
			req:     basicSpeechReq,
			reqBody: basicSpeechReqBody,
			config:  &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, string(basicSpeechReqBody)),
			},
		},
		{
			name:    "hidden invocation parameters",
			req:     basicSpeechReq,
			reqBody: basicSpeechReqBody,
			config:  &openinference.TraceConfig{HideLLMInvocationParameters: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, `{"input":"Hello, how are you?","voice":"alloy","response_format":null,"speed":null,"instructions":null}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
		{
			name:    "empty model name",
			req:     &openai.SpeechRequest{Input: "Test", Voice: "alloy"},
			reqBody: []byte(`{"input":"Test","voice":"alloy"}`),
			config:  &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, `{"input":"Test","voice":"alloy","response_format":null,"speed":null,"instructions":null}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"input":"Test","voice":"alloy"}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewSpeechRecorder(tt.config)

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

func TestSpeechRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *[]byte
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "basic audio response",
			resp:   &basicAudioResponse,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, "audio/mpeg"),
				attribute.Int("output.audio_bytes", len(basicAudioResponse)),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:   "large audio response",
			resp:   func() *[]byte { data := make([]byte, 10000); return &data }(),
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, "audio/mpeg"),
				attribute.Int("output.audio_bytes", 10000),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           &basicAudioResponse,
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
		{
			name:   "empty response",
			resp:   &[]byte{},
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, "audio/mpeg"),
				attribute.Int("output.audio_bytes", 0),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewSpeechRecorder(tt.config)

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

func TestSpeechRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "404 not found error",
			statusCode: 404,
			errorBody:  []byte(`{"error":{"message":"Model not found","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 404 - {\"error\":{\"message\":\"Model not found\",\"type\":\"invalid_request_error\"}}",
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
			name:       "400 bad request",
			statusCode: 400,
			errorBody:  []byte(`{"error":{"message":"Invalid voice parameter"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 400 - {\"error\":{\"message\":\"Invalid voice parameter\"}}",
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
			recorder := NewSpeechRecorderFromEnv()

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

func TestBuildSpeechRequestAttributes(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.SpeechRequest
		body          string
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "basic request attributes",
			req:    basicSpeechReq,
			body:   string(basicSpeechReqBody),
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, `{"input":"Hello, how are you?","voice":"alloy","response_format":null,"speed":null,"instructions":null}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, string(basicSpeechReqBody)),
			},
		},
		{
			name:   "hidden inputs",
			req:    basicSpeechReq,
			body:   string(basicSpeechReqBody),
			config: &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, string(basicSpeechReqBody)),
			},
		},
		{
			name:   "hidden invocation parameters",
			req:    basicSpeechReq,
			body:   string(basicSpeechReqBody),
			config: &openinference.TraceConfig{HideLLMInvocationParameters: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "tts-1"),
				attribute.String(openinference.InputValue, `{"input":"Hello, how are you?","voice":"alloy","response_format":null,"speed":null,"instructions":null}`),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildSpeechRequestAttributes(tt.req, tt.body, tt.config)
			openinference.RequireAttributesEqual(t, tt.expectedAttrs, attrs)
		})
	}
}

// Ensure SpeechRecorder implements the interface.
var _ interface {
	StartParams(*openai.SpeechRequest, []byte) (string, []oteltrace.SpanStartOption)
	RecordRequest(oteltrace.Span, *openai.SpeechRequest, []byte)
	RecordResponse(oteltrace.Span, *[]byte)
	RecordResponseOnError(oteltrace.Span, int, []byte)
} = (*SpeechRecorder)(nil)

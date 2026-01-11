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
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

var (
	basicImageReq = &openai.ImageGenerationRequest{
		Model:          "gpt-image-1",
		Prompt:         "a hummingbird",
		Size:           "1024x1024",
		Quality:        "hd",
		ResponseFormat: "b64_json",
		N:              1,
	}
	basicImageReqBody = mustJSON(basicImageReq)

	basicImageResp = &openai.ImageGenerationResponse{
		Data: []openai.ImageGenerationResponseData{{URL: "https://example.com/img.png"}},
		Usage: &openai.ImageGenerationUsage{
			InputTokens:  8,
			OutputTokens: 1056,
			TotalTokens:  1064,
		},
	}
	basicImageRespBody = mustJSON(basicImageResp)
)

func TestImageGenerationRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.ImageGenerationRequest
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicImageReq,
			reqBody:          basicImageReqBody,
			expectedSpanName: "ImagesResponse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(nil)

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestImageGenerationRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ImageGenerationRequest
		reqBody       []byte
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicImageReq,
			reqBody: basicImageReqBody,
			config:  &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, string(basicImageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, string(basicImageReqBody)),
			},
		},
		{
			name:    "hidden inputs",
			req:     basicImageReq,
			reqBody: basicImageReqBody,
			config:  &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, string(basicImageReqBody)),
			},
		},
		{
			name:    "hidden invocation parameters",
			req:     basicImageReq,
			reqBody: basicImageReqBody,
			config:  &openinference.TraceConfig{HideLLMInvocationParameters: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, string(basicImageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(tt.config)

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

func TestImageGenerationRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.ImageGenerationResponse
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedEvents []trace.Event
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicImageResp,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputValue, string(basicImageRespBody)),
			},
			expectedEvents: nil,
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:   "hidden outputs",
			resp:   basicImageResp,
			config: &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputValue, openinference.RedactedValue),
			},
			expectedEvents: nil,
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			openinference.RequireEventsEqual(t, tt.expectedEvents, actualSpan.Events)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestImageGenerationRecorder_RecordResponseOnError(t *testing.T) {
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
			errorBody:  []byte(`{"error":{"message":"Invalid input"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 400 - {\"error\":{\"message\":\"Invalid input\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorderFromEnv()

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

var _ tracing.ImageGenerationRecorder = (*ImageGenerationRecorder)(nil)

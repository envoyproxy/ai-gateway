// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"encoding/json"
	"testing"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

// Test data for image generation span tests

// Mock recorder for testing image generation span
type testImageGenerationRecorder struct{}

func (r testImageGenerationRecorder) StartParams(_ *openaisdk.ImageGenerateParams, _ []byte) (string, []oteltrace.SpanStartOption) {
	return "ImagesResponse", nil
}

func (r testImageGenerationRecorder) RecordRequest(span oteltrace.Span, req *openaisdk.ImageGenerateParams, _ []byte) {
	span.SetAttributes(
		attribute.String("model", req.Model),
		attribute.String("prompt", req.Prompt),
		attribute.String("size", string(req.Size)),
	)
}

func (r testImageGenerationRecorder) RecordResponse(span oteltrace.Span, resp *openaisdk.ImagesResponse) {
	respBytes, _ := json.Marshal(resp)
	span.SetAttributes(
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	)
}

func (r testImageGenerationRecorder) RecordResponseOnError(span oteltrace.Span, statusCode int, body []byte) {
	span.SetAttributes(
		attribute.Int("statusCode", statusCode),
		attribute.String("errorBody", string(body)),
	)
}

func TestImageGenerationSpan_RecordResponse(t *testing.T) {
	resp := &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{{URL: "https://example.com/test.png"}},
		Size: openaisdk.ImagesResponseSize1024x1024,
		Usage: openaisdk.ImagesResponseUsage{
			InputTokens:  5,
			OutputTokens: 100,
			TotalTokens:  105,
		},
	}
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	s := &imageGenerationSpan{recorder: testImageGenerationRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false // Recording response shouldn't end the span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	}, actualSpan.Attributes)
}

func TestImageGenerationSpan_EndSpan(t *testing.T) {
	s := &imageGenerationSpan{recorder: testImageGenerationRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.EndSpan()
		return true // EndSpan ends the underlying span.
	})

	// EndSpan should not add any attributes, just end the span
	require.Empty(t, actualSpan.Attributes)
}

func TestImageGenerationSpan_EndSpanOnError(t *testing.T) {
	errorMsg := "image generation failed"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &imageGenerationSpan{span: span, recorder: testImageGenerationRecorder{}}
		s.EndSpanOnError(500, []byte(errorMsg))
		return true // EndSpanOnError ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", errorMsg),
	}, actualSpan.Attributes)
}

func TestImageGenerationSpan_RecordResponse_WithMultipleImages(t *testing.T) {
	resp := &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{
			{URL: "https://example.com/img1.png"},
			{URL: "https://example.com/img2.png"},
			{URL: "https://example.com/img3.png"},
		},
		Size: openaisdk.ImagesResponseSize1024x1024,
		Usage: openaisdk.ImagesResponseUsage{
			InputTokens:  10,
			OutputTokens: 200,
			TotalTokens:  210,
		},
	}
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	s := &imageGenerationSpan{recorder: testImageGenerationRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	}, actualSpan.Attributes)
}

func TestImageGenerationSpan_EndSpanOnError_WithDifferentStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorBody  string
	}{
		{
			name:       "bad request",
			statusCode: 400,
			errorBody:  `{"error":{"message":"Invalid prompt","type":"invalid_request_error"}}`,
		},
		{
			name:       "rate limit",
			statusCode: 429,
			errorBody:  `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`,
		},
		{
			name:       "server error",
			statusCode: 500,
			errorBody:  `{"error":{"message":"Internal server error","type":"server_error"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				s := &imageGenerationSpan{span: span, recorder: testImageGenerationRecorder{}}
				s.EndSpanOnError(tt.statusCode, []byte(tt.errorBody))
				return true
			})

			require.Equal(t, []attribute.KeyValue{
				attribute.Int("statusCode", tt.statusCode),
				attribute.String("errorBody", tt.errorBody),
			}, actualSpan.Attributes)
		})
	}
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"testing"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func TestImageGenerationRecorder_WithConfig_HideInputs(t *testing.T) {
	req := basicImageReq
	reqBody := basicImageReqBody

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
				attribute.String(openinference.LLMModelName, req.Model),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				// No InputMimeType when input is hidden.
			},
		},
		{
			name:   "show input value by default",
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, req.Model),
				attribute.String(openinference.InputValue, string(reqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, req, reqBody)
				return false
			})

			attrs := attributesToMap(actualSpan.Attributes)
			// Required base attrs
			_, hasKind := attrs[openinference.SpanKind]
			_, hasSystem := attrs[openinference.LLMSystem]
			_, hasModel := attrs[openinference.LLMModelName]
			require.True(t, hasKind && hasSystem && hasModel)

			if tt.config.HideInputs {
				require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue])
				_, hasMime := attrs[openinference.InputMimeType]
				require.False(t, hasMime)
			} else {
				require.Equal(t, string(reqBody), attrs[openinference.InputValue])
				require.Equal(t, openinference.MimeTypeJSON, attrs[openinference.InputMimeType])
			}
		})
	}
}

func TestImageGenerationRecorder_WithConfig_HideOutputs(t *testing.T) {
	resp := &openaisdk.ImagesResponse{Data: []openaisdk.Image{{URL: "https://example.com/img.png"}}}
	respBody, err := json.Marshal(resp)
	require.NoError(t, err)

	tests := []struct {
		name           string
		config         *openinference.TraceConfig
		expectedStatus trace.Status
	}{
		{
			name: "hide output value",
			config: &openinference.TraceConfig{
				HideOutputs: true,
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "show output value",
			config:         &openinference.TraceConfig{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				var r openaisdk.ImagesResponse
				require.NoError(t, json.Unmarshal(respBody, &r))
				recorder.RecordResponse(span, &r)
				return false
			})

			attrs := attributesToMap(actualSpan.Attributes)
			// Output MIME type should be set regardless
			require.Equal(t, openinference.MimeTypeJSON, attrs[openinference.OutputMimeType])
			if tt.config.HideOutputs {
				require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue])
			} else {
				require.Equal(t, string(respBody), attrs[openinference.OutputValue])
			}
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

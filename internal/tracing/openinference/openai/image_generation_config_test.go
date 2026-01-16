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
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testotel"
)

func TestImageGenerationRecorder_WithConfig_HideInputs(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		req           *openai.ImageGenerationRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "hide input value",
			config: &openinference.TraceConfig{
				HideInputs: true,
			},
			req:     basicImageReq,
			reqBody: basicImageReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, string(basicImageReqBody)),
			},
		},
		{
			name: "hide invocation parameters",
			config: &openinference.TraceConfig{
				HideLLMInvocationParameters: true,
			},
			req:     basicImageReq,
			reqBody: basicImageReqBody,
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
		})
	}
}

func TestImageGenerationRecorder_WithConfig_HideOutputs(t *testing.T) {
	recorder := NewImageGenerationRecorder(&openinference.TraceConfig{
		HideInputs:  true,
		HideOutputs: true,
	})

	tests := []struct {
		name           string
		fn             func(oteltrace.Span) bool
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name: "RecordRequest redacts InputValue",
			fn: func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, basicImageReq, basicImageReqBody)
				return false
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, string(basicImageReqBody)),
			},
		},
		{
			name: "RecordResponse redacts OutputValue",
			fn: func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, basicImageResp)
				return false
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputValue, openinference.RedactedValue),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualSpan := testotel.RecordWithSpan(t, tt.fn)
			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestImageGenerationRecorder_ConfigFromEnvironment(t *testing.T) {
	t.Setenv(openinference.EnvHideInputs, "true")
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewImageGenerationRecorderFromEnv()

	reqSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, basicImageReq, basicImageReqBody)
		return false
	})

	attrs := make(map[string]attribute.Value)
	for _, kv := range reqSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue].AsString())

	respSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicImageResp)
		return false
	})

	attrs = make(map[string]attribute.Value)
	for _, kv := range respSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue].AsString())
}

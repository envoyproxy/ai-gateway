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

func TestCompletionRecorder_WithConfig_HideInputs(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		req           *openai.CompletionRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "hide input value",
			config: &openinference.TraceConfig{
				HideInputs: true,
			},
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				// No InputMimeType when input is hidden.
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompts when HideInputs is true.
			},
		},
		{
			name: "hide prompts only",
			config: &openinference.TraceConfig{
				HidePrompts: true,
			},
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(basicCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompt text when HidePrompts is true.
			},
		},
		{
			name: "hide invocation parameters",
			config: &openinference.TraceConfig{
				HideLLMInvocationParameters: true,
			},
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(basicCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				// No LLMInvocationParameters when hidden.
				attribute.String(openinference.PromptTextAttribute(0), "Say this is a test"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCompletionRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
		})
	}
}

func TestCompletionRecorder_WithConfig_HideOutputs(t *testing.T) {
	tests := []struct {
		name           string
		config         *openinference.TraceConfig
		resp           *openai.CompletionResponse
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name: "hide output value",
			config: &openinference.TraceConfig{
				HideOutputs: true,
			},
			resp: basicCompletionResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				// No OutputMimeType when output is hidden.
				// No choices when HideOutputs is true.
				// Token counts are still included as metadata.
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountCompletion, 4),
				attribute.Int(openinference.LLMTokenCountTotal, 9),
				// output.value is checked separately and filtered out in this test
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name: "hide choices only",
			config: &openinference.TraceConfig{
				HideChoices: true,
			},
			resp: basicCompletionResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				// No choice text when HideChoices is true.
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountCompletion, 4),
				attribute.Int(openinference.LLMTokenCountTotal, 9),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:   "show all by default",
			config: &openinference.TraceConfig{},
			resp:   basicCompletionResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.ChoiceTextAttribute(0), "This is a test"),
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountCompletion, 4),
				attribute.Int(openinference.LLMTokenCountTotal, 9),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCompletionRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			// Extract just the response attributes (skip output.value).
			var responseAttrs []attribute.KeyValue
			for _, attr := range actualSpan.Attributes {
				if string(attr.Key) != openinference.OutputValue {
					responseAttrs = append(responseAttrs, attr)
				}
			}

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, responseAttrs)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestCompletionRecorder_WithConfig_NoJSONMarshalWhenHidden(t *testing.T) {
	// Test that we don't do unnecessary work when attributes are hidden.
	config := &openinference.TraceConfig{
		HideLLMInvocationParameters: true,
		HideInputs:                  true,
		HideOutputs:                 true,
	}

	recorder := NewCompletionRecorder(config)

	// Create a request.
	req := &openai.CompletionRequest{
		Model:  "gpt-3.5-turbo-instruct",
		Prompt: openai.PromptUnion{Value: "Test prompt"},
	}

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		// This should not panic or error even though invocation params are hidden.
		recorder.RecordRequest(span, req, []byte(`{"model":"test"}`))
		return false
	})

	// Verify minimal attributes are set.
	expectedAttrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
		attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
		attribute.String(openinference.InputValue, openinference.RedactedValue),
		// No InputMimeType, no invocation params, no prompts.
	}

	openinference.RequireAttributesEqual(t, expectedAttrs, actualSpan.Attributes)
}

func TestCompletionRecorder_ConfigFromEnvironment(t *testing.T) {
	// Test that recorder uses environment variables when config is nil.
	t.Setenv(openinference.EnvHideInputs, "true")
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewCompletionRecorderFromEnv()

	// Request test.
	reqSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, basicCompletionReq, basicCompletionReqBody)
		return false
	})

	// Verify input is hidden.
	attrs := make(map[string]attribute.Value)
	for _, kv := range reqSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue].AsString())

	// Response test.
	respSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicCompletionResp)
		return false
	})

	// Verify output is hidden.
	attrs = make(map[string]attribute.Value)
	for _, kv := range respSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue].AsString())
}

func TestCompletionRecorder_WithConfig_Streaming(t *testing.T) {
	config := &openinference.TraceConfig{
		HideOutputs: true,
	}

	recorder := NewCompletionRecorder(config)

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseChunks(span, streamChunks)
		return false
	})

	// Verify output is hidden.
	attrs := make(map[string]attribute.Value)
	for _, kv := range actualSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue].AsString())
	// Model name should still be present.
	require.Equal(t, "gpt-3.5-turbo-instruct", attrs[openinference.LLMModelName].AsString())
}

func TestCompletionRecorder_WithConfig_MultiplePrompts(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		req           *openai.CompletionRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "array of prompts",
			config:  &openinference.TraceConfig{},
			req:     arrayCompletionReq,
			reqBody: arrayCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(arrayCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				attribute.String(openinference.PromptTextAttribute(0), "Say this is a test"),
				attribute.String(openinference.PromptTextAttribute(1), "Say hello"),
			},
		},
		{
			name: "array of prompts with HidePrompts",
			config: &openinference.TraceConfig{
				HidePrompts: true,
			},
			req:     arrayCompletionReq,
			reqBody: arrayCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(arrayCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompt text when HidePrompts is true.
			},
		},
		{
			name:    "token array prompts not recorded",
			config:  &openinference.TraceConfig{},
			req:     tokenCompletionReq,
			reqBody: tokenCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(tokenCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompt text for token arrays (per OpenInference guidance).
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCompletionRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
		})
	}
}

func TestCompletionRecorder_WithConfig_MultipleChoices(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		resp          *openai.CompletionResponse
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "multiple choices",
			config: &openinference.TraceConfig{},
			resp:   multiChoiceCompletionResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.ChoiceTextAttribute(0), "First choice"),
				attribute.String(openinference.ChoiceTextAttribute(1), "Second choice"),
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountCompletion, 6),
				attribute.Int(openinference.LLMTokenCountTotal, 11),
			},
		},
		{
			name: "multiple choices with HideChoices",
			config: &openinference.TraceConfig{
				HideChoices: true,
			},
			resp: multiChoiceCompletionResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				// No choice text when HideChoices is true.
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountCompletion, 6),
				attribute.Int(openinference.LLMTokenCountTotal, 11),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCompletionRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			// Extract just the response attributes (skip output.value).
			var responseAttrs []attribute.KeyValue
			for _, attr := range actualSpan.Attributes {
				if string(attr.Key) != openinference.OutputValue {
					responseAttrs = append(responseAttrs, attr)
				}
			}

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, responseAttrs)
		})
	}
}

func TestCompletionRecorder_WithConfig_HideChoicesEnvVar(t *testing.T) {
	// Test OPENINFERENCE_HIDE_CHOICES environment variable.
	t.Setenv(openinference.EnvHideChoices, "true")

	recorder := NewCompletionRecorderFromEnv()

	respSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicCompletionResp)
		return false
	})

	// Verify choices are hidden.
	attrs := make(map[string]attribute.Value)
	for _, kv := range respSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	// Should not have choice text attributes.
	_, hasChoice := attrs[openinference.ChoiceTextAttribute(0)]
	require.False(t, hasChoice, "choice text should be hidden when OPENINFERENCE_HIDE_CHOICES=true")
	// Should still have model name.
	require.Equal(t, "gpt-3.5-turbo-instruct", attrs[openinference.LLMModelName].AsString())
}

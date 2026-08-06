// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package otelgenai

import (
	"testing"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestChatToolDefinitions(t *testing.T) {
	tests := []struct {
		name     string
		req      *openai.ChatCompletionRequest
		expected string
	}{
		{name: "no tools", req: &openai.ChatCompletionRequest{}, expected: ""},
		{
			name: "function tool",
			req: &openai.ChatCompletionRequest{Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "get_weather",
					Description: "Look up the weather",
					Parameters:  map[string]any{"type": "object"},
				},
			}}},
			expected: `[{"type":"function","name":"get_weather","description":"Look up the weather",` +
				`"parameters":{"type":"object"}}]`,
		},
		{
			// Provider-native tools have no function block; the type alone is
			// still worth recording.
			name: "provider tool without a function block",
			req: &openai.ChatCompletionRequest{Tools: []openai.Tool{{
				Type: openai.ToolType("google_search"),
			}}},
			expected: `[{"type":"google_search","name":""}]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attrs := toolDefinitionsAttr(chatToolDefinitions(tc.req))
			if tc.expected == "" {
				require.Empty(t, attrs)
				return
			}
			require.Len(t, attrs, 1)
			require.JSONEq(t, tc.expected, attrs[0].Value.AsString())
		})
	}
}

func TestAnthropicToolDefinitions(t *testing.T) {
	defs := anthropicToolDefinitions(&anthropicschema.MessagesRequest{
		Tools: []anthropicschema.ToolUnion{
			{Tool: &anthropicschema.Tool{
				Type:        "custom",
				Name:        "get_weather",
				Description: "Look up the weather",
			}},
			{BashTool: &anthropicschema.BashTool{Type: "bash_20250124", Name: "bash"}},
		},
	})

	require.Len(t, defs, 2)
	require.Equal(t, "get_weather", defs[0].Name)
	require.Equal(t, "Look up the weather", defs[0].Description)
	require.Equal(t, "bash", defs[1].Name)
	require.Equal(t, "bash_20250124", defs[1].Type)
}

// TestToolDefinitions_gatedByCapture pins that tool schemas are treated as
// content: they are authored by the caller and can carry proprietary detail.
func TestToolDefinitions_gatedByCapture(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "gpt-5-nano",
		Tools: []openai.Tool{{
			Type:     openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: "internal_tool"},
		}},
	}

	for _, capture := range []bool{false, true} {
		t.Run(t.Name(), func(t *testing.T) {
			r := NewChatCompletionRecorder(&Config{CaptureMessageContent: capture})
			span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				r.RecordRequest(span, req, nil)
				return false
			})
			require.Equal(t, capture, hasAttr(span.Attributes, ToolDefinitions))
		})
	}
}

func TestCompletionMessages(t *testing.T) {
	tests := []struct {
		name     string
		prompt   any
		expected string
	}{
		{name: "empty", prompt: "", expected: ""},
		{
			name:     "single string becomes a user message",
			prompt:   "once upon a time",
			expected: `[{"role":"user","parts":[{"type":"text","content":"once upon a time"}]}]`,
		},
		{
			name:   "string list becomes one message each",
			prompt: []string{"a", "b"},
			expected: `[{"role":"user","parts":[{"type":"text","content":"a"}]},` +
				`{"role":"user","parts":[{"type":"text","content":"b"}]}]`,
		},
		{
			// Pre-tokenized prompts are described by length rather than decoded,
			// because the gateway has no tokenizer for the target model.
			name:     "token array reports its length",
			prompt:   []int64{1, 2, 3},
			expected: `[{"role":"user","parts":[{"type":"text","content":"<3 tokens>"}]}]`,
		},
		{name: "unsupported type", prompt: 42, expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &openai.CompletionRequest{Prompt: openai.PromptUnion{Value: tc.prompt}}
			attrs := messagesAttr(InputMessages, completionInputMessages(req))
			if tc.expected == "" {
				require.Empty(t, attrs)
				return
			}
			require.Len(t, attrs, 1)
			require.JSONEq(t, tc.expected, attrs[0].Value.AsString())
		})
	}
}

func TestCompletionOutputMessages(t *testing.T) {
	attrs := messagesAttr(OutputMessages, completionOutputMessages(&openai.CompletionResponse{
		Choices: []openai.CompletionChoice{{Text: "the end", FinishReason: "stop"}},
	}))
	require.Len(t, attrs, 1)
	require.JSONEq(t,
		`[{"role":"assistant","parts":[{"type":"text","content":"the end"}],"finish_reason":"stop"}]`,
		attrs[0].Value.AsString())
}

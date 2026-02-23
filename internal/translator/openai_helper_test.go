// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// sseEvent holds parsed Anthropic SSE event data.
type sseEvent struct {
	eventType string
	data      string
}

// parseSSEEventsFromBytes parses raw Anthropic SSE output into individual events.
func parseSSEEventsFromBytes(output []byte) []sseEvent {
	var events []sseEvent
	for block := range bytes.SplitSeq(output, []byte("\n\n")) {
		block = bytes.TrimSpace(block)
		if len(block) == 0 {
			continue
		}
		var e sseEvent
		for line := range bytes.SplitSeq(block, []byte("\n")) {
			if after, ok := bytes.CutPrefix(line, []byte("event: ")); ok {
				e.eventType = string(after)
			} else if after, ok := bytes.CutPrefix(line, []byte("data: ")); ok {
				e.data = string(after)
			}
		}
		if e.eventType != "" || e.data != "" {
			events = append(events, e)
		}
	}
	return events
}

func TestBuildOpenAIChatCompletionRequest(t *testing.T) {
	t.Run("basic model and message", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:     "claude-3-haiku",
			MaxTokens: 100,
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hello"}},
			},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		assert.Equal(t, "claude-3-haiku", req.Model)
		require.NotNil(t, req.MaxCompletionTokens)
		assert.Equal(t, int64(100), *req.MaxCompletionTokens)
		require.Len(t, req.Messages, 1)
		require.NotNil(t, req.Messages[0].OfUser)
		assert.Equal(t, openai.ChatMessageRoleUser, req.Messages[0].OfUser.Role)
		assert.Equal(t, "Hello", req.Messages[0].OfUser.Content.Value)
	})

	t.Run("model name override", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:    "claude-3-haiku",
			Messages: []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
		}
		req := buildOpenAIChatCompletionRequest(body, "gpt-4o")
		assert.Equal(t, "gpt-4o", req.Model)
	})

	t.Run("system prompt prepended as first message", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:  "claude-3",
			System: &anthropic.SystemPrompt{Text: "You are a helpful assistant."},
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}},
			},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.Len(t, req.Messages, 2)
		require.NotNil(t, req.Messages[0].OfSystem)
		assert.Equal(t, openai.ChatMessageRoleSystem, req.Messages[0].OfSystem.Role)
		assert.Equal(t, "You are a helpful assistant.", req.Messages[0].OfSystem.Content.Value)
		require.NotNil(t, req.Messages[1].OfUser)
	})

	t.Run("empty system prompt not prepended", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:  "claude-3",
			System: &anthropic.SystemPrompt{},
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}},
			},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.Len(t, req.Messages, 1)
		assert.Nil(t, req.Messages[0].OfSystem)
	})

	t.Run("multi-turn conversation", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model: "claude-3",
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}},
				{Role: anthropic.MessageRoleAssistant, Content: anthropic.MessageContent{Text: "Hello!"}},
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Bye"}},
			},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.Len(t, req.Messages, 3)
		assert.NotNil(t, req.Messages[0].OfUser)
		assert.NotNil(t, req.Messages[1].OfAssistant)
		assert.Equal(t, openai.ChatMessageRoleAssistant, req.Messages[1].OfAssistant.Role)
		assert.Equal(t, "Hello!", req.Messages[1].OfAssistant.Content.Value)
		assert.NotNil(t, req.Messages[2].OfUser)
	})

	t.Run("streaming sets stream_options", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:    "claude-3",
			Stream:   true,
			Messages: []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		assert.True(t, req.Stream)
		require.NotNil(t, req.StreamOptions)
		assert.True(t, req.StreamOptions.IncludeUsage)
	})

	t.Run("non-streaming has no stream_options", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:    "claude-3",
			Messages: []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		assert.False(t, req.Stream)
		assert.Nil(t, req.StreamOptions)
	})

	t.Run("temperature and topP passthrough", func(t *testing.T) {
		temp := 0.7
		topP := 0.95
		body := &anthropic.MessagesRequest{
			Model:       "claude-3",
			Temperature: &temp,
			TopP:        &topP,
			Messages:    []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.NotNil(t, req.Temperature)
		assert.Equal(t, &temp, req.Temperature)
		require.NotNil(t, req.TopP)
		assert.Equal(t, &topP, req.TopP)
	})

	t.Run("tools conversion", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model: "claude-3",
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Get weather"}},
			},
			Tools: []anthropic.Tool{
				{
					Name:        "get_weather",
					Description: "Retrieve current weather information",
					InputSchema: anthropic.ToolInputSchema{
						Type:     "object",
						Required: []string{"location"},
					},
				},
			},
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.Len(t, req.Tools, 1)
		assert.Equal(t, openai.ToolTypeFunction, req.Tools[0].Type)
		require.NotNil(t, req.Tools[0].Function)
		assert.Equal(t, "get_weather", req.Tools[0].Function.Name)
		assert.Equal(t, "Retrieve current weather information", req.Tools[0].Function.Description)
	})

	t.Run("no tools means tool choice not set even if body has it", func(t *testing.T) {
		tc := anthropic.ToolChoice(map[string]any{"type": "auto"})
		body := &anthropic.MessagesRequest{
			Model:      "claude-3",
			Messages:   []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
			ToolChoice: &tc,
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		assert.Nil(t, req.Tools)
		assert.Nil(t, req.ToolChoice)
	})

	t.Run("tools with auto tool_choice", func(t *testing.T) {
		body := &anthropic.MessagesRequest{
			Model:    "claude-3",
			Messages: []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Hi"}}},
			Tools:    []anthropic.Tool{{Name: "search", InputSchema: anthropic.ToolInputSchema{Type: "object"}}},
			ToolChoice: ptr.To(anthropic.ToolChoice(map[string]any{
				"type": "auto",
			})),
		}
		req := buildOpenAIChatCompletionRequest(body, "")
		require.NotNil(t, req.Tools)
		require.NotNil(t, req.ToolChoice)
		assert.Equal(t, string(openai.ToolChoiceTypeAuto), req.ToolChoice.Value)
	})
}

func TestAnthropicSystemPromptToText(t *testing.T) {
	tests := []struct {
		name     string
		system   *anthropic.SystemPrompt
		expected string
	}{
		{
			name:     "plain text",
			system:   &anthropic.SystemPrompt{Text: "You are helpful."},
			expected: "You are helpful.",
		},
		{
			name: "array of text blocks concatenated",
			system: &anthropic.SystemPrompt{
				Texts: []anthropic.TextBlockParam{
					{Text: "You are "},
					{Text: "very helpful."},
				},
			},
			expected: "You are very helpful.",
		},
		{
			name:     "empty system prompt",
			system:   &anthropic.SystemPrompt{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, anthropicSystemPromptToText(tt.system))
		})
	}
}

func TestAnthropicContentToText(t *testing.T) {
	tests := []struct {
		name     string
		content  anthropic.MessageContent
		expected string
	}{
		{
			name:     "plain text field",
			content:  anthropic.MessageContent{Text: "Hello!"},
			expected: "Hello!",
		},
		{
			name: "array of text blocks",
			content: anthropic.MessageContent{
				Array: []anthropic.ContentBlockParam{
					{Text: &anthropic.TextBlockParam{Text: "Hello "}},
					{Text: &anthropic.TextBlockParam{Text: "world"}},
				},
			},
			expected: "Hello world",
		},
		{
			name: "array with nil text block is skipped",
			content: anthropic.MessageContent{
				Array: []anthropic.ContentBlockParam{
					{Text: nil},
					{Text: &anthropic.TextBlockParam{Text: "world"}},
				},
			},
			expected: "world",
		},
		{
			name:     "empty content",
			content:  anthropic.MessageContent{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, anthropicContentToText(tt.content))
		})
	}
}

func TestAnthropicToolsToOpenAI(t *testing.T) {
	t.Run("nil tools returns nil", func(t *testing.T) {
		assert.Nil(t, anthropicToolsToOpenAI(nil))
	})

	t.Run("empty tools returns nil", func(t *testing.T) {
		assert.Nil(t, anthropicToolsToOpenAI([]anthropic.Tool{}))
	})

	t.Run("single tool converted correctly", func(t *testing.T) {
		tools := []anthropic.Tool{
			{
				Name:        "search",
				Description: "Search the web",
				InputSchema: anthropic.ToolInputSchema{
					Type:       "object",
					Properties: map[string]any{"query": map[string]any{"type": "string"}},
					Required:   []string{"query"},
				},
			},
		}
		result := anthropicToolsToOpenAI(tools)
		require.Len(t, result, 1)
		assert.Equal(t, openai.ToolTypeFunction, result[0].Type)
		require.NotNil(t, result[0].Function)
		assert.Equal(t, "search", result[0].Function.Name)
		assert.Equal(t, "Search the web", result[0].Function.Description)
	})

	t.Run("multiple tools all converted", func(t *testing.T) {
		tools := []anthropic.Tool{
			{Name: "tool1", InputSchema: anthropic.ToolInputSchema{Type: "object"}},
			{Name: "tool2", InputSchema: anthropic.ToolInputSchema{Type: "object"}},
		}
		result := anthropicToolsToOpenAI(tools)
		require.Len(t, result, 2)
		assert.Equal(t, "tool1", result[0].Function.Name)
		assert.Equal(t, "tool2", result[1].Function.Name)
	})
}

func TestAnthropicToolChoiceToOpenAI(t *testing.T) {
	tests := []struct {
		name      string
		tc        anthropic.ToolChoice
		hasTools  bool
		expectNil bool
		expectVal any
	}{
		{
			name:      "nil tool choice returns nil",
			tc:        nil,
			hasTools:  true,
			expectNil: true,
		},
		{
			name:      "no tools returns nil even with valid choice",
			tc:        map[string]any{"type": "auto"},
			hasTools:  false,
			expectNil: true,
		},
		{
			name:      "auto maps to auto",
			tc:        map[string]any{"type": "auto"},
			hasTools:  true,
			expectVal: string(openai.ToolChoiceTypeAuto),
		},
		{
			name:      "none maps to none",
			tc:        map[string]any{"type": "none"},
			hasTools:  true,
			expectVal: string(openai.ToolChoiceTypeNone),
		},
		{
			name:      "any maps to required",
			tc:        map[string]any{"type": "any"},
			hasTools:  true,
			expectVal: string(openai.ToolChoiceTypeRequired),
		},
		{
			name:     "tool type with name maps to named tool choice",
			tc:       map[string]any{"type": "tool", "name": "search"},
			hasTools: true,
			expectVal: openai.ChatCompletionNamedToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ChatCompletionNamedToolChoiceFunction{Name: "search"},
			},
		},
		{
			name:      "unknown type returns nil",
			tc:        map[string]any{"type": "unknown_type"},
			hasTools:  true,
			expectNil: true,
		},
		{
			name:      "non-map type returns nil",
			tc:        "auto",
			hasTools:  true,
			expectNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anthropicToolChoiceToOpenAI(tt.tc, tt.hasTools)
			if tt.expectNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			assert.Equal(t, tt.expectVal, result.Value)
		})
	}
}

func TestOpenAIResponseToAnthropic(t *testing.T) {
	t.Run("text content response", func(t *testing.T) {
		content := "Hello from the model!"
		resp := &openai.ChatCompletionResponse{
			ID:    "chatcmpl-123",
			Model: "gpt-4o",
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					Message: openai.ChatCompletionResponseChoiceMessage{
						Content: &content,
						Role:    "assistant",
					},
				},
			},
			Usage: openai.Usage{PromptTokens: 10, CompletionTokens: 20},
		}
		result := openAIResponseToAnthropic(resp, "gpt-4o")
		assert.Equal(t, "chatcmpl-123", result.ID)
		assert.Equal(t, "gpt-4o", result.Model)
		require.Len(t, result.Content, 1)
		require.NotNil(t, result.Content[0].Text)
		assert.Equal(t, "Hello from the model!", result.Content[0].Text.Text)
		require.NotNil(t, result.StopReason)
		assert.Equal(t, anthropic.StopReasonEndTurn, *result.StopReason)
		require.NotNil(t, result.Usage)
		assert.Equal(t, float64(10), result.Usage.InputTokens)
		assert.Equal(t, float64(20), result.Usage.OutputTokens)
	})

	t.Run("empty string content not added to blocks", func(t *testing.T) {
		empty := ""
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					Message:      openai.ChatCompletionResponseChoiceMessage{Content: &empty},
				},
			},
		}
		result := openAIResponseToAnthropic(resp, "test-model")
		assert.Nil(t, result.Content)
	})

	t.Run("nil content not added to blocks", func(t *testing.T) {
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					Message:      openai.ChatCompletionResponseChoiceMessage{Content: nil},
				},
			},
		}
		result := openAIResponseToAnthropic(resp, "test-model")
		assert.Nil(t, result.Content)
	})

	t.Run("tool call response", func(t *testing.T) {
		callID := "call-abc"
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
					Message: openai.ChatCompletionResponseChoiceMessage{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID: &callID,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "get_weather",
									Arguments: `{"location":"NYC"}`,
								},
							},
						},
					},
				},
			},
		}
		result := openAIResponseToAnthropic(resp, "test-model")
		require.Len(t, result.Content, 1)
		require.NotNil(t, result.Content[0].Tool)
		assert.Equal(t, "call-abc", result.Content[0].Tool.ID)
		assert.Equal(t, "get_weather", result.Content[0].Tool.Name)
		assert.Equal(t, map[string]any{"location": "NYC"}, result.Content[0].Tool.Input)
		require.NotNil(t, result.StopReason)
		assert.Equal(t, anthropic.StopReasonToolUse, *result.StopReason)
	})

	t.Run("malformed tool call arguments becomes empty map", func(t *testing.T) {
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
					Message: openai.ChatCompletionResponseChoiceMessage{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "broken_tool",
									Arguments: `{invalid json`,
								},
							},
						},
					},
				},
			},
		}
		result := openAIResponseToAnthropic(resp, "test-model")
		require.Len(t, result.Content, 1)
		require.NotNil(t, result.Content[0].Tool)
		assert.Equal(t, map[string]any{}, result.Content[0].Tool.Input)
	})

	t.Run("missing tool call ID becomes empty string", func(t *testing.T) {
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
					Message: openai.ChatCompletionResponseChoiceMessage{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID: nil,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "my_tool",
									Arguments: `{}`,
								},
							},
						},
					},
				},
			},
		}
		result := openAIResponseToAnthropic(resp, "test-model")
		require.Len(t, result.Content, 1)
		assert.Empty(t, result.Content[0].Tool.ID)
	})

	t.Run("no choices produces no content and no stop reason", func(t *testing.T) {
		resp := &openai.ChatCompletionResponse{
			ID:    "chatcmpl-empty",
			Model: "gpt-4o",
			Usage: openai.Usage{PromptTokens: 5},
		}
		result := openAIResponseToAnthropic(resp, "gpt-4o")
		assert.Nil(t, result.Content)
		assert.Nil(t, result.StopReason)
		assert.Equal(t, float64(5), result.Usage.InputTokens)
	})
}

func TestOpenAIFinishReasonToAnthropic(t *testing.T) {
	tests := []struct {
		reason   openai.ChatCompletionChoicesFinishReason
		expected anthropic.StopReason
	}{
		{openai.ChatCompletionChoicesFinishReasonStop, anthropic.StopReasonEndTurn},
		{openai.ChatCompletionChoicesFinishReasonLength, anthropic.StopReasonMaxTokens},
		{openai.ChatCompletionChoicesFinishReasonToolCalls, anthropic.StopReasonToolUse},
		{openai.ChatCompletionChoicesFinishReasonContentFilter, anthropic.StopReasonRefusal},
		{"function_call", anthropic.StopReasonEndTurn},
		{"", anthropic.StopReasonEndTurn},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			assert.Equal(t, tt.expected, openAIFinishReasonToAnthropic(tt.reason))
		})
	}
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_TextStreaming(t *testing.T) {
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "claude-3",
	}

	// OpenAI SSE input: text streaming with usage chunk and [DONE].
	// Chunk 1: first delta with text → emits message_start, content_block_start, content_block_delta
	// Chunk 2: finish_reason → stores stop reason
	// Chunk 3: usage-only → emits content_block_stop, message_delta, message_stop
	// [DONE]: skipped
	input := "data: {\"id\":\"chatcmpl-abc\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}],\"model\":\"gpt-4o\"}\n\n" +
		"data: {\"id\":\"chatcmpl-abc\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-abc\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-abc\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"

	state.buffer.WriteString(input)

	var out []byte
	err := state.processBuffer(&out, true)
	require.NoError(t, err)

	events := parseSSEEventsFromBytes(out)
	// 7 events: message_start, content_block_start, content_block_delta x2, content_block_stop, message_delta, message_stop
	require.Len(t, events, 7)

	assert.Equal(t, "message_start", events[0].eventType)
	require.JSONEq(t, `{
		"type":"message_start",
		"message":{
			"id":"chatcmpl-abc",
			"type":"message",
			"role":"assistant",
			"content":[],
			"model":"gpt-4o",
			"stop_reason":null,
			"stop_sequence":null,
			"usage":{"input_tokens":0,"output_tokens":0}
		}
	}`, events[0].data)

	assert.Equal(t, "content_block_start", events[1].eventType)
	require.JSONEq(t, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, events[1].data)

	assert.Equal(t, "content_block_delta", events[2].eventType)
	require.JSONEq(t, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`, events[2].data)

	assert.Equal(t, "content_block_delta", events[3].eventType)
	require.JSONEq(t, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`, events[3].data)

	assert.Equal(t, "content_block_stop", events[4].eventType)
	require.JSONEq(t, `{"type":"content_block_stop","index":0}`, events[4].data)

	assert.Equal(t, "message_delta", events[5].eventType)
	require.JSONEq(t, `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`, events[5].data)

	assert.Equal(t, "message_stop", events[6].eventType)
	require.JSONEq(t, `{"type":"message_stop"}`, events[6].data)
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_ToolCallStreaming(t *testing.T) {
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "claude-3",
	}

	// Chunk 1: new tool call start → emits message_start, content_block_start (tool_use)
	// Chunk 2: tool argument delta → emits content_block_delta (input_json_delta)
	// Chunk 3: finish_reason=tool_calls → stores stop reason
	// Chunk 4: usage → emits content_block_stop, message_delta (stop_reason=tool_use), message_stop
	input := "data: {\"id\":\"chatcmpl-def\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-xyz\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"},\"type\":\"function\"}]}}],\"model\":\"gpt-4o\"}\n\n" +
		"data: {\"id\":\"chatcmpl-def\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":null,\"function\":{\"name\":\"\",\"arguments\":\"{\\\"city\\\":\\\"NYC\\\"}\"}}]}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-def\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-def\",\"choices\":[],\"usage\":{\"prompt_tokens\":15,\"completion_tokens\":10}}\n\n" +
		"data: [DONE]\n\n"

	state.buffer.WriteString(input)

	var out []byte
	err := state.processBuffer(&out, true)
	require.NoError(t, err)

	events := parseSSEEventsFromBytes(out)
	// 6 events: message_start, content_block_start, content_block_delta, content_block_stop, message_delta, message_stop
	require.Len(t, events, 6)

	assert.Equal(t, "message_start", events[0].eventType)

	assert.Equal(t, "content_block_start", events[1].eventType)
	require.JSONEq(t, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-xyz","name":"get_weather","input":{}}}`, events[1].data)

	assert.Equal(t, "content_block_delta", events[2].eventType)
	require.JSONEq(t, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"NYC\"}"}}`, events[2].data)

	assert.Equal(t, "content_block_stop", events[3].eventType)
	require.JSONEq(t, `{"type":"content_block_stop","index":0}`, events[3].data)

	assert.Equal(t, "message_delta", events[4].eventType)
	require.JSONEq(t, `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":10}}`, events[4].data)

	assert.Equal(t, "message_stop", events[5].eventType)
	require.JSONEq(t, `{"type":"message_stop"}`, events[5].data)
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_EndOfStreamClosing(t *testing.T) {
	// Verify endOfStream triggers closing events when no usage chunk is present.
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "test-model",
	}

	input := "data: {\"id\":\"chatcmpl-eos\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	state.buffer.WriteString(input)

	var out []byte
	err := state.processBuffer(&out, true)
	require.NoError(t, err)

	events := parseSSEEventsFromBytes(out)
	require.GreaterOrEqual(t, len(events), 4)

	// Last event must be message_stop.
	assert.Equal(t, "message_stop", events[len(events)-1].eventType)

	// Find message_delta and verify stop_reason defaults to end_turn.
	var msgDeltaData string
	for _, e := range events {
		if e.eventType == "message_delta" {
			msgDeltaData = e.data
			break
		}
	}
	require.NotEmpty(t, msgDeltaData)
	require.JSONEq(t, `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":0}}`, msgDeltaData)
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_EmptyInput(t *testing.T) {
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "test-model",
	}

	var out []byte
	err := state.processBuffer(&out, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_SkipsDoneMarker(t *testing.T) {
	// Ensure [DONE] marker does not cause errors or spurious events.
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "test-model",
	}

	input := "data: [DONE]\n\n"
	state.buffer.WriteString(input)

	var out []byte
	err := state.processBuffer(&out, false)
	require.NoError(t, err)
	// No events should be emitted for just [DONE].
	assert.Empty(t, out)
}

func TestOpenAIStreamToAnthropicState_ProcessBuffer_MalformedChunkSkipped(t *testing.T) {
	state := &openAIStreamToAnthropicState{
		activeTools:  make(map[int64]*streamToolCall),
		requestModel: "test-model",
	}

	// A malformed JSON chunk should be silently skipped, no error.
	input := "data: {not valid json}\n\n"
	state.buffer.WriteString(input)

	var out []byte
	err := state.processBuffer(&out, false)
	require.NoError(t, err)
}

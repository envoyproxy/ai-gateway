// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func anthropicToolRequest(model string, stream bool) *anthropic.MessagesRequest {
	return &anthropic.MessagesRequest{
		Model: model, MaxTokens: 128, Stream: stream,
		Messages: []anthropic.MessageParam{{
			Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Get the weather"},
		}},
		Tools: []anthropic.ToolUnion{{Tool: &anthropic.Tool{
			Name: "get_weather", Description: "Get weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}}},
		ToolChoice: &anthropic.ToolChoice{Tool: &anthropic.ToolChoiceTool{Type: "tool", Name: "get_weather"}},
	}
}

func TestAnthropicToOpenAITranslator_Dispatch(t *testing.T) {
	tests := []struct {
		name, model, expectedPath string
		withTools                 bool
	}{
		{name: "GPT-5.6 tools use Responses", model: "gpt-5.6-terra", withTools: true, expectedPath: "/v1/responses"},
		{name: "GPT-5.6 text retains Chat Completions", model: "gpt-5.6-terra", expectedPath: "/v1/chat/completions"},
		{name: "older tool model retains Chat Completions", model: "gpt-5.5", withTools: true, expectedPath: "/v1/chat/completions"},
		{name: "model override controls dispatch", model: "virtual-model", withTools: true, expectedPath: "/v1/responses"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			override := ""
			if tt.name == "model override controls dispatch" {
				override = "gpt-5.6-sol"
			}
			translator := NewAnthropicToOpenAITranslator("v1", override)
			request := &anthropic.MessagesRequest{
				Model: tt.model, MaxTokens: 32,
				Messages: []anthropic.MessageParam{{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "hello"}}},
			}
			if tt.withTools {
				request.Tools = anthropicToolRequest(tt.model, false).Tools
			}
			headers, _, err := translator.RequestBody(nil, request, false)
			require.NoError(t, err)
			require.NotEmpty(t, headers)
			assert.Equal(t, tt.expectedPath, headers[0].Value())
		})
	}
}

func TestAnthropicToResponsesOpenAITranslator_RequestBody(t *testing.T) {
	request := anthropicToolRequest("gpt-5.6-terra", false)
	disableParallel := true
	request.ToolChoice.Tool.DisableParallelToolUse = &disableParallel
	request.Messages = append(request.Messages,
		anthropic.MessageParam{
			Role: anthropic.MessageRoleAssistant,
			Content: anthropic.MessageContent{Array: []anthropic.ContentBlockParam{{
				ToolUse: &anthropic.ToolUseBlockParam{
					Type: "tool_use", ID: "call_123", Name: "get_weather", Input: map[string]any{"city": "Seattle"},
				},
			}}},
		},
		anthropic.MessageParam{
			Role: anthropic.MessageRoleUser,
			Content: anthropic.MessageContent{Array: []anthropic.ContentBlockParam{{
				ToolResult: &anthropic.ToolResultBlockParam{
					Type: "tool_result", ToolUseID: "call_123", Content: &anthropic.ToolResultContent{Text: "rainy"},
				},
			}}},
		},
	)

	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	headers, body, err := translator.RequestBody(nil, request, false)
	require.NoError(t, err)
	require.Len(t, headers, 2)
	assert.Equal(t, "/v1/responses", headers[0].Value())

	var translated map[string]any
	require.NoError(t, json.Unmarshal(body, &translated))
	assert.Equal(t, "gpt-5.6-terra", translated["model"])
	assert.Equal(t, float64(128), translated["max_output_tokens"])
	assert.Equal(t, false, translated["store"])
	assert.Equal(t, false, translated["parallel_tool_calls"])

	tools := translated["tools"].([]any)
	require.Len(t, tools, 1)
	assert.Equal(t, map[string]any{
		"type": "function", "name": "get_weather", "description": "Get weather by city",
		"parameters": map[string]any{
			"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required": []any{"city"},
		},
	}, tools[0])
	assert.Equal(t, map[string]any{"type": "function", "name": "get_weather"}, translated["tool_choice"])

	input := translated["input"].([]any)
	require.Len(t, input, 3)
	assert.Equal(t, "function_call", input[1].(map[string]any)["type"])
	assert.Equal(t, "call_123", input[1].(map[string]any)["call_id"])
	assert.Equal(t, "function_call_output", input[2].(map[string]any)["type"])
	assert.Equal(t, "rainy", input[2].(map[string]any)["output"])
}

func TestAnthropicToResponsesOpenAITranslator_ResponseBody(t *testing.T) {
	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, _, err := translator.RequestBody(nil, anthropicToolRequest("gpt-5.6-terra", false), false)
	require.NoError(t, err)

	response := `{
		"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"completed",
		"output":[
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'll check."}]},
			{"id":"fc_1","type":"function_call","call_id":"call_123","name":"get_weather","arguments":"{\"city\":\"Seattle\"}","status":"completed"}
		],
		"usage":{"input_tokens":42,"input_tokens_details":{"cached_tokens":10,"cache_creation_input_tokens":2},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":49}
	}`
	headers, body, usage, model, err := translator.ResponseBody(nil, strings.NewReader(response), true, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.6-terra", model)
	require.Len(t, headers, 1)

	var translated anthropic.MessagesResponse
	require.NoError(t, json.Unmarshal(body, &translated))
	assert.Equal(t, "resp_123", translated.ID)
	require.Len(t, translated.Content, 2)
	assert.Equal(t, "I'll check.", translated.Content[0].Text.Text)
	assert.Equal(t, "call_123", translated.Content[1].Tool.ID)
	assert.Equal(t, map[string]any{"city": "Seattle"}, translated.Content[1].Tool.Input)
	assert.Equal(t, anthropic.StopReasonToolUse, *translated.StopReason)
	assert.Equal(t, float64(10), translated.Usage.CacheReadInputTokens)
	assert.Equal(t, float64(2), translated.Usage.CacheCreationInputTokens)
	reasoning, ok := usage.ReasoningTokens()
	assert.True(t, ok)
	assert.Equal(t, uint32(3), reasoning)
}

func TestAnthropicToResponsesOpenAITranslator_ResponseBodyStreamingTool(t *testing.T) {
	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, _, err := translator.RequestBody(nil, anthropicToolRequest("gpt-5.6-terra", true), false)
	require.NoError(t, err)

	stream := strings.Join([]string{
		`event: response.created\ndata: {"type":"response.created","sequence_number":0,"response":{"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"in_progress"}}`,
		`event: response.output_item.added\ndata: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_123","name":"get_weather","arguments":"","status":"in_progress"}}`,
		`event: response.function_call_arguments.delta\ndata: {"type":"response.function_call_arguments.delta","sequence_number":2,"item_id":"fc_1","output_index":0,"delta":"{\"city\":\"Seattle\"}"}`,
		`event: response.function_call_arguments.done\ndata: {"type":"response.function_call_arguments.done","sequence_number":3,"item_id":"fc_1","output_index":0,"name":"get_weather","arguments":"{\"city\":\"Seattle\"}"}`,
		`event: response.completed\ndata: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"completed","output":[],"usage":{"input_tokens":42,"input_tokens_details":{"cached_tokens":5},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":49}}}`,
	}, "\n\n") + "\n\n"
	stream = strings.ReplaceAll(stream, `\n`, "\n")

	_, body, usage, model, err := translator.ResponseBody(nil, bytes.NewBufferString(stream), true, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.6-terra", model)
	events := parseSSEEventsFromBytes(body)
	require.Len(t, events, 6)
	assert.Equal(t, "message_start", events[0].eventType)
	assert.Equal(t, "content_block_start", events[1].eventType)
	assert.Equal(t, "content_block_delta", events[2].eventType)
	assert.Equal(t, "content_block_stop", events[3].eventType)
	assert.Equal(t, "message_delta", events[4].eventType)
	assert.Equal(t, "message_stop", events[5].eventType)
	// response.completed closes the stream; no duplicate argument delta is emitted
	// from function_call_arguments.done after deltas were already received.
	require.JSONEq(t, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Seattle\"}"}}`, events[2].data)
	require.JSONEq(t, `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":42,"output_tokens":7}}`, events[4].data)
	cached, ok := usage.CachedInputTokens()
	assert.True(t, ok)
	assert.Equal(t, uint32(5), cached)
}

func TestAnthropicToResponsesOpenAITranslator_ThinkingDisabled(t *testing.T) {
	request := anthropicToolRequest("gpt-5.6-terra", false)
	request.Thinking = &anthropic.Thinking{Disabled: &anthropic.ThinkingDisabled{Type: "disabled"}}
	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, body, err := translator.RequestBody(nil, request, false)
	require.NoError(t, err)
	var translated map[string]any
	require.NoError(t, json.Unmarshal(body, &translated))
	assert.Equal(t, map[string]any{"effort": "none"}, translated["reasoning"])
}

func TestAnthropicToResponsesOpenAITranslator_ParallelToolsEnabled(t *testing.T) {
	request := anthropicToolRequest("gpt-5.6-terra", false)
	request.ToolChoice = &anthropic.ToolChoice{Any: &anthropic.ToolChoiceAny{
		Type: "any", DisableParallelToolUse: ptr.To(false),
	}}
	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, body, err := translator.RequestBody(nil, request, false)
	require.NoError(t, err)
	var translated map[string]any
	require.NoError(t, json.Unmarshal(body, &translated))
	assert.Equal(t, true, translated["parallel_tool_calls"])
	assert.Equal(t, "required", translated["tool_choice"])
}

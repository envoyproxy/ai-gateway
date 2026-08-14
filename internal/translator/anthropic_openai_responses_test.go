// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
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

func TestAnthropicToResponsesOpenAITranslator_RequestBody(t *testing.T) {
	request := anthropicToolRequest("gpt-5.6-terra", false)
	reasoningItem := &openai.ResponseReasoningItem{
		ID: "rs_123", Type: "reasoning", EncryptedContent: "encrypted-state",
		Summary: []openai.ResponseReasoningItemSummaryParam{{Type: "summary_text", Text: "Checked the weather tool."}},
	}
	reasoningEnvelope, err := encodeOpenAIReasoningItem(reasoningItem)
	require.NoError(t, err)
	disableParallel := true
	request.ToolChoice.Tool.DisableParallelToolUse = &disableParallel
	request.Messages = append(request.Messages,
		anthropic.MessageParam{
			Role: anthropic.MessageRoleAssistant,
			Content: anthropic.MessageContent{Array: []anthropic.ContentBlockParam{
				{RedactedThinking: &anthropic.RedactedThinkingBlockParam{
					Type: "redacted_thinking", Data: reasoningEnvelope,
				}},
				{ToolUse: &anthropic.ToolUseBlockParam{
					Type: "tool_use", ID: "call_123", Name: "get_weather", Input: map[string]any{"city": "Seattle"},
				}},
			}},
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
	assert.Equal(t, []any{"reasoning.encrypted_content"}, translated["include"])

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
	require.Len(t, input, 4)
	assert.Equal(t, "reasoning", input[1].(map[string]any)["type"])
	assert.Equal(t, "rs_123", input[1].(map[string]any)["id"])
	assert.Equal(t, "encrypted-state", input[1].(map[string]any)["encrypted_content"])
	assert.Equal(t, "function_call", input[2].(map[string]any)["type"])
	assert.Equal(t, "call_123", input[2].(map[string]any)["call_id"])
	assert.Equal(t, "function_call_output", input[3].(map[string]any)["type"])
	assert.Equal(t, "rainy", input[3].(map[string]any)["output"])
}

func TestAnthropicToResponsesOpenAITranslator_EncryptedReasoningRoundTrip(t *testing.T) {
	first := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, _, err := first.RequestBody(nil, anthropicToolRequest("gpt-5.6-terra", false), false)
	require.NoError(t, err)

	const reasoningJSON = `{
		"id":"rs_123",
		"type":"reasoning",
		"encrypted_content":"gAAAAA+/=_-opaque-state",
		"summary":[],
		"content":[{"type":"reasoning_text","text":"summary-safe text"}],
		"status":"completed"
	}`
	responseJSON := `{
		"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"completed",
		"output":[` + reasoningJSON + `,{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"First turn."}]}]
	}`
	_, anthropicBody, _, _, err := first.ResponseBody(nil, strings.NewReader(responseJSON), true, nil)
	require.NoError(t, err)

	var firstResponse anthropic.MessagesResponse
	require.NoError(t, json.Unmarshal(anthropicBody, &firstResponse))
	require.Len(t, firstResponse.Content, 2)
	require.NotNil(t, firstResponse.Content[0].RedactedThinking)
	_, err = base64.RawStdEncoding.DecodeString(firstResponse.Content[0].RedactedThinking.Data)
	require.NoError(t, err)

	// Replay the Anthropic response content unchanged as the next request's
	// assistant message, as an Anthropic client would in a stateless conversation.
	contentJSON, err := json.Marshal(firstResponse.Content)
	require.NoError(t, err)
	var replayedContent []anthropic.ContentBlockParam
	require.NoError(t, json.Unmarshal(contentJSON, &replayedContent))
	replayRequest := &anthropic.MessagesRequest{
		Model: "gpt-5.6-terra", MaxTokens: 128,
		Messages: []anthropic.MessageParam{
			{Role: anthropic.MessageRoleAssistant, Content: anthropic.MessageContent{Array: replayedContent}},
			{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "Second turn."}},
		},
	}
	second := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, replayBody, err := second.RequestBody(nil, replayRequest, false)
	require.NoError(t, err)

	var replay map[string]any
	require.NoError(t, json.Unmarshal(replayBody, &replay))
	input := replay["input"].([]any)
	require.Len(t, input, 3)
	replayedReasoning, err := json.Marshal(input[0])
	require.NoError(t, err)
	assert.JSONEq(t, reasoningJSON, string(replayedReasoning))
	assert.Equal(t, "gAAAAA+/=_-opaque-state", input[0].(map[string]any)["encrypted_content"])
}

func TestAnthropicToResponsesOpenAITranslator_ResponseBody(t *testing.T) {
	translator := NewAnthropicToResponsesOpenAITranslator("v1", "")
	_, _, err := translator.RequestBody(nil, anthropicToolRequest("gpt-5.6-terra", false), false)
	require.NoError(t, err)

	response := `{
		"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"completed",
		"output":[
			{"id":"rs_123","type":"reasoning","encrypted_content":"encrypted-state","summary":[{"type":"summary_text","text":"Checked the weather tool."}],"status":"completed"},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I'll check."}]},
			{"id":"fc_1","type":"function_call","call_id":"call_123","name":"get_weather","arguments":"{\"city\":\"Seattle\"}","status":"completed"}
		],
		"usage":{"input_tokens":42,"input_tokens_details":{"cached_tokens":10,"cache_write_tokens":2},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":49}
	}`
	headers, body, usage, model, err := translator.ResponseBody(nil, strings.NewReader(response), true, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.6-terra", model)
	require.Len(t, headers, 1)

	var translated anthropic.MessagesResponse
	require.NoError(t, json.Unmarshal(body, &translated))
	assert.Equal(t, "resp_123", translated.ID)
	require.Len(t, translated.Content, 3)
	require.NotNil(t, translated.Content[0].RedactedThinking)
	decodedReasoning, ok := decodeOpenAIReasoningItem(translated.Content[0].RedactedThinking.Data)
	require.True(t, ok)
	assert.Equal(t, "rs_123", decodedReasoning["id"])
	assert.Equal(t, "encrypted-state", decodedReasoning["encrypted_content"])
	assert.Equal(t, "I'll check.", translated.Content[1].Text.Text)
	assert.Equal(t, "call_123", translated.Content[2].Tool.ID)
	assert.Equal(t, map[string]any{"city": "Seattle"}, translated.Content[2].Tool.Input)
	assert.Equal(t, anthropic.StopReasonToolUse, *translated.StopReason)
	assert.Equal(t, float64(30), translated.Usage.InputTokens)
	assert.Equal(t, float64(10), translated.Usage.CacheReadInputTokens)
	assert.Equal(t, float64(2), translated.Usage.CacheCreationInputTokens)
	assert.Equal(t, float64(42), translated.Usage.InputTokens+translated.Usage.CacheReadInputTokens+translated.Usage.CacheCreationInputTokens)
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
		`event: response.output_item.done\ndata: {"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"id":"rs_123","type":"reasoning","encrypted_content":"encrypted-state","summary":[],"status":"completed"}}`,
		`event: response.output_item.added\ndata: {"type":"response.output_item.added","sequence_number":2,"output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_123","name":"get_weather","arguments":"","status":"in_progress"}}`,
		`event: response.function_call_arguments.delta\ndata: {"type":"response.function_call_arguments.delta","sequence_number":3,"item_id":"fc_1","output_index":1,"delta":"{\"city\":\"Seattle\"}"}`,
		`event: response.function_call_arguments.done\ndata: {"type":"response.function_call_arguments.done","sequence_number":4,"item_id":"fc_1","output_index":1,"name":"get_weather","arguments":"{\"city\":\"Seattle\"}"}`,
		`event: response.completed\ndata: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_123","object":"response","model":"gpt-5.6-terra","status":"completed","output":[{"id":"rs_123","type":"reasoning","encrypted_content":"encrypted-state","summary":[],"status":"completed"}],"usage":{"input_tokens":42,"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":3},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":49}}}`,
	}, "\n\n") + "\n\n"
	stream = strings.ReplaceAll(stream, `\n`, "\n")

	_, body, usage, model, err := translator.ResponseBody(nil, bytes.NewBufferString(stream), true, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.6-terra", model)
	events := parseSSEEventsFromBytes(body)
	require.Len(t, events, 8)
	assert.Equal(t, "message_start", events[0].eventType)
	assert.Equal(t, "content_block_start", events[1].eventType)
	assert.Equal(t, "content_block_stop", events[2].eventType)
	assert.Equal(t, "content_block_start", events[3].eventType)
	assert.Equal(t, "content_block_delta", events[4].eventType)
	assert.Equal(t, "content_block_stop", events[5].eventType)
	assert.Equal(t, "message_delta", events[6].eventType)
	assert.Equal(t, "message_stop", events[7].eventType)
	var redactedStart map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[1].data), &redactedStart))
	redactedBlock := redactedStart["content_block"].(map[string]any)
	assert.Equal(t, "redacted_thinking", redactedBlock["type"])
	streamedReasoning, decoded := decodeOpenAIReasoningItem(redactedBlock["data"])
	assert.True(t, decoded)
	assert.Equal(t, []any{}, streamedReasoning["summary"])
	// response.completed closes the stream; no duplicate argument delta is emitted
	// from function_call_arguments.done after deltas were already received.
	require.JSONEq(t, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Seattle\"}"}}`, events[4].data)
	require.JSONEq(t, `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":42,"output_tokens":7}}`, events[6].data)
	cached, ok := usage.CachedInputTokens()
	assert.True(t, ok)
	assert.Equal(t, uint32(5), cached)
	cacheCreation, ok := usage.CacheCreationInputTokens()
	assert.True(t, ok)
	assert.Equal(t, uint32(3), cacheCreation)
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

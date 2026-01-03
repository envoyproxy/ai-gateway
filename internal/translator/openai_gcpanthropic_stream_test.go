// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

// mockErrorReader is a helper for testing io.Reader failures.
type mockErrorReader struct{}

func (r *mockErrorReader) Read(_ []byte) (n int, err error) {
	return 0, fmt.Errorf("mock reader error")
}

func TestAnthropicStreamParser_ErrorHandling(t *testing.T) {
	runStreamErrTest := func(t *testing.T, sseStream string, endOfStream bool) error {
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, _, _, _, err = translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), endOfStream, nil)
		return err
	}

	tests := []struct {
		name          string
		sseStream     string
		endOfStream   bool
		expectedError string
	}{
		{
			name:          "malformed message_start event",
			sseStream:     "event: message_start\ndata: {invalid\n\n",
			expectedError: "unmarshal message_start",
		},
		{
			name:          "malformed content_block_start event",
			sseStream:     "event: content_block_start\ndata: {invalid\n\n",
			expectedError: "failed to unmarshal content_block_start",
		},
		{
			name:          "malformed content_block_delta event",
			sseStream:     "event: content_block_delta\ndata: {invalid\n\n",
			expectedError: "unmarshal content_block_delta",
		},
		{
			name:          "malformed content_block_stop event",
			sseStream:     "event: content_block_stop\ndata: {invalid\n\n",
			expectedError: "unmarshal content_block_stop",
		},
		{
			name:          "malformed error event data",
			sseStream:     "event: error\ndata: {invalid\n\n",
			expectedError: "unparsable error event",
		},
		{
			name:        "unknown stop reason",
			endOfStream: true,
			sseStream: `event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "some_future_reason"}, "usage": {"output_tokens": 0}}

event: message_stop
data: {"type": "message_stop"}
`,
			expectedError: "received invalid stop reason",
		},
		{
			name:          "malformed_final_event_block",
			sseStream:     "event: message_stop\ndata: {invalid", // No trailing \n\n.
			endOfStream:   true,
			expectedError: "unmarshal message_stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runStreamErrTest(t, tt.sseStream, tt.endOfStream)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.expectedError)
		})
	}

	t.Run("body read error", func(t *testing.T) {
		parser := newAnthropicStreamParser("test-model")
		_, _, _, _, err := parser.Process(&mockErrorReader{}, false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read from stream body")
	})
}

// TestResponseModel_GCPAnthropicStreaming tests that GCP Anthropic streaming returns the request model
// GCP Anthropic uses deterministic model mapping without virtualization
func TestResponseModel_GCPAnthropicStreaming(t *testing.T) {
	modelName := "claude-sonnet-4@20250514"
	sseStream := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY", "type": "message", "role": "assistant", "content": [], "model": "claude-sonnet-4@20250514", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 10, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence":null}, "usage": {"output_tokens": 5}}

event: message_stop
data: {"type": "message_stop"}

`
	openAIReq := &openai.ChatCompletionRequest{
		Stream:    true,
		Model:     modelName, // Use the actual model name from documentation
		MaxTokens: new(int64),
	}

	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
	_, _, err := translator.RequestBody(nil, openAIReq, false)
	require.NoError(t, err)

	// Test streaming response - GCP Anthropic doesn't return model in response, uses request model
	_, _, tokenUsage, responseModel, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel) // Returns the request model since no virtualization
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(10), inputTokens)
	outputTokens, ok := tokenUsage.OutputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(5), outputTokens)
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody_Streaming(t *testing.T) {
	t.Run("handles simple text stream", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY", "type": "message", "role": "assistant", "content": [], "model": "claude-opus-4-20250514", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 25, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "!"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence":null}, "usage": {"output_tokens": 15}}

event: message_stop
data: {"type": "message_stop"}

`
		openAIReq := &openai.ChatCompletionRequest{
			Stream:    true,
			Model:     "test-model",
			MaxTokens: new(int64),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)

		bodyStr := string(bm)
		require.Contains(t, bodyStr, `"content":"Hello"`)
		require.Contains(t, bodyStr, `"finish_reason":"stop"`)
		require.Contains(t, bodyStr, `"prompt_tokens":25`)
		require.Contains(t, bodyStr, `"completion_tokens":15`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles text and tool use stream", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_014p7gG3wDgGV9EUtLvnow3U","type":"message","role":"assistant","model":"claude-opus-4-20250514","stop_sequence":null,"usage":{"input_tokens":472,"output_tokens":2},"content":[],"stop_reason":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Okay"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":","}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" let"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"'s"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" check"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" the"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" for"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" San"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" Francisco"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":","}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" CA"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":":"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01T1x1fJ34qAmk2tNTrN7Up6","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" \"San"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" Francisc"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"o,"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" CA\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":", "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"unit\": \"fah"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"renheit\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":89}}

event: message_stop
data: {"type":"message_stop"}
`

		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// Parse all streaming events to verify the event flow
		var chunks []openai.ChatCompletionResponseChunk
		var textChunks []string
		var toolCallStarted bool
		var hasRole bool
		var toolCallCompleted bool
		var finalFinishReason openai.ChatCompletionChoicesFinishReason
		var finalUsageChunk *openai.ChatCompletionResponseChunk
		var toolCallChunks []string // Track partial JSON chunks

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)

			// Check if this is the final usage chunk
			if strings.Contains(jsonBody, `"usage"`) {
				finalUsageChunk = &chunk
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				// Check for role in first content chunk
				if choice.Delta != nil && choice.Delta.Content != nil && *choice.Delta.Content != "" && !hasRole {
					require.NotNil(t, choice.Delta.Role, "Role should be present on first content chunk")
					require.Equal(t, openai.ChatMessageRoleAssistant, choice.Delta.Role)
					hasRole = true
				}

				// Collect text content
				if choice.Delta != nil && choice.Delta.Content != nil {
					textChunks = append(textChunks, *choice.Delta.Content)
				}

				// Check tool calls - start and accumulate partial JSON
				if choice.Delta != nil && len(choice.Delta.ToolCalls) > 0 {
					toolCall := choice.Delta.ToolCalls[0]

					// Check tool call initiation
					if toolCall.Function.Name == "get_weather" && !toolCallStarted {
						require.Equal(t, "get_weather", toolCall.Function.Name)
						require.NotNil(t, toolCall.ID)
						require.Equal(t, "toolu_01T1x1fJ34qAmk2tNTrN7Up6", *toolCall.ID)
						require.Equal(t, int64(0), toolCall.Index, "Tool call should be at index 1 (after text content at index 0)")
						toolCallStarted = true
					}

					// Accumulate partial JSON arguments - these should also be at index 1
					if toolCall.Function.Arguments != "" {
						toolCallChunks = append(toolCallChunks, toolCall.Function.Arguments)

						// Verify the index remains consistent at 1 for all tool call chunks
						require.Equal(t, int64(0), toolCall.Index, "Tool call argument chunks should be at index 1")
					}
				}

				// Track finish reason
				if choice.FinishReason != "" {
					finalFinishReason = choice.FinishReason
					if finalFinishReason == "tool_calls" {
						toolCallCompleted = true
					}
				}
			}
		}

		// Check the final usage chunk for accumulated tool call arguments
		if finalUsageChunk != nil {
			require.Equal(t, 472, finalUsageChunk.Usage.PromptTokens)
			require.Equal(t, 89, finalUsageChunk.Usage.CompletionTokens)
		}

		// Verify partial JSON accumulation in streaming chunks
		if len(toolCallChunks) > 0 {
			// Verify we got multiple partial JSON chunks during streaming
			require.GreaterOrEqual(t, len(toolCallChunks), 2, "Should receive multiple partial JSON chunks for tool arguments")

			// Verify some expected partial content appears in the chunks
			fullPartialJSON := strings.Join(toolCallChunks, "")
			require.Contains(t, fullPartialJSON, `"location":`, "Partial JSON should contain location field")
			require.Contains(t, fullPartialJSON, `"unit":`, "Partial JSON should contain unit field")
			require.Contains(t, fullPartialJSON, "San Francisco", "Partial JSON should contain location value")
			require.Contains(t, fullPartialJSON, "fahrenheit", "Partial JSON should contain unit value")
		}

		// Verify streaming event assertions
		require.GreaterOrEqual(t, len(chunks), 5, "Should have multiple streaming chunks")
		require.True(t, hasRole, "Should have role in first content chunk")
		require.True(t, toolCallStarted, "Tool call should have been initiated")
		require.True(t, toolCallCompleted, "Tool call should have complete arguments in final chunk")
		require.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, finalFinishReason, "Final finish reason should be tool_calls")

		// Verify text content was streamed correctly
		fullText := strings.Join(textChunks, "")
		require.Contains(t, fullText, "Okay, let's check the weather for San Francisco, CA:")
		require.GreaterOrEqual(t, len(textChunks), 3, "Text should be streamed in multiple chunks")

		// Original aggregate response assertions
		require.Contains(t, bodyStr, `"content":"Okay"`)
		require.Contains(t, bodyStr, `"name":"get_weather"`)
		require.Contains(t, bodyStr, "\"arguments\":\"{\\\"location\\\":")
		require.NotContains(t, bodyStr, "\"arguments\":\"{}\"")
		require.Contains(t, bodyStr, "renheit\\\"}\"")
		require.Contains(t, bodyStr, `"finish_reason":"tool_calls"`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles streaming with web search tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01G...","type":"message","role":"assistant","usage":{"input_tokens":2679,"output_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I'll check"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" the current weather in New York City for you"}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_014hJH82Qum7Td6UV8gDXThB","name":"web_search","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"weather NYC today\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_014hJH82Qum7Td6UV8gDXThB","content":[{"type":"web_search_result","title":"Weather in New York City in May 2025 (New York)","url":"https://world-weather.info/forecast/usa/new_york/may-2025/","page_age":null}]}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"Here's the current weather information for New York"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":" City."}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":510}}

event: message_stop
data: {"type":"message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		require.Contains(t, bodyStr, `"content":"I'll check"`)
		require.Contains(t, bodyStr, `"content":" the current weather in New York City for you"`)
		require.Contains(t, bodyStr, `"name":"web_search"`)
		require.Contains(t, bodyStr, "\"arguments\":\"{\\\"query\\\":\\\"weather NYC today\\\"}\"")
		require.NotContains(t, bodyStr, "\"arguments\":\"{}\"")
		require.Contains(t, bodyStr, `"content":"Here's the current weather information for New York"`)
		require.Contains(t, bodyStr, `"finish_reason":"stop"`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles unterminated tool call at end of stream", func(t *testing.T) {
		// This stream starts a tool call but ends without a content_block_stop or message_stop.
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_abc","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\": \"SF\"}"}}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var finalToolCallChunk openai.ChatCompletionResponseChunk

		// Split the response into individual SSE messages and find the final data chunk.
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.HasPrefix(line, "data: [DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")
			// The final chunk with the accumulated tool call is the only one with a "usage" field.
			if strings.Contains(jsonBody, `"usage"`) {
				err := json.Unmarshal([]byte(jsonBody), &finalToolCallChunk)
				require.NoError(t, err, "Failed to unmarshal final tool call chunk")
				break
			}
		}

		require.NotEmpty(t, finalToolCallChunk.Choices, "Final chunk should have choices")
		require.NotNil(t, finalToolCallChunk.Choices[0].Delta.ToolCalls, "Final chunk should have tool calls")

		finalToolCall := finalToolCallChunk.Choices[0].Delta.ToolCalls[0]
		require.Equal(t, "tool_abc", *finalToolCall.ID)
		require.Equal(t, "get_weather", finalToolCall.Function.Name)
		require.JSONEq(t, `{"location": "SF"}`, finalToolCall.Function.Arguments)
	})
	t.Run("handles  thinking and tool use stream", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_123", "type": "message", "role": "assistant", "usage": {"input_tokens": 50, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "name": "web_searcher"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Searching for information..."}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: content_block_start
data: {"type": "content_block_start", "index": 1, "content_block": {"type": "tool_use", "id": "toolu_abc123", "name": "get_weather", "input": {"location": "San Francisco, CA"}}}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "tool_use"}, "usage": {"output_tokens": 35}}

event: message_stop
data: {"type": "message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var contentDeltas []string
		var reasoningTexts []string
		var foundToolCallWithArgs bool
		var finalFinishReason openai.ChatCompletionChoicesFinishReason

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)

			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				if choice.Delta.Content != nil {
					contentDeltas = append(contentDeltas, *choice.Delta.Content)
				}
				if choice.Delta.ReasoningContent != nil {
					if choice.Delta.ReasoningContent.Text != "" {
						reasoningTexts = append(reasoningTexts, choice.Delta.ReasoningContent.Text)
					}
				}
				if len(choice.Delta.ToolCalls) > 0 {
					toolCall := choice.Delta.ToolCalls[0]
					// Check if this is the tool chunk that contains the arguments.
					if toolCall.Function.Arguments != "" {
						expectedArgs := `{"location":"San Francisco, CA"}`
						assert.JSONEq(t, expectedArgs, toolCall.Function.Arguments, "Tool call arguments do not match")
						assert.Equal(t, "get_weather", toolCall.Function.Name)
						assert.Equal(t, "toolu_abc123", *toolCall.ID)
						foundToolCallWithArgs = true
					} else {
						// This should be the initial tool call chunk with empty arguments since input is provided upfront
						assert.Equal(t, "get_weather", toolCall.Function.Name)
						assert.Equal(t, "toolu_abc123", *toolCall.ID)
					}
				}
			}
			if choice.FinishReason != "" {
				finalFinishReason = choice.FinishReason
			}
		}

		fullReasoning := strings.Join(reasoningTexts, "")

		assert.Contains(t, fullReasoning, "Searching for information...")
		require.True(t, foundToolCallWithArgs, "Did not find a tool call chunk with arguments to assert against")
		assert.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, finalFinishReason, "Final finish reason should be 'tool_calls'")
	})

	t.Run("handles thinking delta stream with text only", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_thinking_1", "type": "message", "role": "assistant", "usage": {"input_tokens": 20, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Let me think about this problem step by step."}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": " First, I need to understand the requirements."}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 15}}

event: message_stop
data: {"type": "message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var reasoningTexts []string
		var foundFinishReason bool

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)

			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta != nil && choice.Delta.ReasoningContent != nil {
				if choice.Delta.ReasoningContent.Text != "" {
					reasoningTexts = append(reasoningTexts, choice.Delta.ReasoningContent.Text)
				}
			}
			if choice.FinishReason == openai.ChatCompletionChoicesFinishReasonStop {
				foundFinishReason = true
			}
		}

		fullReasoning := strings.Join(reasoningTexts, "")
		assert.Contains(t, fullReasoning, "Let me think about this problem step by step.")
		assert.Contains(t, fullReasoning, " First, I need to understand the requirements.")
		require.True(t, foundFinishReason, "Should find stop finish reason")
	})

	t.Run("handles thinking delta stream with text and signature", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_thinking_2", "type": "message", "role": "assistant", "usage": {"input_tokens": 25, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "Processing request...", "signature": "sig_abc123"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": " Analyzing data...", "signature": "sig_def456"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 20}}

event: message_stop
data: {"type": "message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var reasoningTexts []string
		var signatures []string
		var foundFinishReason bool

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)

			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta != nil && choice.Delta.ReasoningContent != nil {
				if choice.Delta.ReasoningContent.Text != "" {
					reasoningTexts = append(reasoningTexts, choice.Delta.ReasoningContent.Text)
				}
				if choice.Delta.ReasoningContent.Signature != "" {
					signatures = append(signatures, choice.Delta.ReasoningContent.Signature)
				}
			}
			if choice.FinishReason == openai.ChatCompletionChoicesFinishReasonStop {
				foundFinishReason = true
			}
		}

		fullReasoning := strings.Join(reasoningTexts, "")
		assert.Contains(t, fullReasoning, "Processing request...")
		assert.Contains(t, fullReasoning, " Analyzing data...")

		allSignatures := strings.Join(signatures, ",")
		assert.Contains(t, allSignatures, "sig_abc123")
		assert.Contains(t, allSignatures, "sig_def456")

		require.True(t, foundFinishReason, "Should find stop finish reason")
	})
}

func TestAnthropicStreamParser_EventTypes(t *testing.T) {
	runStreamTest := func(t *testing.T, sseStream string, endOfStream bool) ([]byte, metrics.TokenUsage, error) {
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, tokenUsage, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), endOfStream, nil)
		return bm, tokenUsage, err
	}

	t.Run("handles message_start event", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_123", "usage": {"input_tokens": 15}}}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		assert.Empty(t, string(bm), "message_start should produce an empty chunk")
	})

	t.Run("handles content_block events for tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "id": "tool_abc", "name": "get_weather", "input":{}}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "input_json_delta", "partial_json": "{\"location\": \"SF\"}"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// 1. Split the stream into individual data chunks
		//    and remove the "data: " prefix.
		var chunks []openai.ChatCompletionResponseChunk
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)
		}

		// 2. Inspect the Go structs directly.
		require.Len(t, chunks, 2, "Expected two data chunks for this tool call stream")

		// Check the first chunk (the tool call initiation).
		firstChunk := chunks[0]
		require.NotNil(t, firstChunk.Choices[0].Delta.ToolCalls)
		require.Equal(t, "tool_abc", *firstChunk.Choices[0].Delta.ToolCalls[0].ID)
		require.Equal(t, "get_weather", firstChunk.Choices[0].Delta.ToolCalls[0].Function.Name)
		// With empty input, arguments should be empty string, not "{}"
		require.Empty(t, firstChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)

		// Check the second chunk (the arguments delta).
		secondChunk := chunks[1]
		require.NotNil(t, secondChunk.Choices[0].Delta.ToolCalls)
		argumentsJSON := secondChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments

		// 3. Unmarshal the arguments string to verify its contents.
		var args map[string]string
		err = json.Unmarshal([]byte(argumentsJSON), &args)
		require.NoError(t, err)
		require.Equal(t, "SF", args["location"])
	})

	t.Run("handles ping event", func(t *testing.T) {
		sseStream := `event: ping
data: {"type": "ping"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "ping should produce an empty chunk")
	})

	t.Run("handles error event", func(t *testing.T) {
		sseStream := `event: error
data: {"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}

`
		_, _, err := runStreamTest(t, sseStream, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "anthropic stream error: overloaded_error - Overloaded")
	})

	t.Run("gracefully handles unknown event types", func(t *testing.T) {
		sseStream := `event: future_event_type
data: {"some_new_data": "value"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "unknown events should be ignored and produce an empty chunk")
	})

	t.Run("handles message_stop event", func(t *testing.T) {
		sseStream := `event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "max_tokens"}, "usage": {"output_tokens": 1}}

event: message_stop
data: {"type": "message_stop"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		require.Contains(t, string(bm), `"finish_reason":"length"`)
	})

	t.Run("handles chunked input_json_delta for tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "id": "tool_123", "name": "get_weather"}}

event: content_block_delta
data: {"type": "content_block_delta","index": 0,"delta": {"type": "input_json_delta","partial_json": "{\"location\": \"San Fra"}}

event: content_block_delta
data: {"type": "content_block_delta","index": 0,"delta": {"type": "input_json_delta","partial_json": "ncisco\"}"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}
`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// 1. Unmarshal all the chunks from the stream response.
		var chunks []openai.ChatCompletionResponseChunk
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err := json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)
		}

		// 2. We expect 3 chunks: start, delta part 1, delta part 2.
		require.Len(t, chunks, 3, "Expected three data chunks for this stream")

		// 3. Verify the contents of each relevant chunk.

		// Chunk 1: Tool call start.
		chunk1ToolCalls := chunks[0].Choices[0].Delta.ToolCalls
		require.NotNil(t, chunk1ToolCalls)
		require.Equal(t, "get_weather", chunk1ToolCalls[0].Function.Name)

		// Chunk 2: First part of the arguments.
		chunk2Args := chunks[1].Choices[0].Delta.ToolCalls[0].Function.Arguments
		require.Equal(t, `{"location": "San Fra`, chunk2Args) //nolint:testifylint

		// Chunk 3: Second part of the arguments.
		chunk3Args := chunks[2].Choices[0].Delta.ToolCalls[0].Function.Arguments
		require.Equal(t, `ncisco"}`, chunk3Args)
	})
	t.Run("sends role on first chunk", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
`
		// Set endOfStream to true to ensure all events in the buffer are processed.
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var contentChunk openai.ChatCompletionResponseChunk
		foundChunk := false

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				jsonBody := after
				// We only care about the chunk that has the text content.
				if strings.Contains(jsonBody, `"content"`) {
					err := json.Unmarshal([]byte(jsonBody), &contentChunk)
					require.NoError(t, err, "Failed to unmarshal content chunk")
					foundChunk = true
					break
				}
			}
		}

		require.True(t, foundChunk, "Did not find a data chunk with content in the output")

		require.NotNil(t, contentChunk.Choices[0].Delta.Role, "Role should be present on the first chunk")
		require.Equal(t, openai.ChatMessageRoleAssistant, contentChunk.Choices[0].Delta.Role)
	})

	t.Run("accumulates output tokens", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":20}}}

event: message_delta
data: {"type":"message_delta","delta":{},"usage":{"output_tokens":10}}

event: message_delta
data: {"type":"message_delta","delta":{},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}
`
		// Run with endOfStream:true to get the final usage chunk.
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// The final usage chunk should sum the tokens from all message_delta events.
		require.Contains(t, bodyStr, `"completion_tokens":15`)
		require.Contains(t, bodyStr, `"prompt_tokens":20`)
		require.Contains(t, bodyStr, `"total_tokens":35`)
	})

	t.Run("ignores SSE comments", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

: this is a comment and should be ignored

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
`
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		require.Contains(t, bodyStr, `"content":"Hello"`)
		require.NotContains(t, bodyStr, "this is a comment")
	})
	t.Run("handles data-only event as a message event", func(t *testing.T) {
		sseStream := `data: some text

data: another message with two lines
`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "data-only events should be treated as no-op 'message' events and produce an empty chunk")
	})
}

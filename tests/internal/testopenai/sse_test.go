// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSEReader_ReadEvent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []SSEEvent
	}{
		{
			name: "single event with data",
			input: `data: {"test": "value"}

`,
			expected: []SSEEvent{
				{Data: `{"test": "value"}`},
			},
		},
		{
			name: "multiple events",
			input: `data: first event

data: second event

`,
			expected: []SSEEvent{
				{Data: "first event"},
				{Data: "second event"},
			},
		},
		{
			name: "event with all fields",
			input: `event: message
data: test data
id: 123
retry: 5000

`,
			expected: []SSEEvent{
				{
					Event: "message",
					Data:  "test data",
					ID:    "123",
					Retry: "5000",
				},
			},
		},
		{
			name: "skip empty lines",
			input: `

data: test


data: test2

`,
			expected: []SSEEvent{
				{Data: "test"},
				{Data: "test2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewSSEReader(strings.NewReader(tt.input))
			var events []SSEEvent

			for {
				event, err := reader.ReadEvent()
				if err != nil {
					break
				}
				events = append(events, *event)
			}

			require.Equal(t, tt.expected, events)
		})
	}
}

func TestReadChatCompletionStream(t *testing.T) {
	// Sample streaming response from OpenAI.
	streamData := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}

data: [DONE]

`

	chunks, content, err := ReadChatCompletionStream(strings.NewReader(streamData))
	require.NoError(t, err)
	require.Len(t, chunks, 6)
	require.Equal(t, "Hello world!", content)

	// Check first chunk (role).
	require.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	require.Empty(t, chunks[0].Choices[0].Delta.Content)

	// Check content chunks.
	require.Equal(t, "Hello", chunks[1].Choices[0].Delta.Content)
	require.Equal(t, " world", chunks[2].Choices[0].Delta.Content)
	require.Equal(t, "!", chunks[3].Choices[0].Delta.Content)

	// Check finish reason.
	require.Equal(t, "stop", chunks[4].Choices[0].FinishReason)

	// Check usage.
	promptTokens, completionTokens, totalTokens := ExtractTokenUsage(chunks)
	require.Equal(t, 10, promptTokens)
	require.Equal(t, 3, completionTokens)
	require.Equal(t, 13, totalTokens)
}

func TestReadChatCompletionStream_MalformedData(t *testing.T) {
	// Stream with some malformed data.
	streamData := `data: {"valid": "json"}

data: {invalid json

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-3.5-turbo","choices":[{"index":0,"delta":{"content":"test"},"finish_reason":null}]}

data: [DONE]

`

	chunks, content, err := ReadChatCompletionStream(strings.NewReader(streamData))
	require.NoError(t, err)
	// The first valid JSON is parsed as a chunk even though it's not a chat completion chunk.
	// The malformed JSON is skipped.
	// The valid chat completion chunk is parsed.
	require.Len(t, chunks, 2)
	require.Equal(t, "test", content) // Only content from actual chat completion chunks.
}

func TestExtractTokenUsage_NoUsage(t *testing.T) {
	chunks := []ChatCompletionChunk{
		{
			ID: "test",
			Choices: []struct {
				Index int `json:"index"`
				Delta struct {
					Role    string `json:"role,omitempty"`
					Content string `json:"content,omitempty"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason,omitempty"`
			}{
				{Delta: struct {
					Role    string `json:"role,omitempty"`
					Content string `json:"content,omitempty"`
				}{Content: "test"}},
			},
		},
	}

	promptTokens, completionTokens, totalTokens := ExtractTokenUsage(chunks)
	require.Equal(t, 0, promptTokens)
	require.Equal(t, 0, completionTokens)
	require.Equal(t, 0, totalTokens)
}

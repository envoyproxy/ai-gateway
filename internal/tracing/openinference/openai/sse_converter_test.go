// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertSSEToJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "empty input",
		},
		{
			name: "response with escaped content",
			input: `data: {"id":"test","object":"chat.completion.chunk","created":123,"model":"test","choices":[{"delta":{"content":"Line 1\\nLine 2\\t\\\"quoted\\\""}}]}

data: {"id":"test","object":"chat.completion.chunk","created":123,"model":"test","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}

data: [DONE]
`,
			expected: `{"id":"test","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Line 1\\nLine 2\\t\\\"quoted\\\""}}],"created":123,"model":"test","object":"chat.completion.chunk","usage":{"completion_tokens":2,"prompt_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "minimal response without optional fields",
			input: `data: {"id":"123","object":"chat.completion.chunk","created":456,"model":"gpt-4.1-nano","choices":[{"delta":{"content":"Hi"}}]}

data: [DONE]
`,
			expected: `{"id":"123","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hi"}}],"created":456,"model":"gpt-4.1-nano","object":"chat.completion.chunk"}`,
		},
		{
			name: "response with usage details",
			input: `data: {"id":"test","object":"chat.completion.chunk","created":123,"model":"test","choices":[{"delta":{"content":"Hi"}}]}

data: {"id":"test","object":"chat.completion.chunk","created":123,"model":"test","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]
`,
			expected: `{"id":"test","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hi"}}],"created":123,"model":"test","object":"chat.completion.chunk","usage":{"completion_tokens":1,"prompt_tokens":5,"total_tokens":6,"completion_tokens_details":{},"prompt_tokens_details":{}}}`,
		},
		{
			name: "response with unicode escapes",
			input: `data: {"id":"test","object":"chat.completion.chunk","created":123,"model":"test","choices":[{"delta":{"content":"Hello \\u0048\\u0065\\u006c\\u006c\\u006f"}}]}

data: [DONE]
`,
			expected: `{"id":"test","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hello \\u0048\\u0065\\u006c\\u006c\\u006f"}}],"created":123,"model":"test","object":"chat.completion.chunk"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertSSEToJSON([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, string(result))
		})
	}
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputMessageAttribute(t *testing.T) {
	tests := []struct {
		name    string
		index   int
		suffix  string
		wantKey string
	}{
		{
			name:    "role attribute",
			index:   0,
			suffix:  MessageRole,
			wantKey: "llm.input_messages.0.message.role",
		},
		{
			name:    "content attribute",
			index:   1,
			suffix:  MessageContent,
			wantKey: "llm.input_messages.1.message.content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := InputMessageAttribute(tt.index, tt.suffix)
			require.Equal(t, tt.wantKey, key)
		})
	}
}

func TestInputMessageContentAttribute(t *testing.T) {
	tests := []struct {
		name         string
		messageIndex int
		contentIndex int
		suffix       string
		expected     string
	}{
		{
			name:         "text content",
			messageIndex: 0,
			contentIndex: 0,
			suffix:       "text",
			expected:     "llm.input_messages.0.message.contents.0.message_content.text",
		},
		{
			name:         "image URL",
			messageIndex: 2,
			contentIndex: 1,
			suffix:       "image.image.url",
			expected:     "llm.input_messages.2.message.contents.1.message_content.image.image.url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := InputMessageContentAttribute(tt.messageIndex, tt.contentIndex, tt.suffix)
			require.Equal(t, tt.expected, key)
		})
	}
}

func TestOutputMessageAttribute(t *testing.T) {
	tests := []struct {
		name     string
		index    int
		suffix   string
		expected string
	}{
		{
			name:     "role attribute",
			index:    0,
			suffix:   MessageRole,
			expected: "llm.output_messages.0.message.role",
		},
		{
			name:     "content attribute",
			index:    1,
			suffix:   MessageContent,
			expected: "llm.output_messages.1.message.content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := OutputMessageAttribute(tt.index, tt.suffix)
			require.Equal(t, tt.expected, key)
		})
	}
}

func TestOutputMessageToolCallAttribute(t *testing.T) {
	tests := []struct {
		name          string
		messageIndex  int
		toolCallIndex int
		suffix        string
		expected      string
	}{
		{
			name:          "tool call id",
			messageIndex:  0,
			toolCallIndex: 0,
			suffix:        ToolCallID,
			expected:      "llm.output_messages.0.message.tool_calls.0.tool_call.id",
		},
		{
			name:          "tool call function name",
			messageIndex:  0,
			toolCallIndex: 0,
			suffix:        ToolCallFunctionName,
			expected:      "llm.output_messages.0.message.tool_calls.0.tool_call.function.name",
		},
		{
			name:          "tool call function arguments",
			messageIndex:  0,
			toolCallIndex: 0,
			suffix:        ToolCallFunctionArguments,
			expected:      "llm.output_messages.0.message.tool_calls.0.tool_call.function.arguments",
		},
		{
			name:          "second message first tool call",
			messageIndex:  1,
			toolCallIndex: 0,
			suffix:        ToolCallID,
			expected:      "llm.output_messages.1.message.tool_calls.0.tool_call.id",
		},
		{
			name:          "first message second tool call",
			messageIndex:  0,
			toolCallIndex: 1,
			suffix:        ToolCallFunctionName,
			expected:      "llm.output_messages.0.message.tool_calls.1.tool_call.function.name",
		},
		{
			name:          "multiple tool calls in message",
			messageIndex:  2,
			toolCallIndex: 3,
			suffix:        ToolCallFunctionArguments,
			expected:      "llm.output_messages.2.message.tool_calls.3.tool_call.function.arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := OutputMessageToolCallAttribute(tt.messageIndex, tt.toolCallIndex, tt.suffix)
			require.Equal(t, tt.expected, key)
		})
	}
}

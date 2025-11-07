// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessagesRequest_GetModel(t *testing.T) {
	tests := []struct {
		name     string
		request  MessagesRequest
		expected string
	}{
		{
			name:     "valid model string",
			request:  MessagesRequest{"model": "claude-3-sonnet"},
			expected: "claude-3-sonnet",
		},
		{
			name:     "missing model field",
			request:  MessagesRequest{},
			expected: "",
		},
		{
			name:     "non-string model field",
			request:  MessagesRequest{"model": 123},
			expected: "",
		},
		{
			name:     "nil model field",
			request:  MessagesRequest{"model": nil},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.request.GetModel()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMessagesRequest_GetMaxTokens(t *testing.T) {
	tests := []struct {
		name     string
		request  MessagesRequest
		expected int
	}{
		{
			name:     "valid max_tokens float64",
			request:  MessagesRequest{"max_tokens": 1000.0},
			expected: 1000,
		},
		{
			name:     "valid max_tokens with decimal",
			request:  MessagesRequest{"max_tokens": 1000.5},
			expected: 1000,
		},
		{
			name:     "missing max_tokens field",
			request:  MessagesRequest{},
			expected: 0,
		},
		{
			name:     "non-float64 max_tokens field",
			request:  MessagesRequest{"max_tokens": "1000"},
			expected: 0,
		},
		{
			name:     "nil max_tokens field",
			request:  MessagesRequest{"max_tokens": nil},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.request.GetMaxTokens()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMessagesRequest_GetStream(t *testing.T) {
	tests := []struct {
		name     string
		request  MessagesRequest
		expected bool
	}{
		{
			name:     "stream true",
			request:  MessagesRequest{"stream": true},
			expected: true,
		},
		{
			name:     "stream false",
			request:  MessagesRequest{"stream": false},
			expected: false,
		},
		{
			name:     "missing stream field",
			request:  MessagesRequest{},
			expected: false,
		},
		{
			name:     "non-bool stream field",
			request:  MessagesRequest{"stream": "true"},
			expected: false,
		},
		{
			name:     "nil stream field",
			request:  MessagesRequest{"stream": nil},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.request.GetStream()
			require.Equal(t, tt.expected, result)
		})
	}
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
)

func TestExtractLLMTokenUsage(t *testing.T) {
	tests := []struct {
		name                   string
		inputTokens           int64
		outputTokens          int64
		cacheReadTokens       int64
		cacheCreationTokens   int64
		expectedInputTokens   uint32
		expectedOutputTokens  uint32
		expectedTotalTokens   uint32
		expectedCachedTokens  uint32
	}{
		{
			name:                 "basic usage without cache",
			inputTokens:          100,
			outputTokens:         50,
			cacheReadTokens:      0,
			cacheCreationTokens:  0,
			expectedInputTokens:  100,
			expectedOutputTokens: 50,
			expectedTotalTokens:  150,
			expectedCachedTokens: 0,
		},
		{
			name:                 "usage with cache read tokens",
			inputTokens:          80,
			outputTokens:         30,
			cacheReadTokens:      20,
			cacheCreationTokens:  0,
			expectedInputTokens:  100, // 80 + 0 + 20
			expectedOutputTokens: 30,
			expectedTotalTokens:  130, // 100 + 30
			expectedCachedTokens: 20,  // 20 + 0
		},
		{
			name:                 "usage with cache creation tokens",
			inputTokens:          60,
			outputTokens:         40,
			cacheReadTokens:      0,
			cacheCreationTokens:  15,
			expectedInputTokens:  75, // 60 + 15 + 0
			expectedOutputTokens: 40,
			expectedTotalTokens:  115, // 75 + 40
			expectedCachedTokens: 15,  // 0 + 15
		},
		{
			name:                 "usage with both cache types",
			inputTokens:          70,
			outputTokens:         25,
			cacheReadTokens:      10,
			cacheCreationTokens:  5,
			expectedInputTokens:  85, // 70 + 5 + 10
			expectedOutputTokens: 25,
			expectedTotalTokens:  110, // 85 + 25
			expectedCachedTokens: 15,  // 10 + 5
		},
		{
			name:                 "zero values",
			inputTokens:          0,
			outputTokens:         0,
			cacheReadTokens:      0,
			cacheCreationTokens:  0,
			expectedInputTokens:  0,
			expectedOutputTokens: 0,
			expectedTotalTokens:  0,
			expectedCachedTokens: 0,
		},
		{
			name:                 "large values",
			inputTokens:          100000,
			outputTokens:         50000,
			cacheReadTokens:      25000,
			cacheCreationTokens:  15000,
			expectedInputTokens:  140000, // 100000 + 15000 + 25000
			expectedOutputTokens: 50000,
			expectedTotalTokens:  190000, // 140000 + 50000
			expectedCachedTokens: 40000,  // 25000 + 15000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractLLMTokenUsage(
				tt.inputTokens,
				tt.outputTokens,
				tt.cacheReadTokens,
				tt.cacheCreationTokens,
			)

			assert.Equal(t, tt.expectedInputTokens, result.InputTokens, "InputTokens should match")
			assert.Equal(t, tt.expectedOutputTokens, result.OutputTokens, "OutputTokens should match")
			assert.Equal(t, tt.expectedTotalTokens, result.TotalTokens, "TotalTokens should match")
			assert.Equal(t, tt.expectedCachedTokens, result.CachedInputTokens, "CachedInputTokens should match")
		})
	}
}

func TestExtractLLMTokenUsageFromUsage(t *testing.T) {
	tests := []struct {
		name     string
		usage    anthropic.Usage
		expected LLMTokenUsage
	}{
		{
			name: "non-streaming response without cache",
			usage: anthropic.Usage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expected: LLMTokenUsage{
				InputTokens:       150,
				OutputTokens:      75,
				TotalTokens:       225,
				CachedInputTokens: 0,
			},
		},
		{
			name: "non-streaming response with cache read",
			usage: anthropic.Usage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheReadInputTokens:     25,
				CacheCreationInputTokens: 0,
			},
			expected: LLMTokenUsage{
				InputTokens:       125, // 100 + 0 + 25
				OutputTokens:      50,
				TotalTokens:       175, // 125 + 50
				CachedInputTokens: 25,  // 25 + 0
			},
		},
		{
			name: "non-streaming response with both cache types",
			usage: anthropic.Usage{
				InputTokens:              90,
				OutputTokens:             60,
				CacheReadInputTokens:     15,
				CacheCreationInputTokens: 10,
			},
			expected: LLMTokenUsage{
				InputTokens:       115, // 90 + 10 + 15
				OutputTokens:      60,
				TotalTokens:       175, // 115 + 60
				CachedInputTokens: 25,  // 15 + 10
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractLLMTokenUsageFromUsage(tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromMessageUsage(t *testing.T) {
	tests := []struct {
		name     string
		usage    anthropic.Usage
		expected LLMTokenUsage
	}{
		{
			name: "message_start event without cache",
			usage: anthropic.Usage{
				InputTokens:              200,
				OutputTokens:             1,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expected: LLMTokenUsage{
				InputTokens:       200,
				OutputTokens:      1,
				TotalTokens:       201,
				CachedInputTokens: 0,
			},
		},
		{
			name: "message_start event with cache",
			usage: anthropic.Usage{
				InputTokens:              180,
				OutputTokens:             1,
				CacheReadInputTokens:     20,
				CacheCreationInputTokens: 5,
			},
			expected: LLMTokenUsage{
				InputTokens:       205, // 180 + 5 + 20
				OutputTokens:      1,
				TotalTokens:       206, // 205 + 1
				CachedInputTokens: 25,  // 20 + 5
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractLLMTokenUsageFromMessageUsage(tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromDeltaUsage(t *testing.T) {
	tests := []struct {
		name     string
		usage    anthropic.MessageDeltaUsage
		expected LLMTokenUsage
	}{
		{
			name: "message_delta event with final totals",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              250,
				OutputTokens:             120,
				CacheReadInputTokens:     30,
				CacheCreationInputTokens: 0,
			},
			expected: LLMTokenUsage{
				InputTokens:       280, // 250 + 0 + 30
				OutputTokens:      120,
				TotalTokens:       400, // 280 + 120
				CachedInputTokens: 30,  // 30 + 0
			},
		},
		{
			name: "message_delta event with only output tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              0,
				OutputTokens:             85,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expected: LLMTokenUsage{
				InputTokens:       0,
				OutputTokens:      85,
				TotalTokens:       85,
				CachedInputTokens: 0,
			},
		},
		{
			name: "message_delta with cache creation tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     10,
				CacheCreationInputTokens: 5,
			},
			expected: LLMTokenUsage{
				InputTokens:       165, // 150 + 5 + 10
				OutputTokens:      75,
				TotalTokens:       240, // 165 + 75
				CachedInputTokens: 15,  // 10 + 5
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractLLMTokenUsageFromDeltaUsage(tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test edge cases and boundary conditions
func TestExtractLLMTokenUsage_EdgeCases(t *testing.T) {
	t.Run("negative values should be handled", func(t *testing.T) {
		// Note: In practice, the Anthropic API shouldn't return negative values,
		// but our function should handle them gracefully by casting to uint32
		result := ExtractLLMTokenUsage(-10, -5, -2, -1)

		// Negative int64 values will wrap around when cast to uint32
		// This test documents current behavior rather than prescribing it
		assert.NotPanics(t, func() {
			ExtractLLMTokenUsage(-10, -5, -2, -1)
		})

		// The exact values aren't important, just that it doesn't panic
		assert.NotNil(t, result)
	})

	t.Run("maximum int64 values", func(t *testing.T) {
		// Test with very large values to ensure no overflow issues
		// Note: This will result in truncation when casting to uint32
		assert.NotPanics(t, func() {
			ExtractLLMTokenUsage(9223372036854775807, 1000, 500, 100)
		})
	})
}

// Test that demonstrates the correct calculation according to Claude API docs
func TestExtractLLMTokenUsage_ClaudeAPIDocumentationCompliance(t *testing.T) {
	t.Run("claude API documentation example", func(t *testing.T) {
		// This test verifies compliance with Claude API documentation:
		// "Total input tokens in a request is the summation of input_tokens,
		// cache_creation_input_tokens, and cache_read_input_tokens"

		inputTokens := int64(100)
		cacheCreationTokens := int64(20)
		cacheReadTokens := int64(30)
		outputTokens := int64(50)

		result := ExtractLLMTokenUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)

		// Total input should be sum of all input token types
		expectedTotalInput := uint32(inputTokens + cacheCreationTokens + cacheReadTokens) // 100 + 20 + 30 = 150
		assert.Equal(t, expectedTotalInput, result.InputTokens,
			"InputTokens should be sum of input_tokens + cache_creation_input_tokens + cache_read_input_tokens")

		// Total cache should be sum of cache token types
		expectedCacheTokens := uint32(cacheCreationTokens + cacheReadTokens) // 20 + 30 = 50
		assert.Equal(t, expectedCacheTokens, result.CachedInputTokens,
			"CachedInputTokens should be sum of cache_creation_input_tokens + cache_read_input_tokens")

		// Total tokens should be input + output
		expectedTotal := uint32(expectedTotalInput + uint32(outputTokens)) // 150 + 50 = 200
		assert.Equal(t, expectedTotal, result.TotalTokens,
			"TotalTokens should be InputTokens + OutputTokens")
	})
}
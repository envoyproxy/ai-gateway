// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestExtractLLMTokenUsage(t *testing.T) {
	tests := []struct {
		name                        string
		inputTokens                 int64
		outputTokens                int64
		cacheReadTokens             int64
		cacheCreationTokens         int64
		expectedInputTokens         uint32
		expectedOutputTokens        uint32
		expectedTotalTokens         uint32
		expectedCachedTokens        uint32
		expectedCacheCreationTokens uint32
	}{
		{
			name:                        "basic usage without cache",
			inputTokens:                 100,
			outputTokens:                50,
			cacheReadTokens:             0,
			cacheCreationTokens:         0,
			expectedInputTokens:         100,
			expectedOutputTokens:        50,
			expectedTotalTokens:         150,
			expectedCachedTokens:        0,
			expectedCacheCreationTokens: 0,
		},
		{
			name:                        "usage with cache read tokens",
			inputTokens:                 80,
			outputTokens:                30,
			cacheReadTokens:             20,
			cacheCreationTokens:         0,
			expectedInputTokens:         100, // 80 + 0 + 20
			expectedOutputTokens:        30,
			expectedTotalTokens:         130, // 100 + 30
			expectedCachedTokens:        20,  // 20
			expectedCacheCreationTokens: 0,
		},
		{
			name:                        "usage with cache creation tokens",
			inputTokens:                 60,
			outputTokens:                40,
			cacheReadTokens:             0,
			cacheCreationTokens:         15,
			expectedInputTokens:         75, // 60 + 15 + 0
			expectedOutputTokens:        40,
			expectedTotalTokens:         115, // 75 + 40
			expectedCachedTokens:        0,   // 0
			expectedCacheCreationTokens: 15,  // 15
		},
		{
			name:                        "usage with both cache types",
			inputTokens:                 70,
			outputTokens:                25,
			cacheReadTokens:             10,
			cacheCreationTokens:         5,
			expectedInputTokens:         85, // 70 + 5 + 10
			expectedOutputTokens:        25,
			expectedTotalTokens:         110, // 85 + 25
			expectedCachedTokens:        10,  // 10
			expectedCacheCreationTokens: 5,   // 5
		},
		{
			name:                        "zero values",
			inputTokens:                 0,
			outputTokens:                0,
			cacheReadTokens:             0,
			cacheCreationTokens:         0,
			expectedInputTokens:         0,
			expectedOutputTokens:        0,
			expectedTotalTokens:         0,
			expectedCachedTokens:        0,
			expectedCacheCreationTokens: 0,
		},
		{
			name:                        "large values",
			inputTokens:                 100000,
			outputTokens:                50000,
			cacheReadTokens:             25000,
			cacheCreationTokens:         15000,
			expectedInputTokens:         140000, // 100000 + 15000 + 25000
			expectedOutputTokens:        50000,
			expectedTotalTokens:         190000, // 140000 + 50000
			expectedCachedTokens:        25000,  // 25000
			expectedCacheCreationTokens: 15000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := metrics.ExtractTokenUsageFromAnthropic(
				tt.inputTokens,
				tt.outputTokens,
				tt.cacheReadTokens,
				tt.cacheCreationTokens,
			)

			expected := tokenUsageFrom(
				int32(tt.expectedInputTokens),         // nolint:gosec
				int32(tt.expectedCachedTokens),        // nolint:gosec
				int32(tt.expectedCacheCreationTokens), // nolint:gosec
				int32(tt.expectedOutputTokens),        // nolint:gosec
				int32(tt.expectedTotalTokens),         // nolint:gosec
			)
			assert.Equal(t, expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromUsage(t *testing.T) {
	tests := []struct {
		name                        string
		usage                       anthropic.Usage
		expectedInputTokens         int32
		expectedOutputTokens        int32
		expectedTotalTokens         int32
		expectedCachedTokens        uint32
		expectedCacheCreationTokens uint32
	}{
		{
			name: "non-streaming response without cache",
			usage: anthropic.Usage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:         150,
			expectedOutputTokens:        75,
			expectedTotalTokens:         225,
			expectedCachedTokens:        0,
			expectedCacheCreationTokens: 0,
		},
		{
			name: "non-streaming response with cache read",
			usage: anthropic.Usage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheReadInputTokens:     25,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:         125, // 100 + 0 + 25
			expectedOutputTokens:        50,
			expectedTotalTokens:         175, // 125 + 50
			expectedCachedTokens:        25,  // 25
			expectedCacheCreationTokens: 0,   // 0
		},
		{
			name: "non-streaming response with both cache types",
			usage: anthropic.Usage{
				InputTokens:              90,
				OutputTokens:             60,
				CacheReadInputTokens:     15,
				CacheCreationInputTokens: 10,
			},
			expectedInputTokens:         115, // 90 + 10 + 15
			expectedOutputTokens:        60,
			expectedTotalTokens:         175, // 115 + 60
			expectedCachedTokens:        15,  // 15
			expectedCacheCreationTokens: 10,  // 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := metrics.ExtractTokenUsageFromAnthropic(tt.usage.InputTokens,
				tt.usage.OutputTokens,
				tt.usage.CacheReadInputTokens,
				tt.usage.CacheCreationInputTokens,
			)
			expected := tokenUsageFrom(tt.expectedInputTokens, int32(tt.expectedCachedTokens), int32(tt.expectedCacheCreationTokens), tt.expectedOutputTokens, tt.expectedTotalTokens) // nolint:gosec
			assert.Equal(t, expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromDeltaUsage(t *testing.T) {
	tests := []struct {
		name                        string
		usage                       anthropic.MessageDeltaUsage
		expectedInputTokens         int32
		expectedOutputTokens        int32
		expectedTotalTokens         int32
		expectedCachedTokens        uint32
		expectedCacheCreationTokens uint32
	}{
		{
			name: "message_delta event with final totals",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              250,
				OutputTokens:             120,
				CacheReadInputTokens:     30,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:         280, // 250 + 0 + 30
			expectedOutputTokens:        120,
			expectedTotalTokens:         400, // 280 + 120
			expectedCachedTokens:        30,  // 30
			expectedCacheCreationTokens: 0,
		},
		{
			name: "message_delta event with only output tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              0,
				OutputTokens:             85,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:         0,
			expectedOutputTokens:        85,
			expectedTotalTokens:         85,
			expectedCachedTokens:        0,
			expectedCacheCreationTokens: 0,
		},
		{
			name: "message_delta with cache creation tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     10,
				CacheCreationInputTokens: 5,
			},
			expectedInputTokens:         165, // 150 + 5 + 10
			expectedOutputTokens:        75,
			expectedTotalTokens:         240, // 165 + 75
			expectedCachedTokens:        10,  // 10
			expectedCacheCreationTokens: 5,   // 5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := metrics.ExtractTokenUsageFromAnthropic(tt.usage.InputTokens,
				tt.usage.OutputTokens,
				tt.usage.CacheReadInputTokens,
				tt.usage.CacheCreationInputTokens,
			)
			expected := tokenUsageFrom(tt.expectedInputTokens, int32(tt.expectedCachedTokens), int32(tt.expectedCacheCreationTokens), tt.expectedOutputTokens, tt.expectedTotalTokens) // nolint:gosec
			assert.Equal(t, expected, result)
		})
	}
}

// Test edge cases and boundary conditions.
func TestExtractLLMTokenUsage_EdgeCases(t *testing.T) {
	t.Run("negative values should be handled", func(t *testing.T) {
		// Note: In practice, the Anthropic API shouldn't return negative values,
		// but our function should handle them gracefully by casting to uint32.
		result := metrics.ExtractTokenUsageFromAnthropic(-10, -5, -2, -1)

		// Negative int64 values will wrap around when cast to uint32.
		// This test documents current behavior rather than prescribing it.
		// The exact values aren't important, just that it doesn't panic.
		assert.NotNil(t, result)
	})

	t.Run("maximum int64 values", func(t *testing.T) {
		// Test with very large values to ensure no overflow issues.
		// Note: This will result in truncation when casting to uint32.
		result := metrics.ExtractTokenUsageFromAnthropic(9223372036854775807, 1000, 500, 100)
		assert.NotNil(t, result)
	})
}

// Test that demonstrates the correct calculation according to Claude API docs.
func TestExtractLLMTokenUsage_ClaudeAPIDocumentationCompliance(t *testing.T) {
	t.Run("claude API documentation example", func(t *testing.T) {
		// This test verifies compliance with Claude API documentation:
		// "Total input tokens in a request is the summation of input_tokens,
		// cache_creation_input_tokens, and cache_read_input_tokens".

		inputTokens := int64(100)
		cachedWriteTokens := int64(20)
		cacheReadTokens := int64(30)
		outputTokens := int64(50)

		result := metrics.ExtractTokenUsageFromAnthropic(inputTokens, outputTokens, cacheReadTokens, cachedWriteTokens)

		// Total input should be sum of all input token types.
		expectedTotalInputInt := inputTokens + cachedWriteTokens + cacheReadTokens
		expectedTotalInput := uint32(expectedTotalInputInt) // #nosec G115 - test values are small and safe
		inputTokensVal, ok := result.InputTokens()
		assert.True(t, ok)
		assert.Equal(t, expectedTotalInput, inputTokensVal,
			"InputTokens should be sum of input_tokens + cache_creation_input_tokens + cache_read_input_tokens")

		cachedTokens, ok := result.CachedInputTokens()
		assert.True(t, ok)
		assert.Equal(t, uint32(cacheReadTokens), cachedTokens,
			"CachedInputTokens should be  cache_read_input_tokens")

		cacheCreationTokens, ok := result.CacheCreationInputTokens()
		assert.True(t, ok)
		assert.Equal(t, uint32(cachedWriteTokens), cacheCreationTokens,
			"CacheCreationInputTokens should be cache_creation_input_tokens")

		// Total tokens should be input + output.
		expectedTotal := expectedTotalInput + uint32(outputTokens)
		totalTokens, ok := result.TotalTokens()
		assert.True(t, ok)
		assert.Equal(t, expectedTotal, totalTokens,
			"TotalTokens should be InputTokens + OutputTokens")
	})
}

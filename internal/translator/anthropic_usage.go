// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import "github.com/anthropics/anthropic-sdk-go"

// ExtractLLMTokenUsage extracts the correct token usage from Anthropic API response.
// According to Claude API documentation, total input tokens is the summation of:
// input_tokens + cache_creation_input_tokens + cache_read_input_tokens
//
// This function works for both streaming and non-streaming responses by accepting
// the common usage fields that exist in all Anthropic usage structures.
func ExtractLLMTokenUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64) LLMTokenUsage {
	// Calculate total input tokens as per Anthropic API documentation
	totalInputTokens := inputTokens + cacheCreationTokens + cacheReadTokens

	// Cache tokens include both read and creation tokens
	totalCachedTokens := cacheReadTokens + cacheCreationTokens

	return LLMTokenUsage{
		InputTokens:       uint32(totalInputTokens),                //nolint:gosec
		OutputTokens:      uint32(outputTokens),                    //nolint:gosec
		TotalTokens:       uint32(totalInputTokens + outputTokens), //nolint:gosec
		CachedInputTokens: uint32(totalCachedTokens),               //nolint:gosec
	}
}

// ExtractLLMTokenUsageFromUsage extracts token usage from anthropic.Usage struct (non-streaming).
func ExtractLLMTokenUsageFromUsage(usage anthropic.Usage) LLMTokenUsage {
	return ExtractLLMTokenUsage(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadInputTokens,
		usage.CacheCreationInputTokens,
	)
}

// ExtractLLMTokenUsageFromDeltaUsage extracts token usage from streaming message_delta events.
func ExtractLLMTokenUsageFromDeltaUsage(usage anthropic.MessageDeltaUsage) LLMTokenUsage {
	return ExtractLLMTokenUsage(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadInputTokens,
		usage.CacheCreationInputTokens,
	)
}

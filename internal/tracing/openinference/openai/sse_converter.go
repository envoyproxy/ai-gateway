// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// convertSSEToJSON converts a complete SSE stream to a single JSON-encoded
// openai.ChatCompletionResponse. This will not serialize zero values including
// fields whose values are zero or empty, or nested objects where all fields
// have zero values.
//
// TODO: This can be refactored in "streaming" in stateful way without asking for all chunks at once.
// That would reduce a slice allocation for events.
func convertSSEToJSON(chunks []*openai.ChatCompletionResponseChunk) *openai.ChatCompletionResponse {

	var (
		firstChunk *openai.ChatCompletionResponseChunk
		content    strings.Builder
		usage      *openai.ChatCompletionResponseUsage
		role       string
	)

	for _, chunk := range chunks {
		if firstChunk == nil {
			firstChunk = chunk
		}

		// Accumulate content and role from delta (assuming single choice at index 0).
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			if chunk.Choices[0].Delta.Content != nil {
				content.WriteString(*chunk.Choices[0].Delta.Content)
			}
			if chunk.Choices[0].Delta.Role != "" {
				role = chunk.Choices[0].Delta.Role
			}
		}

		// Capture usage from the last chunk that has it.
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}

	// If no valid first chunk found, return a minimal response.
	if firstChunk == nil {
		return &openai.ChatCompletionResponse{
			ID:      "",
			Object:  "chat.completion.chunk",
			Created: openai.JSONUNIXTime{},
			Model:   "",
			Choices: []openai.ChatCompletionResponseChoice{{
				Index:        0,
				FinishReason: "stop",
				Message: openai.ChatCompletionResponseChoiceMessage{
					Role: role,
				},
			}},
		}
	}

	// Build the response as a chunk with accumulated content.
	contentStr := content.String()

	// Create a ChatCompletionResponse with all accumulated content.
	response := &openai.ChatCompletionResponse{
		ID:                firstChunk.ID,
		Object:            "chat.completion.chunk", // Keep chunk object type for streaming.
		Created:           firstChunk.Created,
		Model:             firstChunk.Model,
		ServiceTier:       firstChunk.ServiceTier,
		SystemFingerprint: firstChunk.SystemFingerprint,
		Choices: []openai.ChatCompletionResponseChoice{{
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:    role,
				Content: &contentStr,
			},
			Index:        0,
			FinishReason: "stop",
		}},
	}

	if usage != nil {
		response.Usage = *usage
	}

	return response
}

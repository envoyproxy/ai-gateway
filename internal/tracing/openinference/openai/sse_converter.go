// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

var (
	dataPrefix = []byte("data: ")
	doneSuffix = []byte("[DONE]")
)

// convertSSEToJSON converts a complete SSE stream to a single JSON-encoded
// openai.ChatCompletionResponse. This will not serialize zero values including
// fields whose values are zero or empty, or nested objects where all fields
// have zero values.
//
// This is optimized for BUFFERED mode where we receive the entire stream at once.
func convertSSEToJSON(sseData []byte) ([]byte, error) {
	if len(sseData) == 0 {
		return nil, nil
	}

	var (
		firstChunk   *openai.ChatCompletionResponseChunk
		content      strings.Builder
		usage        *openai.ChatCompletionResponseUsage
		annotations  []openai.Annotation
		role         string
		obfuscation  string
		finishReason openai.ChatCompletionChoicesFinishReason
	)

	// Split into lines assuming single-line data per event.
	lines := bytes.Split(sseData, []byte("\n"))

	for _, line := range lines {
		if len(line) == 0 || !bytes.HasPrefix(line, dataPrefix) {
			continue
		}

		data := line[len(dataPrefix):]

		if bytes.Equal(data, doneSuffix) {
			break
		}

		var chunk openai.ChatCompletionResponseChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk: %w", err)
		}

		if firstChunk == nil {
			firstChunk = &chunk
		}

		// Accumulate content, role, and annotations from delta (assuming single choice at index 0).
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta != nil {
				if chunk.Choices[0].Delta.Content != nil {
					content.WriteString(*chunk.Choices[0].Delta.Content)
				}
				if chunk.Choices[0].Delta.Role != "" {
					role = chunk.Choices[0].Delta.Role
				}
				if len(chunk.Choices[0].Delta.Annotations) > 0 {
					annotations = append(annotations, chunk.Choices[0].Delta.Annotations...)
				}
			}
			// Capture finish_reason from any chunk that has it.
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
		}

		// Capture usage from the last chunk that has it.
		if chunk.Usage != nil {
			usage = chunk.Usage
		}

		// Capture obfuscation from the last chunk that has it.
		if chunk.Obfuscation != "" {
			obfuscation = chunk.Obfuscation
		}
	}

	// If no valid first chunk found, return a minimal response.
	if firstChunk == nil {
		// Default to "stop" if no finish reason was captured.
		if finishReason == "" {
			finishReason = openai.ChatCompletionChoicesFinishReasonStop
		}
		return json.Marshal(openai.ChatCompletionResponse{
			ID:      "",
			Object:  "chat.completion.chunk",
			Created: openai.JSONUNIXTime{},
			Model:   "",
			Choices: []openai.ChatCompletionResponseChoice{{
				Index:        0,
				FinishReason: finishReason,
				Message: openai.ChatCompletionResponseChoiceMessage{
					Role: role,
				},
			}},
		})
	}

	// Build the response as a chunk with accumulated content.
	contentStr := content.String()

	// Default to "stop" if no finish reason was captured.
	if finishReason == "" {
		finishReason = openai.ChatCompletionChoicesFinishReasonStop
	}

	// Create a ChatCompletionResponse with all accumulated content.
	response := openai.ChatCompletionResponse{
		ID:                firstChunk.ID,
		Object:            "chat.completion.chunk", // Keep chunk object type for streaming.
		Created:           firstChunk.Created,
		Model:             firstChunk.Model,
		ServiceTier:       firstChunk.ServiceTier,
		SystemFingerprint: firstChunk.SystemFingerprint,
		Obfuscation:       obfuscation,
		Choices: []openai.ChatCompletionResponseChoice{{
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:        role,
				Content:     &contentStr,
				Annotations: annotations,
			},
			Index:        0,
			FinishReason: finishReason,
		}},
	}

	if usage != nil {
		response.Usage = *usage
	}

	return json.Marshal(response)
}

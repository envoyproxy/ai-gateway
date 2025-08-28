// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"strings"
	"time"

	openaigo "github.com/openai/openai-go"
	openAIconstant "github.com/openai/openai-go/shared/constant"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// convertSSEToJSON converts a complete SSE stream to a single JSON-encoded
// openai.ChatCompletionResponse. This will not serialize zero values including
// fields whose values are zero or empty, or nested objects where all fields
// have zero values.
//
// TODO: This can be refactored in "streaming" in stateful way without asking for all chunks at once.
// That would reduce a slice allocation for events.
// TODO Or, even better, we can make the chunk version of buildResponseAttributes which accepts a single
// openai.ChatCompletionResponseChunk one at a time, and then we won't need to accumulate all chunks
// in memory.
func convertSSEToJSON(chunks []*openai.ChatCompletionResponseChunk) *openaigo.ChatCompletion {
	var (
		firstChunk   *openai.ChatCompletionResponseChunk
		content      strings.Builder
		usage        *openai.ChatCompletionResponseUsage
		annotations  []openai.Annotation
		role         string
		obfuscation  string
		finishReason openaigo.CompletionChoiceFinishReason
	)

	for _, chunk := range chunks {
		if firstChunk == nil {
			firstChunk = chunk
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
				if as := chunk.Choices[0].Delta.Annotations; as != nil && len(*as) > 0 {
					annotations = append(annotations, *as...)
				}
			}
			// Capture finish_reason from any chunk that has it.
			if chunk.Choices[0].FinishReason != "" {
				finishReason = openaigo.CompletionChoiceFinishReason(chunk.Choices[0].FinishReason)
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
			finishReason = openaigo.CompletionChoiceFinishReasonStop
		}
		return &openaigo.ChatCompletion{
			ID:     "",
			Object: "chat.completion.chunk",
			Model:  "",
			Choices: []openaigo.ChatCompletionChoice{{
				Index:        0,
				FinishReason: string(finishReason),
				Message: openaigo.ChatCompletionMessage{
					Role: openAIconstant.Assistant(role),
				},
			}},
		}
	}

	// Build the response as a chunk with accumulated content.
	contentStr := content.String()

	// Default to "stop" if no finish reason was captured.
	if finishReason == "" {
		finishReason = openaigo.CompletionChoiceFinishReasonStop
	}

	var annotationsPtr []openaigo.ChatCompletionMessageAnnotation
	if len(annotations) > 0 {
		annotationsPtr = make([]openaigo.ChatCompletionMessageAnnotation, len(annotations))
		for i, a := range annotations {
			annotationsPtr[i] = openaigo.ChatCompletionMessageAnnotation{
				Type: openAIconstant.URLCitation(a.Type),
			}
			if a.URLCitation != nil {
				annotationsPtr[i].URLCitation = openaigo.ChatCompletionMessageAnnotationURLCitation{
					EndIndex:   int64(a.URLCitation.EndIndex),
					StartIndex: int64(a.URLCitation.StartIndex),
					Title:      a.URLCitation.Title,
					URL:        a.URLCitation.URL,
				}
			}
		}
	}

	// Create a ChatCompletion with all accumulated content.
	_ = obfuscation
	response := &openaigo.ChatCompletion{
		ID:                firstChunk.ID,
		Object:            "chat.completion.chunk", // Keep chunk object type for streaming.
		Created:           time.Time(firstChunk.Created).Unix(),
		Model:             firstChunk.Model,
		ServiceTier:       openaigo.ChatCompletionServiceTier(firstChunk.ServiceTier),
		SystemFingerprint: firstChunk.SystemFingerprint,
		// Obfuscation:       obfuscation,
		// TODO: obfuscation is not a response type - not a chat completion field. we should not include it.
		Choices: []openaigo.ChatCompletionChoice{{
			Message: openaigo.ChatCompletionMessage{
				Role:        openAIconstant.Assistant(role),
				Content:     contentStr,
				Annotations: annotationsPtr,
			},
			Index:        0,
			FinishReason: string(finishReason),
		}},
	}

	if usage != nil {
		// Convert usage to openaigo.ChatCompletionUsage for now,
		// but once the refactor to use openaigo natively is done, this conversion can be removed.
		response.Usage = openaigo.CompletionUsage{
			CompletionTokens: int64(usage.CompletionTokens),
			PromptTokens:     int64(usage.PromptTokens),
			TotalTokens:      int64(usage.TotalTokens),
		}

		if usage.CompletionTokensDetails != nil {
			response.Usage.CompletionTokensDetails = openaigo.CompletionUsageCompletionTokensDetails{
				AudioTokens:              int64(usage.CompletionTokensDetails.AudioTokens),
				ReasoningTokens:          int64(usage.CompletionTokensDetails.ReasoningTokens),
				AcceptedPredictionTokens: int64(usage.CompletionTokensDetails.AcceptedPredictionTokens),
				RejectedPredictionTokens: int64(usage.CompletionTokensDetails.RejectedPredictionTokens),
			}
		}
		if usage.PromptTokensDetails != nil {
			response.Usage.PromptTokensDetails = openaigo.CompletionUsagePromptTokensDetails{
				AudioTokens:  int64(usage.PromptTokensDetails.AudioTokens),
				CachedTokens: int64(usage.PromptTokensDetails.CachedTokens),
			}
		}
	}

	return response
}

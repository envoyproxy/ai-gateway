// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/base64"
	"encoding/binary"
	"math"

	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func buildResponseAttributes(resp *openai.ChatCompletionResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, resp.Model),
	}

	if !config.HideOutputs {
		attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))
	}

	// Note: compound match here is from Python OpenInference OpenAI config.py.
	if !config.HideOutputs && !config.HideOutputMessages {
		for i, choice := range resp.Choices {
			attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageRole), choice.Message.Role))

			if choice.Message.Content != nil && *choice.Message.Content != "" {
				content := *choice.Message.Content
				if config.HideOutputText {
					content = openinference.RedactedValue
				}
				attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageContent), content))
			}

			for j, toolCall := range choice.Message.ToolCalls {
				if toolCall.ID != nil {
					attrs = append(attrs, attribute.String(openinference.OutputMessageToolCallAttribute(i, j, openinference.ToolCallID), *toolCall.ID))
				}
				attrs = append(attrs,
					attribute.String(openinference.OutputMessageToolCallAttribute(i, j, openinference.ToolCallFunctionName), toolCall.Function.Name),
					attribute.String(openinference.OutputMessageToolCallAttribute(i, j, openinference.ToolCallFunctionArguments), toolCall.Function.Arguments),
				)
			}
		}
	}

	// Token counts are considered metadata and are still included even when output content is hidden.
	u := resp.Usage
	if pt := u.PromptTokens; pt > 0 {
		attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPrompt, pt))
		if td := resp.Usage.PromptTokensDetails; td != nil {
			attrs = append(attrs,
				attribute.Int(openinference.LLMTokenCountPromptAudio, td.AudioTokens),
				attribute.Int(openinference.LLMTokenCountPromptCacheHit, td.CachedTokens),
				attribute.Int(openinference.LLMTokenCountPromptCacheWrite, td.CachedCreationTokens),
			)
		}
	}
	if ct := u.CompletionTokens; ct > 0 {
		attrs = append(attrs, attribute.Int(openinference.LLMTokenCountCompletion, ct))
		if td := resp.Usage.CompletionTokensDetails; td != nil {
			attrs = append(attrs,
				attribute.Int(openinference.LLMTokenCountCompletionAudio, td.AudioTokens),
				attribute.Int(openinference.LLMTokenCountCompletionReasoning, td.ReasoningTokens),
			)
		}
	}
	if tt := u.TotalTokens; tt > 0 {
		attrs = append(attrs, attribute.Int(openinference.LLMTokenCountTotal, tt))
	}
	return attrs
}

func buildEmbeddingsResponseAttributes(resp *openai.EmbeddingResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// Add the model name for successful responses.
	attrs = append(attrs, attribute.String(openinference.EmbeddingModelName, resp.Model))

	if !config.HideOutputs {
		attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))

		// Record embedding vectors as float arrays.
		// Per OpenInference spec: base64-encoded vectors MUST be decoded to float arrays.
		if !config.HideEmbeddingsVectors {
			for i, data := range resp.Data {
				switch v := data.Embedding.Value.(type) {
				case []float64:
					if len(v) > 0 {
						attrs = append(attrs, attribute.Float64Slice(openinference.EmbeddingVectorAttribute(i), v))
					}
				case string:
					// Decode base64-encoded embeddings to float arrays.
					if floats, err := decodeBase64Embeddings(v); err == nil && len(floats) > 0 {
						attrs = append(attrs, attribute.Float64Slice(openinference.EmbeddingVectorAttribute(i), floats))
					}
				}
			}
		}
	}

	// Token counts are considered metadata and are still included even when output content is hidden.
	if pt := resp.Usage.PromptTokens; pt > 0 {
		attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPrompt, pt))
	}
	if tt := resp.Usage.TotalTokens; tt > 0 {
		attrs = append(attrs, attribute.Int(openinference.LLMTokenCountTotal, tt))
	}
	return attrs
}

// decodeBase64Embeddings decodes a base64-encoded embedding vector to []float64.
// OpenAI returns base64-encoded little-endian float32 arrays when encoding_format="base64".
func decodeBase64Embeddings(encoded string) ([]float64, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	// Each float32 is 4 bytes
	numFloats := len(decoded) / 4
	result := make([]float64, numFloats)

	for i := 0; i < numFloats; i++ {
		bits := binary.LittleEndian.Uint32(decoded[i*4 : (i+1)*4])
		result[i] = float64(math.Float32frombits(bits))
	}

	return result, nil
}

// buildCompletionResponseAttributes builds OpenInference attributes from the completions response.
func buildCompletionResponseAttributes(resp *openai.CompletionResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, resp.Model),
	}

	if !config.HideOutputs {
		attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))
	}

	// Handle choices using indexed attribute format.
	// Per OpenInference spec, we record completion text for each choice.
	if !config.HideOutputs && !config.HideChoices {
		for i, choice := range resp.Choices {
			text := choice.Text
			attrs = append(attrs, attribute.String(openinference.ChoiceTextAttribute(i), text))
		}
	}

	// Token counts are considered metadata and are still included even when output content is hidden.
	u := resp.Usage
	if u != nil {
		if pt := u.PromptTokens; pt > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPrompt, pt))
		}
		if ct := u.CompletionTokens; ct > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountCompletion, ct))
		}
		if tt := u.TotalTokens; tt > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountTotal, tt))
		}
	}

	return attrs
}

// buildResponsesResponseAttributes builds OpenTelemetry attributes for responses responses.
func buildResponsesResponseAttributes(resp *openai.Response, _ *openinference.TraceConfig) []attribute.KeyValue {
	// TODO: Add more detailed response attributes
	var attrs []attribute.KeyValue

	if resp.Model != "" {
		attrs = append(attrs, attribute.String(openinference.LLMModelName, resp.Model))
	}

	// Add token usage if available
	if resp.Usage != nil {
		if resp.Usage.InputTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPrompt, int(resp.Usage.InputTokens)))
		}
		if resp.Usage.OutputTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountCompletion, int(resp.Usage.OutputTokens)))
		}
		if resp.Usage.TotalTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountTotal, int(resp.Usage.TotalTokens)))
		}
		if resp.Usage.InputTokensDetails.CachedTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPromptCacheHit, int(resp.Usage.InputTokensDetails.CachedTokens)))
		}
		if resp.Usage.InputTokensDetails.CachedCreationTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPromptCacheWrite, int(resp.Usage.InputTokensDetails.CachedCreationTokens)))
		}
	}

	return attrs
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func buildResponseAttributes(resp *openai.ChatCompletionResponse) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(LLMModelName, resp.Model),
		attribute.String(OutputMimeType, MimeTypeJSON),
	}

	// Add output messages.
	for i, choice := range resp.Choices {
		attrs = append(attrs, attribute.String(OutputMessageAttribute(i, MessageRole), choice.Message.Role))

		if choice.Message.Content != nil && *choice.Message.Content != "" {
			attrs = append(attrs, attribute.String(OutputMessageAttribute(i, MessageContent), *choice.Message.Content))
		}

		// Add tool calls if present.
		for j, toolCall := range choice.Message.ToolCalls {
			if toolCall.ID != nil {
				attrs = append(attrs, attribute.String(OutputMessageToolCallAttribute(i, j, ToolCallID), *toolCall.ID))
			}
			attrs = append(attrs,
				attribute.String(OutputMessageToolCallAttribute(i, j, ToolCallFunctionName), toolCall.Function.Name),
				attribute.String(OutputMessageToolCallAttribute(i, j, ToolCallFunctionArguments), toolCall.Function.Arguments),
			)
		}
	}

	// Add token counts if available.
	u := resp.Usage
	if pt := u.PromptTokens; pt > 0 {
		attrs = append(attrs, attribute.Int(LLMTokenCountPrompt, pt))
		if td := resp.Usage.PromptTokensDetails; td != nil {
			attrs = append(attrs,
				attribute.Int(LLMTokenCountPromptAudio, td.AudioTokens),
				attribute.Int(LLMTokenCountPromptCacheHit, td.CachedTokens),
			)
		}
	}
	if ct := u.CompletionTokens; ct > 0 {
		attrs = append(attrs, attribute.Int(LLMTokenCountCompletion, ct))
		if td := resp.Usage.CompletionTokensDetails; td != nil {
			attrs = append(attrs,
				attribute.Int(LLMTokenCountCompletionAudio, td.AudioTokens),
				attribute.Int(LLMTokenCountCompletionReasoning, td.ReasoningTokens),
			)
		}
	}
	if tt := u.TotalTokens; tt > 0 {
		attrs = append(attrs, attribute.Int(LLMTokenCountTotal, tt))
	}
	return attrs
}

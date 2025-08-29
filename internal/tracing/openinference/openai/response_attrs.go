// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func buildResponseAttributes(resp *openai.CustomChatCompletion, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, resp.Model),
	}

	if !config.HideOutputs {
		attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))
	}

	// Note: compound match here is from Python OpenInference OpenAI config.py.
	if !config.HideOutputs && !config.HideOutputMessages {
		for i, choice := range resp.Choices {
			attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageRole), string(choice.Message.Role)))

			if choice.Message.Content != "" {
				content := choice.Message.Content
				if config.HideOutputText {
					content = openinference.RedactedValue
				}
				attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageContent), content))
			}

			for j, toolCall := range choice.Message.ToolCalls {
				if toolCall.ID != "" {
					attrs = append(attrs, attribute.String(openinference.OutputMessageToolCallAttribute(i, j, openinference.ToolCallID), toolCall.ID))
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
		attrs = append(attrs, attribute.Int64(openinference.LLMTokenCountPrompt, pt))
		td := resp.Usage.PromptTokensDetails
		attrs = append(attrs,
			attribute.Int64(openinference.LLMTokenCountPromptAudio, td.AudioTokens),
			attribute.Int64(openinference.LLMTokenCountPromptCacheHit, td.CachedTokens),
		)
	}
	if ct := u.CompletionTokens; ct > 0 {
		attrs = append(attrs, attribute.Int64(openinference.LLMTokenCountCompletion, ct))
		td := resp.Usage.CompletionTokensDetails
		attrs = append(attrs,
			attribute.Int64(openinference.LLMTokenCountCompletionAudio, td.AudioTokens),
			attribute.Int64(openinference.LLMTokenCountCompletionReasoning, td.ReasoningTokens),
		)
	}
	if tt := u.TotalTokens; tt > 0 {
		attrs = append(attrs, attribute.Int64(openinference.LLMTokenCountTotal, tt))
	}
	return attrs
}

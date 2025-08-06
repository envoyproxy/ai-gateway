// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// llmInvocationParameters is the representation of LLMInvocationParameters,
// which includes all parameters except messages and tools, which have their
// own attributes.
// See: openinference-instrumentation-openai _request_attributes_extractor.py.
type llmInvocationParameters struct {
	openai.ChatCompletionRequest
	Messages []openai.ChatCompletionMessageParamUnion `json:"messages,omitempty"`
	Tools    []openai.Tool                            `json:"tools,omitempty"`
}

// buildRequestAttributes builds OpenInference attributes from the request.
func buildRequestAttributes(chatRequest *openai.ChatCompletionRequest, body string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(SpanKind, SpanKindLLM),
		attribute.String(LLMSystem, LLMSystemOpenAI),
		attribute.String(LLMModelName, chatRequest.Model),
		attribute.String(InputValue, body),
		attribute.String(InputMimeType, MimeTypeJSON),
	}

	// Add the invocation parameters without messages and tools.
	if invocationParamsJSON, err := json.Marshal(llmInvocationParameters{
		ChatCompletionRequest: *chatRequest,
	}); err == nil {
		attrs = append(attrs, attribute.String(LLMInvocationParameters, string(invocationParamsJSON)))
	}

	// Add indexed attributes for each message.
	for i, msg := range chatRequest.Messages {
		role := msg.Type
		attrs = append(attrs, attribute.String(InputMessageAttribute(i, MessageRole), role))

		// Handle different message content types.
		switch v := msg.Value.(type) {
		case openai.ChatCompletionUserMessageParam:
			if v.Content.Value != nil {
				switch content := v.Content.Value.(type) {
				case string:
					if content != "" {
						attrs = append(attrs, attribute.String(InputMessageAttribute(i, MessageContent), content))
					}
				case []openai.ChatCompletionContentPartUserUnionParam:
					// Handle multimodal content.
					for j, part := range content {
						switch {
						case part.TextContent != nil:
							textKey := InputMessageContentAttribute(i, j, "text")
							typeKey := InputMessageContentAttribute(i, j, "type")
							attrs = append(attrs,
								attribute.String(textKey, part.TextContent.Text),
								attribute.String(typeKey, "text"),
							)
						case part.ImageContent != nil && part.ImageContent.ImageURL.URL != "":
							urlKey := InputMessageContentAttribute(i, j, "image.image.url")
							typeKey := InputMessageContentAttribute(i, j, "type")
							attrs = append(attrs,
								attribute.String(urlKey, part.ImageContent.ImageURL.URL),
								attribute.String(typeKey, "image"),
							)
						case part.InputAudioContent != nil:
							// Skip recording audio content attributes to match Python OpenInference behavior.
							// Audio data is already included in input.value as part of the full request.
						}
					}
				}
			}
		default:
			// For other message types, use the simple extraction.
			content := extractMessageContent(msg)
			if content != "" {
				key := InputMessageAttribute(i, MessageContent)
				attrs = append(attrs, attribute.String(key, content))
			}
		}
	}

	// Add indexed attributes for each tool.
	for i, tool := range chatRequest.Tools {
		if toolJSON, err := json.Marshal(tool); err == nil {
			attrs = append(attrs,
				attribute.String(fmt.Sprintf("%s.%d.tool.json_schema", LLMTools, i), string(toolJSON)),
			)
		}
	}

	return attrs
}

// extractMessageContent extracts content from OpenAI message union types.
func extractMessageContent(msg openai.ChatCompletionMessageParamUnion) string {
	switch v := msg.Value.(type) {
	case openai.ChatCompletionUserMessageParam:
		if v.Content.Value == nil {
			return ""
		}
		if content, ok := v.Content.Value.(string); ok {
			return content
		}
		return "[complex content]"
	case openai.ChatCompletionAssistantMessageParam:
		if v.Content.Value == nil {
			return ""
		}
		if content, ok := v.Content.Value.(string); ok {
			return content
		}
		return "[assistant message]"
	case openai.ChatCompletionSystemMessageParam:
		if v.Content.Value == nil {
			return ""
		}
		if content, ok := v.Content.Value.(string); ok {
			return content
		}
		return "[system message]"
	case openai.ChatCompletionDeveloperMessageParam:
		if v.Content.Value == nil {
			return ""
		}
		if content, ok := v.Content.Value.(string); ok {
			return content
		}
		return "[developer message]"
	case openai.ChatCompletionToolMessageParam:
		if v.Content.Value == nil {
			return ""
		}
		if content, ok := v.Content.Value.(string); ok {
			return content
		}
		return "[tool content]"
	default:
		return "[unknown message type]"
	}
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
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
func buildRequestAttributes(chatRequest *openai.ChatCompletionRequest, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
		attribute.String(openinference.LLMModelName, chatRequest.Model),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs, attribute.String(openinference.InputValue, body))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	if !config.HideLLMInvocationParameters {
		if invocationParamsJSON, err := json.Marshal(llmInvocationParameters{
			ChatCompletionRequest: *chatRequest,
		}); err == nil {
			attrs = append(attrs, attribute.String(openinference.LLMInvocationParameters, string(invocationParamsJSON)))
		}
	}

	// Note: compound match here is from Python OpenInference OpenAI config.py.
	if !config.HideInputs && !config.HideInputMessages {
		for i, msg := range chatRequest.Messages {
			role := msg.Type
			attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageRole), role))

			switch v := msg.Value.(type) {
			case openai.ChatCompletionUserMessageParam:
				if v.Content.Value != nil {
					switch content := v.Content.Value.(type) {
					case string:
						if content != "" {
							if config.HideInputText {
								content = openinference.RedactedValue
							}
							attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageContent), content))
						}
					case []openai.ChatCompletionContentPartUserUnionParam:
						for j, part := range content {
							switch {
							case part.TextContent != nil:
								text := part.TextContent.Text
								if config.HideInputText {
									text = openinference.RedactedValue
								}
								attrs = append(attrs,
									attribute.String(openinference.InputMessageContentAttribute(i, j, "text"), text),
									attribute.String(openinference.InputMessageContentAttribute(i, j, "type"), "text"),
								)
							case part.ImageContent != nil && part.ImageContent.ImageURL.URL != "":
								if !config.HideInputImages {
									urlKey := openinference.InputMessageContentAttribute(i, j, "image.image.url")
									typeKey := openinference.InputMessageContentAttribute(i, j, "type")
									url := part.ImageContent.ImageURL.URL
									if isBase64URL(url) && len(url) > config.Base64ImageMaxLength {
										url = openinference.RedactedValue
									}
									attrs = append(attrs,
										attribute.String(urlKey, url),
										attribute.String(typeKey, "image"),
									)
								}
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
					if config.HideInputText {
						content = openinference.RedactedValue
					}
					attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageContent), content))
				}
			}
		}
	}

	// Add indexed attributes for each tool.
	for i, tool := range chatRequest.Tools {
		if toolJSON, err := json.Marshal(tool); err == nil {
			attrs = append(attrs,
				attribute.String(fmt.Sprintf("%s.%d.tool.json_schema", openinference.LLMTools, i), string(toolJSON)),
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

// isBase64URL checks if a string is a base64-encoded image URL.
// See: https://github.com/Arize-ai/openinference/blob/main/python/openinference-instrumentation/src/openinference/instrumentation/config.py#L339
func isBase64URL(url string) bool {
	return strings.HasPrefix(url, "data:image/") && strings.Contains(url, "base64")
}

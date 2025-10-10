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
			role := msg.ExtractMessgaeRole()
			attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageRole), role))

			if msg.OfUser != nil {
				switch content := msg.OfUser.Content.Value.(type) {
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
						case part.OfText != nil:
							text := part.OfText.Text
							if config.HideInputText {
								text = openinference.RedactedValue
							}
							attrs = append(attrs,
								attribute.String(openinference.InputMessageContentAttribute(i, j, "text"), text),
								attribute.String(openinference.InputMessageContentAttribute(i, j, "type"), "text"),
							)
						case part.OfImageURL != nil && part.OfImageURL.ImageURL.URL != "":
							if !config.HideInputImages {
								urlKey := openinference.InputMessageContentAttribute(i, j, "image.image.url")
								typeKey := openinference.InputMessageContentAttribute(i, j, "type")
								url := part.OfImageURL.ImageURL.URL
								if isBase64URL(url) && len(url) > config.Base64ImageMaxLength {
									url = openinference.RedactedValue
								}
								attrs = append(attrs,
									attribute.String(urlKey, url),
									attribute.String(typeKey, "image"),
								)
							}
						case part.OfInputAudio != nil:
							// Skip recording audio content attributes to match Python OpenInference behavior.
							// Audio data is already included in input.value as part of the full request.
						case part.OfFile != nil:
							// TODO: skip file content for now.
						}
					}
				}
			} else {
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
	switch {
	case msg.OfUser != nil:
		content := msg.OfUser.Content
		if content.Value == nil {
			return ""
		}
		if content, ok := content.Value.(string); ok {
			return content
		}
		return "[complex content]"
	case msg.OfAssistant != nil:
		content := msg.OfAssistant.Content

		if content.Value == nil {
			return ""
		}
		if content, ok := content.Value.(string); ok {
			return content
		}
		return "[assistant message]"
	case msg.OfSystem != nil:
		content := msg.OfSystem.Content
		if content.Value == nil {
			return ""
		}
		if content, ok := content.Value.(string); ok {
			return content
		}
		return "[system message]"
	case msg.OfDeveloper != nil:
		content := msg.OfDeveloper.Content
		if content.Value == nil {
			return ""
		}
		if content, ok := content.Value.(string); ok {
			return content
		}
		return "[developer message]"
	case msg.OfTool != nil:
		content := msg.OfTool.Content
		if content.Value == nil {
			return ""
		}
		if content, ok := content.Value.(string); ok {
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

// embeddingsInvocationParameters is the representation of LLMInvocationParameters
// for embeddings, which includes all parameters except input.
type embeddingsInvocationParameters struct {
	Model          string  `json:"model"`
	EncodingFormat *string `json:"encoding_format,omitempty"`
	Dimensions     *int    `json:"dimensions,omitempty"`
	User           *string `json:"user,omitempty"`
}

// buildEmbeddingsRequestAttributes builds OpenInference attributes from the embeddings request.
func buildEmbeddingsRequestAttributes(embRequest *openai.EmbeddingRequest, body []byte, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
		attribute.String(openinference.SpanKind, openinference.SpanKindEmbedding),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs, attribute.String(openinference.InputValue, string(body)))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	if !config.HideLLMInvocationParameters {
		params := embeddingsInvocationParameters{
			Model:          embRequest.Model,
			EncodingFormat: embRequest.EncodingFormat,
			Dimensions:     embRequest.Dimensions,
			User:           embRequest.User,
		}
		if invocationParamsJSON, err := json.Marshal(params); err == nil {
			attrs = append(attrs, attribute.String(openinference.EmbeddingInvocationParameters, string(invocationParamsJSON)))
		}
	}

	// Record embedding text attributes for string inputs only.
	// We don't decode numeric tokens to text because:
	// 1. OpenAI-compatible backends may use different tokenizers (Ollama, LocalAI, etc.)
	// 2. The same token IDs mean different things in different tokenizers
	// 3. It would require model-specific tokenizer libraries (tiktoken, sentencepiece, etc.)
	// 4. Azure deployments don't affect this (they only host OpenAI models with cl100k_base)
	// Following OpenInference spec guidance to only record human-readable text.
	if !config.HideInputs && !config.HideEmbeddingsText {
		switch input := embRequest.Input.Value.(type) {
		case string:
			attrs = append(attrs, attribute.String(openinference.EmbeddingTextAttribute(0), input))
		case []string:
			for i, text := range input {
				attrs = append(attrs, attribute.String(openinference.EmbeddingTextAttribute(i), text))
			}
		// Token inputs are not recorded to reduce span size.
		case []int64:
		case [][]int64:
		}
	}

	return attrs
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

var (
	basicReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
				Role:    openai.ChatMessageRoleUser,
			},
		}},
	}
	basicReqBody = mustJSON(basicReq)

	// Multimodal request with text and image.
	multimodalReq = &openai.ChatCompletionRequest{
		Model:     openai.ModelGPT41Nano,
		MaxTokens: ptr(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{TextContent: &openai.ChatCompletionContentPartTextParam{
							Text: "What is in this image?",
							Type: "text",
						}},
						{ImageContent: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
							},
							Type: "image_url",
						}},
					},
				},
			},
		}},
	}
	multimodalReqBody = mustJSON(multimodalReq)

	// Request with tools.
	toolsReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "What is the weather like in Boston today?"},
			},
		}},
		ToolChoice: "auto",
		Tools: []openai.Tool{{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        "get_current_weather",
				Description: "Get the current weather in a given location",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type":        "string",
							"description": "The city and state, e.g. San Francisco, CA",
						},
						"unit": map[string]interface{}{
							"type": "string",
							"enum": []string{"celsius", "fahrenheit"},
						},
					},
					"required": []string{"location"},
				},
			},
		}},
	}
	toolsReqBody = mustJSON(toolsReq)

	// Request with audio content.
	audioReq = &openai.ChatCompletionRequest{
		Model: "gpt-4o-audio-preview",
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{TextContent: &openai.ChatCompletionContentPartTextParam{
							Text: "Answer in up to 5 words: What do you hear in this audio?",
							Type: "text",
						}},
						{InputAudioContent: &openai.ChatCompletionContentPartInputAudioParam{
							InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
								Data:   "REDACTED_BASE64_AUDIO_DATA",
								Format: "wav",
							},
							Type: "input_audio",
						}},
					},
				},
			},
		}},
	}
	audioReqBody = mustJSON(audioReq)

	// Request with JSON mode.
	jsonModeReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Generate a JSON object with three properties: name, age, and city."},
			},
		}},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: "json_object",
		},
	}
	jsonModeReqBody = mustJSON(jsonModeReq)

	// Request with system message.
	systemMessageReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{
					Role:    openai.ChatMessageRoleSystem,
					Content: openai.StringOrArray{Value: "You are a helpful assistant."},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
				},
			},
		},
	}
	systemMessageReqBody = mustJSON(systemMessageReq)

	// Request with empty tool array.
	emptyToolsReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
			},
		}},
		Tools: []openai.Tool{},
	}
	emptyToolsReqBody = mustJSON(emptyToolsReq)

	// Request with tool message.
	toolMessageReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
				},
			},
			{
				Type: openai.ChatMessageRoleAssistant,
				Value: openai.ChatCompletionAssistantMessageParam{
					Role:    openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{Value: nil},
					ToolCalls: []openai.ChatCompletionMessageToolCallParam{{
						ID:   ptr("call_123"),
						Type: "function",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location": "NYC"}`,
						},
					}},
				},
			},
			{
				Type: openai.ChatMessageRoleTool,
				Value: openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_123",
					Content:    openai.StringOrArray{Value: "Sunny, 72°F"},
				},
			},
		},
	}
	toolMessageReqBody = mustJSON(toolMessageReq)

	// Request with empty image URL.
	emptyImageURLReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{TextContent: &openai.ChatCompletionContentPartTextParam{
							Text: "What is this?",
							Type: "text",
						}},
						{ImageContent: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "", // Empty URL.
							},
							Type: "image_url",
						}},
					},
				},
			},
		}},
	}
	emptyImageURLReqBody = mustJSON(emptyImageURLReq)

	// Request with empty user content.
	emptyContentReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			Type: openai.ChatMessageRoleUser,
			Value: openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: ""},
			},
		}},
	}
	emptyContentReqBody = mustJSON(emptyContentReq)
)

func TestBuildRequestAttributes(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ChatCompletionRequest
		reqBody       string
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicReq,
			reqBody: string(basicReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(basicReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "Hello!"),
			},
		},
		{
			name:    "multimodal request with text and image",
			req:     multimodalReq,
			reqBody: string(multimodalReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(multimodalReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano","max_tokens":100}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageContentAttribute(0, 0, "text"), "What is in this image?"),
				attribute.String(InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(InputMessageContentAttribute(0, 1, "image.image.url"), "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"),
				attribute.String(InputMessageContentAttribute(0, 1, "type"), "image"),
			},
		},
		{
			name:    "request with tools",
			req:     toolsReq,
			reqBody: string(toolsReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(toolsReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano","tool_choice":"auto"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "What is the weather like in Boston today?"),
				attribute.String("llm.tools.0.tool.json_schema", `{"type":"function","function":{"name":"get_current_weather","description":"Get the current weather in a given location","parameters":{"properties":{"location":{"description":"The city and state, e.g. San Francisco, CA","type":"string"},"unit":{"enum":["celsius","fahrenheit"],"type":"string"}},"required":["location"],"type":"object"}}}`),
			},
		},
		{
			name:    "request with audio content",
			req:     audioReq,
			reqBody: string(audioReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, "gpt-4o-audio-preview"),
				attribute.String(InputValue, string(audioReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4o-audio-preview"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageContentAttribute(0, 0, "text"), "Answer in up to 5 words: What do you hear in this audio?"),
				attribute.String(InputMessageContentAttribute(0, 0, "type"), "text"),
				// Audio content is skipped to match Python OpenInference behavior.
			},
		},
		{
			name:    "request with JSON mode",
			req:     jsonModeReq,
			reqBody: string(jsonModeReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(jsonModeReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano","response_format":{"type":"json_object"}}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "Generate a JSON object with three properties: name, age, and city."),
			},
		},
		{
			name:    "request with system message",
			req:     systemMessageReq,
			reqBody: string(systemMessageReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(systemMessageReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleSystem),
				attribute.String(InputMessageAttribute(0, MessageContent), "You are a helpful assistant."),
				attribute.String(InputMessageAttribute(1, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(1, MessageContent), "Hello!"),
			},
		},
		{
			name:    "request with empty tool array",
			req:     emptyToolsReq,
			reqBody: string(emptyToolsReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(emptyToolsReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "Hello!"),
			},
		},
		{
			name:    "request with tool message",
			req:     toolMessageReq,
			reqBody: string(toolMessageReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(toolMessageReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "What's the weather?"),
				attribute.String(InputMessageAttribute(1, MessageRole), openai.ChatMessageRoleAssistant),
				// Assistant message with nil content is skipped.
				attribute.String(InputMessageAttribute(2, MessageRole), openai.ChatMessageRoleTool),
				attribute.String(InputMessageAttribute(2, MessageContent), "Sunny, 72°F"),
			},
		},
		{
			name:    "request with multimodal content - empty image URL",
			req:     emptyImageURLReq,
			reqBody: string(emptyImageURLReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(emptyImageURLReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageContentAttribute(0, 0, "text"), "What is this?"),
				attribute.String(InputMessageContentAttribute(0, 0, "type"), "text"),
				// Image with empty URL is skipped..
			},
		},
		{
			name:    "request with user message empty content",
			req:     emptyContentReq,
			reqBody: string(emptyContentReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(emptyContentReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				// Empty content is skipped..
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildRequestAttributes(tt.req, tt.reqBody)

			requireAttributesEqual(t, tt.expectedAttrs, attrs)
		})
	}
}

func TestExtractMessageContent(t *testing.T) {
	tests := []struct {
		name     string
		msg      openai.ChatCompletionMessageParamUnion
		expected string
	}{
		// User message tests.
		{
			name: "user message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello, how are you?",
					},
				},
			},
			expected: "Hello, how are you?",
		},
		{
			name: "user message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: nil,
					},
				},
			},
			expected: "",
		},
		{
			name: "user message with complex content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "Part 1"}},
							{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "Part 2"}},
						},
					},
				},
			},
			expected: "[complex content]",
		},
		// Assistant message tests.
		{
			name: "assistant message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleAssistant,
				Value: openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: "I'm doing well, thank you!",
					},
				},
			},
			expected: "I'm doing well, thank you!",
		},
		{
			name: "assistant message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleAssistant,
				Value: openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: nil,
					},
				},
			},
			expected: "",
		},
		// System message tests.
		{
			name: "system message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{
					Content: openai.StringOrArray{
						Value: "You are a helpful assistant.",
					},
				},
			},
			expected: "You are a helpful assistant.",
		},
		// Developer message tests.
		{
			name: "developer message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleDeveloper,
				Value: openai.ChatCompletionDeveloperMessageParam{
					Content: openai.StringOrArray{
						Value: "Internal developer note",
					},
				},
			},
			expected: "Internal developer note",
		},
		// Tool message tests.
		{
			name: "tool message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleTool,
				Value: openai.ChatCompletionToolMessageParam{
					Content: openai.StringOrArray{
						Value: "Tool response content",
					},
				},
			},
			expected: "Tool response content",
		},
		{
			name: "assistant message with empty string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: "assistant",
				Value: openai.ChatCompletionAssistantMessageParam{
					Role:    "assistant",
					Content: openai.StringOrAssistantRoleContentUnion{Value: ""},
				},
			},
			expected: "",
		},
		{
			name: "assistant message with non-string content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleAssistant,
				Value: openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: []openai.ChatCompletionAssistantMessageParamContent{
							{Type: "text", Text: ptr("Part 1")},
						},
					},
				},
			},
			expected: "[assistant message]",
		},
		{
			name: "system message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{
					Content: openai.StringOrArray{Value: nil},
				},
			},
			expected: "",
		},
		{
			name: "system message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{
					Content: openai.StringOrArray{Value: []string{"instruction1", "instruction2"}},
				},
			},
			expected: "[system message]",
		},
		{
			name: "developer message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleDeveloper,
				Value: openai.ChatCompletionDeveloperMessageParam{
					Content: openai.StringOrArray{Value: nil},
				},
			},
			expected: "",
		},
		{
			name: "developer message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: "developer",
				Value: openai.ChatCompletionDeveloperMessageParam{
					Role:    "developer",
					Content: openai.StringOrArray{Value: []string{"instruction1", "instruction2"}},
				},
			},
			expected: "[developer message]",
		},
		{
			name: "tool message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: openai.ChatMessageRoleTool,
				Value: openai.ChatCompletionToolMessageParam{
					Content: openai.StringOrArray{Value: nil},
				},
			},
			expected: "",
		},
		{
			name: "tool message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				Type: "tool",
				Value: openai.ChatCompletionToolMessageParam{
					Role:       "tool",
					ToolCallID: "call_123",
					Content:    openai.StringOrArray{Value: []string{"result1", "result2"}},
				},
			},
			expected: "[tool content]",
		},
		// Unknown message type.
		{
			name: "unknown message type",
			msg: openai.ChatCompletionMessageParamUnion{
				Type:  "unknown",
				Value: "some unknown value",
			},
			expected: "[unknown message type]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMessageContent(tt.msg)
			require.Equal(t, tt.expected, result)
		})
	}
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

var (
	basicReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
				Role:    openai.ChatMessageRoleUser,
			},
		}},
	}
	basicReqBody = mustJSON(basicReq)

	streamingReq = func() *openai.ChatCompletionRequest {
		streamingReq := *basicReq
		streamingReq.Stream = true
		return &streamingReq
	}()
	streamingReqBody = mustJSON(streamingReq)

	// Multimodal request with text and image.
	multimodalReq = &openai.ChatCompletionRequest{
		Model:     openai.ModelGPT5Nano,
		MaxTokens: ptr(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{OfText: &openai.ChatCompletionContentPartTextParam{
							Text: "What is in this image?",
							Type: "text",
						}},
						{OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "What is the weather like in Boston today?"},
			},
		}},
		ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
		Tools: []openai.Tool{{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        "get_current_weather",
				Description: "Get the current weather in a given location",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "The city and state, e.g. San Francisco, CA",
						},
						"unit": map[string]any{
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
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{OfText: &openai.ChatCompletionContentPartTextParam{
							Text: "Answer in up to 5 words: What do you hear in this audio?",
							Type: "text",
						}},
						{OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
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
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Generate a JSON object with three properties: name, age, and city."},
			},
		}},
		ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
			OfJSONObject: &openai.ChatCompletionResponseFormatJSONObjectParam{
				Type: "json_object",
			},
		},
	}
	jsonModeReqBody = mustJSON(jsonModeReq)

	// Request with system message.
	systemMessageReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role:    openai.ChatMessageRoleSystem,
					Content: openai.ContentUnion{Value: "You are a helpful assistant."},
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
				},
			},
		},
	}
	systemMessageReqBody = mustJSON(systemMessageReq)

	// Request with empty tool array.
	emptyToolsReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
			},
		}},
		Tools: []openai.Tool{},
	}
	emptyToolsReqBody = mustJSON(emptyToolsReq)

	// Request with tool message.
	toolMessageReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
				},
			},
			{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
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
				OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_123",
					Content:    openai.ContentUnion{Value: "Sunny, 72°F"},
				},
			},
		},
	}
	toolMessageReqBody = mustJSON(toolMessageReq)

	// Request with empty image URL.
	emptyImageURLReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{OfText: &openai.ChatCompletionContentPartTextParam{
							Text: "What is this?",
							Type: "text",
						}},
						{OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
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
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicReq,
			reqBody: string(basicReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(basicReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "Hello!"),
			},
		},
		{
			name:    "multimodal request with text and image",
			req:     multimodalReq,
			reqBody: string(multimodalReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(multimodalReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano","max_tokens":100}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "What is in this image?"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "image.image.url"), "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "image"),
			},
		},
		{
			name:    "request with tools",
			req:     toolsReq,
			reqBody: string(toolsReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(toolsReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano","tool_choice":"auto"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "What is the weather like in Boston today?"),
				attribute.String("llm.tools.0.tool.json_schema", `{"type":"function","function":{"name":"get_current_weather","description":"Get the current weather in a given location","parameters":{"properties":{"location":{"description":"The city and state, e.g. San Francisco, CA","type":"string"},"unit":{"enum":["celsius","fahrenheit"],"type":"string"}},"required":["location"],"type":"object"}}}`),
			},
		},
		{
			name:    "request with audio content",
			req:     audioReq,
			reqBody: string(audioReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-4o-audio-preview"),
				attribute.String(openinference.InputValue, string(audioReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-4o-audio-preview"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "Answer in up to 5 words: What do you hear in this audio?"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				// Audio content is skipped to match Python OpenInference behavior.
			},
		},
		{
			name:    "request with JSON mode",
			req:     jsonModeReq,
			reqBody: string(jsonModeReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(jsonModeReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano","response_format":{"type":"json_object"}}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "Generate a JSON object with three properties: name, age, and city."),
			},
		},
		{
			name:    "request with system message",
			req:     systemMessageReq,
			reqBody: string(systemMessageReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(systemMessageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleSystem),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "You are a helpful assistant."),
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageContent), "Hello!"),
			},
		},
		{
			name:    "request with empty tool array",
			req:     emptyToolsReq,
			reqBody: string(emptyToolsReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(emptyToolsReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "Hello!"),
			},
		},
		{
			name:    "request with tool message",
			req:     toolMessageReq,
			reqBody: string(toolMessageReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(toolMessageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "What's the weather?"),
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageRole), openai.ChatMessageRoleAssistant),
				// Assistant message with nil content is skipped.
				attribute.String(openinference.InputMessageAttribute(2, openinference.MessageRole), openai.ChatMessageRoleTool),
				attribute.String(openinference.InputMessageAttribute(2, openinference.MessageContent), "Sunny, 72°F"),
			},
		},
		{
			name:    "request with multimodal content - empty image URL",
			req:     emptyImageURLReq,
			reqBody: string(emptyImageURLReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(emptyImageURLReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "What is this?"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				// Image with empty URL is skipped..
			},
		},
		{
			name:    "request with user message empty content",
			req:     emptyContentReq,
			reqBody: string(emptyContentReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(emptyContentReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				// Empty content is skipped..
			},
		},
		{
			name:    "multimodal request with text redaction",
			req:     multimodalReq,
			reqBody: string(multimodalReqBody),
			config: &openinference.TraceConfig{
				HideInputs:  true,
				HideOutputs: false,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano","max_tokens":100}`),
				// Messages are not included when HideInputs is true.
			},
		},
		{
			name:    "multimodal request with HideInputText",
			req:     multimodalReq,
			reqBody: string(multimodalReqBody),
			config: &openinference.TraceConfig{
				HideInputText: true,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(multimodalReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano","max_tokens":100}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), openinference.RedactedValue),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "image.image.url"), "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "image"),
			},
		},
		{
			name:    "system message with HideInputText",
			req:     systemMessageReq,
			reqBody: string(systemMessageReqBody),
			config: &openinference.TraceConfig{
				HideInputText: true,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(systemMessageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleSystem),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), openinference.RedactedValue),
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageRole), openai.ChatMessageRoleUser),
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageContent), openinference.RedactedValue),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config == nil {
				tt.config = openinference.NewTraceConfig()
			}
			attrs := buildRequestAttributes(tt.req, tt.reqBody, tt.config)

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, attrs)
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
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello, how are you?",
					},
					Role: openai.ChatMessageRoleUser,
				},
			},
			expected: "Hello, how are you?",
		},
		{
			name: "user message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: nil,
					},
					Role: openai.ChatMessageRoleUser,
				},
			},
			expected: "",
		},
		{
			name: "user message with complex content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{OfText: &openai.ChatCompletionContentPartTextParam{Text: "Part 1"}},
							{OfText: &openai.ChatCompletionContentPartTextParam{Text: "Part 2"}},
						},
					},
					Role: openai.ChatMessageRoleUser,
				},
			},
			expected: "[complex content]",
		},
		// Assistant message tests.
		{
			name: "assistant message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: "I'm doing well, thank you!",
					},
					Role: openai.ChatMessageRoleAssistant,
				},
			},
			expected: "I'm doing well, thank you!",
		},
		{
			name: "assistant message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: nil,
					},
					Role: openai.ChatMessageRoleAssistant,
				},
			},
			expected: "",
		},
		// System message tests.
		{
			name: "system message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{
						Value: "You are a helpful assistant.",
					},
					Role: openai.ChatMessageRoleSystem,
				},
			},
			expected: "You are a helpful assistant.",
		},
		// Developer message tests.
		{
			name: "developer message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Content: openai.ContentUnion{
						Value: "Internal developer note",
					},
					Role: openai.ChatMessageRoleDeveloper,
				},
			},
			expected: "Internal developer note",
		},
		// Tool message tests.
		{
			name: "tool message with string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ContentUnion{
						Value: "Tool response content",
					},
					Role: openai.ChatMessageRoleTool,
				},
			},
			expected: "Tool response content",
		},
		{
			name: "assistant message with empty string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role:    openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{Value: ""},
				},
			},
			expected: "",
		},
		{
			name: "assistant message with non-string content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: []openai.ChatCompletionAssistantMessageParamContent{
							{Type: "text", Text: ptr("Part 1")},
						},
					},
					Role: openai.ChatMessageRoleAssistant,
				},
			},
			expected: "[assistant message]",
		},
		{
			name: "system message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{Value: nil},
					Role:    openai.ChatMessageRoleSystem,
				},
			},
			expected: "",
		},
		{
			name: "system message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "instruction1"},
						{Type: "text", Text: "instruction2"},
					}},
					Role: openai.ChatMessageRoleSystem,
				},
			},
			expected: "[system message]",
		},
		{
			name: "developer message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Content: openai.ContentUnion{Value: nil},
					Role:    openai.ChatMessageRoleDeveloper,
				},
			},
			expected: "",
		},
		{
			name: "developer message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Role: openai.ChatMessageRoleDeveloper,
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "instruction1"},
						{Type: "text", Text: "instruction2"},
					}},
				},
			},
			expected: "[developer message]",
		},
		{
			name: "tool message with nil content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ContentUnion{Value: nil},
					Role:    openai.ChatMessageRoleTool,
				},
			},
			expected: "",
		},
		{
			name: "tool message with array content",
			msg: openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_123",
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "result1"},
						{Type: "text", Text: "result2"},
					}},
				},
			},
			expected: "[tool content]",
		},
		// Unknown message type.
		{
			name:     "unknown message type",
			msg:      openai.ChatCompletionMessageParamUnion{},
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

func TestIsBase64URL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "base64 image",
			url:      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAUA",
			expected: true,
		},
		{
			name:     "not a base64 image URL",
			url:      "https://example.com/image.png",
			expected: false,
		},
		{
			name:     "base64 but not an image",
			url:      "data:text/plain;base64,SGVsbG8gV29ybGQh",
			expected: false,
		},
		{
			name:     "image but not base64",
			url:      "data:image/png,89504E470D0A1A0A",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBase64URL(tt.url)
			require.Equal(t, tt.expected, result)
		})
	}
}

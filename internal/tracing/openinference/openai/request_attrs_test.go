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

	// Request with assistant message string content.
	assistantStringReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Role:    openai.ChatMessageRoleAssistant,
				Content: openai.StringOrAssistantRoleContentUnion{Value: "I'm doing well, thank you!"},
			},
		}},
	}
	assistantStringReqBody = mustJSON(assistantStringReq)

	// Request with assistant message array content.
	assistantArrayReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Role: openai.ChatMessageRoleAssistant,
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: []openai.ChatCompletionAssistantMessageParamContent{
						{Type: "text", Text: ptr("Part 1")},
						{Type: "text", Text: ptr("Part 2")},
					},
				},
			},
		}},
	}
	assistantArrayReqBody = mustJSON(assistantArrayReq)

	// Request with developer message.
	developerReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
				Role:    openai.ChatMessageRoleDeveloper,
				Content: openai.ContentUnion{Value: "Internal developer note"},
			},
		}},
	}
	developerReqBody = mustJSON(developerReq)

	// Request with developer message array content.
	developerArrayReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
				Role: openai.ChatMessageRoleDeveloper,
				Content: openai.ContentUnion{
					Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "instruction1"},
						{Type: "text", Text: "instruction2"},
					},
				},
			},
		}},
	}
	developerArrayReqBody = mustJSON(developerArrayReq)

	// Request with system message array content.
	systemArrayReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Role: openai.ChatMessageRoleSystem,
				Content: openai.ContentUnion{
					Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "System instruction 1"},
						{Type: "text", Text: "System instruction 2"},
					},
				},
			},
		}},
	}
	systemArrayReqBody = mustJSON(systemArrayReq)

	// Request with tool message array content.
	toolArrayReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfTool: &openai.ChatCompletionToolMessageParam{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "call_456",
				Content: openai.ContentUnion{
					Value: []openai.ChatCompletionContentPartTextParam{
						{Type: "text", Text: "result1"},
						{Type: "text", Text: "result2"},
					},
				},
			},
		}},
	}
	toolArrayReqBody = mustJSON(toolArrayReq)

	// Request with nil content messages.
	nilContentReq = &openai.ChatCompletionRequest{
		Model: openai.ModelGPT5Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: nil},
				},
			},
			{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role:    openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{Value: nil},
				},
			},
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role:    openai.ChatMessageRoleSystem,
					Content: openai.ContentUnion{Value: nil},
				},
			},
		},
	}
	nilContentReqBody = mustJSON(nilContentReq)
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
		{
			name:    "assistant message with string content",
			req:     assistantStringReq,
			reqBody: string(assistantStringReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(assistantStringReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleAssistant),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "I'm doing well, thank you!"),
			},
		},
		{
			name:    "assistant message with array content",
			req:     assistantArrayReq,
			reqBody: string(assistantArrayReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(assistantArrayReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleAssistant),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "Part 1"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "text"), "Part 2"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "text"),
			},
		},
		{
			name:    "developer message with string content",
			req:     developerReq,
			reqBody: string(developerReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(developerReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleDeveloper),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageContent), "Internal developer note"),
			},
		},
		{
			name:    "developer message with array content",
			req:     developerArrayReq,
			reqBody: string(developerArrayReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(developerArrayReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleDeveloper),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "instruction1"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "text"), "instruction2"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "text"),
			},
		},
		{
			name:    "system message with array content",
			req:     systemArrayReq,
			reqBody: string(systemArrayReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(systemArrayReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleSystem),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "System instruction 1"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "text"), "System instruction 2"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "text"),
			},
		},
		{
			name:    "tool message with array content",
			req:     toolArrayReq,
			reqBody: string(toolArrayReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(toolArrayReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleTool),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "text"), "result1"),
				attribute.String(openinference.InputMessageContentAttribute(0, 0, "type"), "text"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "text"), "result2"),
				attribute.String(openinference.InputMessageContentAttribute(0, 1, "type"), "text"),
			},
		},
		{
			name:    "messages with nil content",
			req:     nilContentReq,
			reqBody: string(nilContentReqBody),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.InputValue, string(nilContentReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-5-nano"}`),
				attribute.String(openinference.InputMessageAttribute(0, openinference.MessageRole), openai.ChatMessageRoleUser),
				// nil content is skipped
				attribute.String(openinference.InputMessageAttribute(1, openinference.MessageRole), openai.ChatMessageRoleAssistant),
				// nil content is skipped
				attribute.String(openinference.InputMessageAttribute(2, openinference.MessageRole), openai.ChatMessageRoleSystem),
				// nil content is skipped
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

func TestBuildCompletionRequestAttributes(t *testing.T) {
	basicCompletionReq := &openai.CompletionRequest{
		Model:  "gpt-3.5-turbo-instruct",
		Prompt: openai.PromptUnion{Value: "Say this is a test"},
	}
	basicCompletionReqBody := mustJSON(basicCompletionReq)

	arrayCompletionReq := &openai.CompletionRequest{
		Model:  "gpt-3.5-turbo-instruct",
		Prompt: openai.PromptUnion{Value: []string{"Say this is a test", "Say hello"}},
	}
	arrayCompletionReqBody := mustJSON(arrayCompletionReq)

	tokenCompletionReq := &openai.CompletionRequest{
		Model:  "gpt-3.5-turbo-instruct",
		Prompt: openai.PromptUnion{Value: []int64{1, 2, 3}},
	}
	tokenCompletionReqBody := mustJSON(tokenCompletionReq)

	tests := []struct {
		name          string
		req           *openai.CompletionRequest
		reqBody       []byte
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic string prompt",
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(basicCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				attribute.String(openinference.PromptTextAttribute(0), "Say this is a test"),
			},
		},
		{
			name:    "array prompts",
			req:     arrayCompletionReq,
			reqBody: arrayCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(arrayCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				attribute.String(openinference.PromptTextAttribute(0), "Say this is a test"),
				attribute.String(openinference.PromptTextAttribute(1), "Say hello"),
			},
		},
		{
			name:    "token prompts not recorded",
			req:     tokenCompletionReq,
			reqBody: tokenCompletionReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(tokenCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompt attributes for token arrays
			},
		},
		{
			name:    "hide inputs",
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			config:  &openinference.TraceConfig{HideInputs: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompts when HideInputs is true
			},
		},
		{
			name:    "hide prompts",
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			config:  &openinference.TraceConfig{HidePrompts: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(basicCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.LLMInvocationParameters, `{"model":"gpt-3.5-turbo-instruct"}`),
				// No prompt attributes when HidePrompts is true
			},
		},
		{
			name:    "hide invocation parameters",
			req:     basicCompletionReq,
			reqBody: basicCompletionReqBody,
			config:  &openinference.TraceConfig{HideLLMInvocationParameters: true},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, "gpt-3.5-turbo-instruct"),
				attribute.String(openinference.InputValue, string(basicCompletionReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.PromptTextAttribute(0), "Say this is a test"),
				// No LLMInvocationParameters when HideLLMInvocationParameters is true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config == nil {
				tt.config = openinference.NewTraceConfig()
			}
			attrs := buildCompletionRequestAttributes(tt.req, tt.reqBody, tt.config)

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, attrs)
		})
	}
}

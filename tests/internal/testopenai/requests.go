// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Cassette is an HTTP interaction recording.
//
// Note: At the moment, our tests are optimized for single request/response
// pairs and do not include scenarios requiring multiple round-trips, such as
// `cached_tokens`.
type Cassette int

// Cassette names for testing, corresponding to ChatCassettes().
const (
	// CassetteChatBasic is the canonical OpenAI chat completion request.
	CassetteChatBasic Cassette = iota
	// CassetteChatJSONMode is a chat completion request with JSON response format.
	CassetteChatJSONMode
	// CassetteChatMultimodal is a multimodal chat request with text and image inputs.
	CassetteChatMultimodal
	// CassetteChatMultiturn is a multi-turn conversation with message history.
	CassetteChatMultiturn
	// CassetteChatNoMessages is a request missing the required messages field.
	CassetteChatNoMessages
	// CassetteChatParallelTools is a chat completion with parallel function calling enabled.
	CassetteChatParallelTools
	// CassetteChatStreaming is the canonical OpenAI chat completion request,
	// with streaming enabled.
	CassetteChatStreaming
	// CassetteChatTools is a chat completion request with function tools.
	CassetteChatTools
	// CassetteChatUnknownModel is a request with a non-existent model.
	CassetteChatUnknownModel
	// CassetteChatBadRequest is a request with multiple validation errors.
	CassetteChatBadRequest
	// CassetteChatReasoning tests capture of reasoning_tokens in completion_tokens_details for O1 models.
	CassetteChatReasoning
	// CassetteChatImageToText tests image input processing showing image token
	// count in usage details.
	CassetteChatImageToText
	// CassetteChatTextToImageTool tests image generation through tool calls since
	// chat completions cannot natively output images.
	CassetteChatTextToImageTool
	// CassetteChatAudioToText tests audio input transcription and audio_tokens
	// in prompt_tokens_details.
	CassetteChatAudioToText
	// CassetteChatTextToAudio tests audio output generation where the model
	// produces audio content, showing audio_tokens in completion_tokens_details.
	CassetteChatTextToAudio
	// CassetteChatDetailedUsage tests capture of all token usage detail fields in a single response.
	CassetteChatDetailedUsage
	// CassetteChatStreamingDetailedUsage tests capture of detailed token usage in streaming responses with include_usage.
	CassetteChatStreamingDetailedUsage
	_cassetteNameEnd // Sentinel value for iteration.
)

// stringValues maps Cassette values to their string representations.
var stringValues = map[Cassette]string{
	CassetteChatBasic:                  "chat-basic",
	CassetteChatJSONMode:               "chat-json-mode",
	CassetteChatMultimodal:             "chat-multimodal",
	CassetteChatMultiturn:              "chat-multiturn",
	CassetteChatNoMessages:             "chat-no-messages",
	CassetteChatParallelTools:          "chat-parallel-tools",
	CassetteChatStreaming:              "chat-streaming",
	CassetteChatTools:                  "chat-tools",
	CassetteChatUnknownModel:           "chat-unknown-model",
	CassetteChatBadRequest:             "chat-bad-request",
	CassetteChatReasoning:              "chat-reasoning",
	CassetteChatImageToText:            "chat-image-to-text",
	CassetteChatTextToImageTool:        "chat-text-to-image-tool",
	CassetteChatAudioToText:            "chat-audio-to-text",
	CassetteChatTextToAudio:            "chat-text-to-audio",
	CassetteChatDetailedUsage:          "chat-detailed-usage",
	CassetteChatStreamingDetailedUsage: "chat-streaming-detailed-usage",
}

// String returns the string representation of the cassette name.
func (c Cassette) String() string {
	if s, ok := stringValues[c]; ok {
		return s
	}
	return "unknown"
}

// ChatCassettes returns a slice of all available cassette names for chat compeltions.
// Unlike image generation—which *requires* an image_generation tool call—
// audio synthesis is natively supported.
func ChatCassettes() []Cassette {
	result := make([]Cassette, 0, int(_cassetteNameEnd))
	for i := Cassette(0); i < _cassetteNameEnd; i++ {
		result = append(result, i)
	}
	return result
}

// NewRequest creates a new HTTP request for the given cassette.
//
// The returned request is an http.MethodPost with the body and
// CassetteNameHeader according to the pre-recorded cassette.
func NewRequest(ctx context.Context, baseURL string, cassetteName Cassette) (*http.Request, error) {
	// Get the request body for this cassette.
	requestBody, ok := requestBodies[cassetteName]
	if !ok {
		return nil, fmt.Errorf("unknown cassette name: %s", cassetteName)
	}

	// Marshal the request body to JSON.
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create the request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}

	// Set headers.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cassette-Name", cassetteName.String())

	return req, nil
}

// requestBodies contains the actual request body for each cassette and are
// needed for re-recording the cassettes to get realistic responses.
//
// Prefer bodies in the OpenAI OpenAPI examples to making them up manually.
// See https://github.com/openai/openai-openapi/tree/manual_spec
var requestBodies = map[Cassette]*openai.ChatCompletionRequest{
	CassetteChatBasic: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello!",
					},
				},
			},
		},
	},
	CassetteChatStreaming: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleDeveloper,
				Value: openai.ChatCompletionDeveloperMessageParam{
					Role: openai.ChatMessageRoleDeveloper,
					Content: openai.StringOrArray{
						Value: "You are a helpful assistant.",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello!",
					},
				},
			},
		},
		Stream: true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	},
	CassetteChatTools: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "What is the weather like in Boston today?",
					},
				},
			},
		},
		Tools: []openai.Tool{
			{
				Type: openai.ToolTypeFunction,
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
			},
		},
		ToolChoice: "auto",
	},
	CassetteChatMultimodal: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{
								TextContent: &openai.ChatCompletionContentPartTextParam{
									Type: string(openai.ChatCompletionContentPartTextTypeText),
									Text: "What is in this image?",
								},
							},
							{
								ImageContent: &openai.ChatCompletionContentPartImageParam{
									Type: openai.ChatCompletionContentPartImageTypeImageURL,
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg",
									},
								},
							},
						},
					},
				},
			},
		},
		MaxTokens: ptr.To[int64](100),
	},
	CassetteChatMultiturn: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleDeveloper,
				Value: openai.ChatCompletionDeveloperMessageParam{
					Role: openai.ChatMessageRoleDeveloper,
					Content: openai.StringOrArray{
						Value: "You are a helpful assistant.",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello!",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleAssistant,
				Value: openai.ChatCompletionAssistantMessageParam{
					Role: openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: "Hello! How can I assist you today?",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Answer in up to 5 words: What's the weather like?",
					},
				},
			},
		},
		Temperature: ptr.To(0.7),
	},
	CassetteChatJSONMode: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Generate a JSON object with three properties: name, age, and city.",
					},
				},
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	},
	CassetteChatNoMessages: {
		Model:    openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{},
	},
	CassetteChatParallelTools: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "What is the weather like in San Francisco?",
					},
				},
			},
		},
		Tools: []openai.Tool{
			{
				Type: openai.ToolTypeFunction,
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
			},
		},
		ToolChoice:        "auto",
		ParallelToolCalls: ptr.To(true),
	},
	CassetteChatBadRequest: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: nil,
					},
				},
			},
		},
		Temperature: ptr.To(-0.5),
		MaxTokens:   ptr.To[int64](0),
	},
	CassetteChatImageToText: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{
								TextContent: &openai.ChatCompletionContentPartTextParam{
									Type: string(openai.ChatCompletionContentPartTextTypeText),
									Text: "Answer in up to 5 words: What's in this image?",
								},
							},
							{
								ImageContent: &openai.ChatCompletionContentPartImageParam{
									Type: openai.ChatCompletionContentPartImageTypeImageURL,
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAXElEQVR42mNgoAkofv8fBVOkmQyD/sPwn1UlYAzlE6cZpgGmGZlPkgHYDCHKCcia0AwgHlCm+c+f/9gwabajG0CsK+DOxmIA8YZQ6gXkhISG6W8ALj7RtuMTgwMA0WTdqiU1ensAAAAASUVORK5CYII=",
									},
								},
							},
						},
					},
				},
			},
		},
	},
	CassetteChatUnknownModel: {
		Model: "gpt-4.1-nano-wrong",
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Hello!",
					},
				},
			},
		},
	},
	CassetteChatReasoning: {
		Model: openai.ModelO3Mini,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "A bat and ball cost $1.10. Bat costs $1 more than ball. Ball cost?",
					},
				},
			},
		},
	},
	CassetteChatAudioToText: {
		Model: openai.ModelGPT4oAudioPreview,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{
								TextContent: &openai.ChatCompletionContentPartTextParam{
									Type: string(openai.ChatCompletionContentPartTextTypeText),
									Text: "Answer in up to 5 words: What do you hear in this audio?",
								},
							},
							{
								InputAudioContent: &openai.ChatCompletionContentPartInputAudioParam{
									Type: openai.ChatCompletionContentPartInputAudioTypeInputAudio,
									InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
										Data:   "UklGRlwEAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YTgEAADY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2CcnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2CcnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2CcnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2CcnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2NgnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycn2NjY2NjY2NjY2CcnJycnJycnJyfY2NjY2NjY2NjYJycnJycnJycnJ9jY2NjY2NjY2NgnJycnJycnJycnv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/v0BAQEBAv7+/v79AQEBAQL+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAv7+/v79AQEBAQL+/v7+/QEBAQEC/v7+/v0BAQEBAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIA=", // 8-bit style jump sound, 0.135s, 8kHz mono WAV.
										Format: openai.ChatCompletionContentPartInputAudioInputAudioFormatWAV,
									},
								},
							},
						},
					},
				},
			},
		},
	},
	CassetteChatTextToAudio: {
		Model: openai.ModelGPT4oMiniAudioPreview,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Say a single short 'beep' sound, as brief as possible.",
					},
				},
			},
		},
		Audio: &openai.ChatCompletionAudioParam{
			Format: openai.ChatCompletionAudioFormatOpus,
			Voice:  openai.ChatCompletionAudioVoiceAlloy,
		},
		Modalities: []openai.ChatCompletionModality{
			openai.ChatCompletionModalityText,
			openai.ChatCompletionModalityAudio,
		},
	},
	CassetteChatDetailedUsage: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleDeveloper,
				Value: openai.ChatCompletionDeveloperMessageParam{
					Role: openai.ChatMessageRoleDeveloper,
					Content: openai.StringOrArray{
						Value: "You are a poetry assistant. Write a haiku when asked.",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Write a haiku about OpenTelemetry tracing.",
					},
				},
			},
		},
		Temperature: ptr.To(0.8),
		MaxTokens:   ptr.To[int64](100),
	},
	CassetteChatStreamingDetailedUsage: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Say hello",
					},
				},
			},
		},
		Stream: true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
		MaxTokens: ptr.To[int64](10),
	},
	CassetteChatTextToImageTool: {
		Model: openai.ModelGPT41Nano,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type: openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{
					Role: openai.ChatMessageRoleSystem,
					Content: openai.StringOrArray{
						Value: "You are an AI assistant that generates simple, sketch-style images with minimal detail. When asked to generate an image, create it with low quality settings for cost efficiency.",
					},
				},
			},
			{
				Type: openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: "Draw a simple, minimalist image of a cute cat playing with a ball of yarn in a sketch style.",
					},
				},
			},
		},
		Tools: []openai.Tool{
			{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "generate_image",
					Description: "Generate a simple, minimalist image based on the given prompt in sketch style with low quality for cost efficiency.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"prompt": map[string]interface{}{
								"type":        "string",
								"description": "The text description of the image to generate.",
							},
						},
						"required": []string{"prompt"},
					},
				},
			},
		},
		ToolChoice: openai.ChatCompletionNamedToolChoice{
			Type: openai.ToolChoiceTypeFunction,
			Function: openai.ChatCompletionNamedToolChoiceFunction{
				Name: "generate_image",
			},
		},
		MaxTokens: ptr.To[int64](150),
	},
}

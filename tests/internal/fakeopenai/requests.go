// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Cassette is an HTTP interaction recording.
type Cassette int

// Cassette names for testing.
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
	// CassetteChatBase64Image is a request with a base64-encoded image.
	CassetteChatBase64Image
	_cassetteNameEnd // Sentinel value for iteration.
)

// stringValues maps Cassette values to their string representations.
var stringValues = map[Cassette]string{
	CassetteChatBasic:         "chat-basic",
	CassetteChatJSONMode:      "chat-json-mode",
	CassetteChatMultimodal:    "chat-multimodal",
	CassetteChatMultiturn:     "chat-multiturn",
	CassetteChatNoMessages:    "chat-no-messages",
	CassetteChatParallelTools: "chat-parallel-tools",
	CassetteChatStreaming:     "chat-streaming",
	CassetteChatTools:         "chat-tools",
	CassetteChatUnknownModel:  "chat-unknown-model",
	CassetteChatBadRequest:    "chat-bad-request",
	CassetteChatBase64Image:   "chat-base64-image",
}

// String returns the string representation of the cassette name.
func (c Cassette) String() string {
	if s, ok := stringValues[c]; ok {
		return s
	}
	return "unknown"
}

// AllCassettes returns a slice of all available cassette names.
func AllCassettes() []Cassette {
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
func NewRequest(baseURL string, cassetteName Cassette) (*http.Request, error) {
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
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(jsonData))
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
								"enum": []interface{}{"celsius", "fahrenheit"},
							},
						},
						"required": []interface{}{"location"},
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
						Value: "What's the weather like?",
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
								"enum": []interface{}{"celsius", "fahrenheit"},
							},
						},
						"required": []interface{}{"location"},
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
	CassetteChatBase64Image: {
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
									Text: "What's in this image?",
								},
							},
							{
								ImageContent: &openai.ChatCompletionContentPartImageParam{
									Type: openai.ChatCompletionContentPartImageTypeImageURL,
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
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
}

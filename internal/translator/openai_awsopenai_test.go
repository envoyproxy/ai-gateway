// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestOpenAIToAwsOpenAITranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	tests := []struct {
		name              string
		input             openai.ChatCompletionRequest
		modelNameOverride internalapi.ModelNameOverride
		expectedPath      string
		expectedBody      openai.ChatCompletionRequest
	}{
		{
			name: "basic non-streaming request",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Hello, world!",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			expectedPath: "/model/gpt-4/invoke",
			expectedBody: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Hello, world!",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
		},
		{
			name: "streaming request",
			input: openai.ChatCompletionRequest{
				Stream: true,
				Model:  "gpt-3.5-turbo",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me a story",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			expectedPath: "/model/gpt-3.5-turbo/invoke-with-response-stream",
			expectedBody: openai.ChatCompletionRequest{
				Stream: true,
				Model:  "gpt-3.5-turbo",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me a story",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
		},
		{
			name: "model name override",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Hello with override",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			modelNameOverride: "arn:aws:bedrock:us-east-1:123456789:model/gpt-4",
			expectedPath:      "/model/arn:aws:bedrock:us-east-1:123456789:model%2Fgpt-4/invoke",
			expectedBody: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "arn:aws:bedrock:us-east-1:123456789:model/gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Hello with override",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
		},
		{
			name: "complex request with tools",
			input: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gpt-4",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1000)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant.",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "What's the weather?",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "City name",
									},
								},
							},
						},
					},
				},
			},
			expectedPath: "/model/gpt-4/invoke",
			expectedBody: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gpt-4",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1000)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant.",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "What's the weather?",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "City name",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToAwsOpenAITranslator("v1", tt.modelNameOverride)

			// Marshal input to JSON
			rawBody, err := json.Marshal(tt.input)
			require.NoError(t, err)

			// Call RequestBody
			headers, newBody, err := translator.RequestBody(rawBody, &tt.input, false)
			require.NoError(t, err)

			// Check headers
			require.Len(t, headers, 2)
			require.Equal(t, pathHeaderName, headers[0][0])
			require.Equal(t, tt.expectedPath, headers[0][1])
			require.Equal(t, contentLengthHeaderName, headers[1][0])
			require.Equal(t, strconv.Itoa(len(newBody)), headers[1][1])

			// Check body - compare essential fields instead of full struct comparison
			var actualBody openai.ChatCompletionRequest
			err = json.Unmarshal(newBody, &actualBody)
			require.NoError(t, err)

			// Compare essential fields only
			require.Equal(t, tt.expectedBody.Model, actualBody.Model)
			require.Equal(t, tt.expectedBody.Stream, actualBody.Stream)
			require.Equal(t, len(tt.expectedBody.Messages), len(actualBody.Messages))

			// For complex requests, check tools and parameters
			if tt.expectedBody.Temperature != nil {
				require.Equal(t, *tt.expectedBody.Temperature, *actualBody.Temperature)
			}
			if tt.expectedBody.MaxTokens != nil {
				require.Equal(t, *tt.expectedBody.MaxTokens, *actualBody.MaxTokens)
			}
			if len(tt.expectedBody.Tools) > 0 {
				require.Equal(t, len(tt.expectedBody.Tools), len(actualBody.Tools))
				require.Equal(t, tt.expectedBody.Tools[0].Type, actualBody.Tools[0].Type)
				if tt.expectedBody.Tools[0].Function != nil {
					require.Equal(t, tt.expectedBody.Tools[0].Function.Name, actualBody.Tools[0].Function.Name)
				}
			}
		})
	}
}

func TestOpenAIToAwsOpenAITranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	tests := []struct {
		name            string
		inputHeaders    map[string]string
		isStreaming     bool
		expectedHeaders []internalapi.Header
	}{
		{
			name: "non-streaming response",
			inputHeaders: map[string]string{
				"x-amzn-requestid": "test-request-id",
				"content-type":     "application/json",
			},
			isStreaming:     false,
			expectedHeaders: nil,
		},
		{
			name: "streaming response with eventstream",
			inputHeaders: map[string]string{
				"x-amzn-requestid": "test-request-id",
				"content-type":     "application/vnd.amazon.eventstream",
			},
			isStreaming: true,
			expectedHeaders: []internalapi.Header{
				{contentTypeHeaderName, "text/event-stream"},
			},
		},
		{
			name: "streaming response with correct content-type",
			inputHeaders: map[string]string{
				"x-amzn-requestid": "test-request-id",
				"content-type":     "text/event-stream",
			},
			isStreaming:     true,
			expectedHeaders: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToAwsOpenAITranslator("v1", "").(*openAIToAwsOpenAITranslatorV1ChatCompletion)
			translator.stream = tt.isStreaming

			headers, err := translator.ResponseHeaders(tt.inputHeaders)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedHeaders, headers); diff != "" {
				t.Errorf("ResponseHeaders() mismatch (-expected +actual):\n%s", diff)
			}

			// Verify responseID is stored
			require.Equal(t, "test-request-id", translator.responseID)
		})
	}
}

func TestOpenAIToAwsOpenAITranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	tests := []struct {
		name                   string
		isStreaming            bool
		inputBody              string
		endOfStream            bool
		expectedInputTokens    uint32
		expectedOutputTokens   uint32
		expectedTotalTokens    uint32
		expectedResponseBody   string
		checkResponseBodyExact bool
	}{
		{
			name:        "non-streaming response",
			isStreaming: false,
			inputBody: `{
				"id": "chatcmpl-test",
				"object": "chat.completion",
				"created": 1677858242,
				"model": "gpt-4",
				"choices": [
					{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "Hello! How can I assist you today?"
						},
						"finish_reason": "stop"
					}
				],
				"usage": {
					"prompt_tokens": 13,
					"completion_tokens": 9,
					"total_tokens": 22
				}
			}`,
			expectedInputTokens:  13,
			expectedOutputTokens: 9,
			expectedTotalTokens:  22,
			expectedResponseBody: `{
				"id": "test-request-id",
				"object": "chat.completion",
				"created": 1677858242,
				"model": "gpt-4",
				"choices": [
					{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "Hello! How can I assist you today?"
						},
						"finish_reason": "stop"
					}
				],
				"usage": {
					"prompt_tokens": 13,
					"completion_tokens": 9,
					"total_tokens": 22
				}
			}`,
		},
		{
			name:        "streaming response",
			isStreaming: true,
			inputBody: `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":9,"total_tokens":22}}

`,
			endOfStream:          true,
			expectedInputTokens:  13,
			expectedOutputTokens: 9,
			expectedTotalTokens:  22,
			expectedResponseBody: `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":9,"total_tokens":22}}

data: [DONE]
`,
			checkResponseBodyExact: true,
		},
		{
			name:        "streaming response without DONE",
			isStreaming: true,
			inputBody: `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

`,
			endOfStream: true,
			expectedResponseBody: `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: [DONE]
`,
			checkResponseBodyExact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToAwsOpenAITranslator("v1", "").(*openAIToAwsOpenAITranslatorV1ChatCompletion)
			translator.stream = tt.isStreaming
			translator.requestModel = "gpt-4"
			translator.responseID = "test-request-id"

			headers := map[string]string{}
			body := strings.NewReader(tt.inputBody)

			resultHeaders, resultBody, tokenUsage, responseModel, err := translator.ResponseBody(headers, body, tt.endOfStream, nil)
			require.NoError(t, err)

			// Check token usage
			actualInputTokens, _ := tokenUsage.InputTokens()
			actualOutputTokens, _ := tokenUsage.OutputTokens()
			actualTotalTokens, _ := tokenUsage.TotalTokens()
			require.Equal(t, tt.expectedInputTokens, actualInputTokens)
			require.Equal(t, tt.expectedOutputTokens, actualOutputTokens)
			require.Equal(t, tt.expectedTotalTokens, actualTotalTokens)

			// Check response model
			require.Equal(t, "gpt-4", responseModel)

			// Check headers
			if len(resultBody) > 0 {
				require.Len(t, resultHeaders, 1)
				require.Equal(t, contentLengthHeaderName, resultHeaders[0][0])
				require.Equal(t, strconv.Itoa(len(resultBody)), resultHeaders[0][1])
			}

			// Check response body
			if !tt.isStreaming {
				// For non-streaming, parse and compare JSON
				var expected, actual openai.ChatCompletionResponse
				err = json.Unmarshal([]byte(tt.expectedResponseBody), &expected)
				require.NoError(t, err)
				err = json.Unmarshal(resultBody, &actual)
				require.NoError(t, err)

				if diff := cmp.Diff(expected, actual); diff != "" {
					t.Errorf("ResponseBody() body mismatch (-expected +actual):\n%s", diff)
				}
			} else if tt.checkResponseBodyExact {
				// For streaming, compare the content directly
				require.Equal(t, tt.expectedResponseBody, string(resultBody))
			}
		})
	}
}

func TestOpenAIToAwsOpenAITranslatorV1ChatCompletion_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		inputHeaders    map[string]string
		inputBody       string
		expectedError   openai.Error
		expectedHeaders []internalapi.Header
	}{
		{
			name: "AWS JSON error",
			inputHeaders: map[string]string{
				statusHeaderName:       "400",
				contentTypeHeaderName:  "application/json",
				awsErrorTypeHeaderName: "ValidationException",
			},
			inputBody: `{
				"__type": "ValidationException",
				"message": "Invalid model specified"
			}`,
			expectedError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "ValidationException",
					Message: "Invalid model specified",
					Code:    ptr.To("400"),
				},
			},
		},
		{
			name: "existing OpenAI error format",
			inputHeaders: map[string]string{
				statusHeaderName:      "429",
				contentTypeHeaderName: "application/json",
			},
			inputBody: `{
				"error": {
					"message": "Rate limit exceeded",
					"type": "rate_limit_exceeded",
					"code": "rate_limit_exceeded"
				}
			}`,
			expectedError: openai.Error{
				Error: openai.ErrorType{
					Message: "Rate limit exceeded",
					Type:    "rate_limit_exceeded",
					Code:    ptr.To("rate_limit_exceeded"),
				},
			},
		},
		{
			name: "generic AWS error",
			inputHeaders: map[string]string{
				statusHeaderName:      "500",
				contentTypeHeaderName: "application/json",
			},
			inputBody: `{
				"message": "Internal server error"
			}`,
			expectedError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "", // No error type header provided, so it will be empty
					Message: "Internal server error",
					Code:    ptr.To("500"),
				},
			},
		},
		{
			name: "non-JSON error",
			inputHeaders: map[string]string{
				statusHeaderName:      "503",
				contentTypeHeaderName: "text/plain",
			},
			inputBody: "Service Unavailable",
			expectedError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    awsInvokeModelBackendError,
					Message: "Service Unavailable",
					Code:    ptr.To("503"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToAwsOpenAITranslator("v1", "").(*openAIToAwsOpenAITranslatorV1ChatCompletion)

			body := strings.NewReader(tt.inputBody)

			resultHeaders, resultBody, err := translator.ResponseError(tt.inputHeaders, body)
			require.NoError(t, err)

			// Check headers
			require.Len(t, resultHeaders, 2)
			require.Equal(t, contentTypeHeaderName, resultHeaders[0][0])
			require.Equal(t, jsonContentType, resultHeaders[0][1])
			require.Equal(t, contentLengthHeaderName, resultHeaders[1][0])
			require.Equal(t, strconv.Itoa(len(resultBody)), resultHeaders[1][1])

			// Parse and check error response
			var actualError openai.Error
			err = json.Unmarshal(resultBody, &actualError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedError, actualError); diff != "" {
				t.Errorf("ResponseError() mismatch (-expected +actual):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToAwsOpenAITranslatorV1ChatCompletion_ModelNameEncoding(t *testing.T) {
	tests := []struct {
		name         string
		modelName    string
		expectedPath string
	}{
		{
			name:         "simple model name",
			modelName:    "gpt-4",
			expectedPath: "/model/gpt-4/invoke",
		},
		{
			name:         "ARN with special characters",
			modelName:    "arn:aws:bedrock:us-east-1:123456789:model/gpt-4",
			expectedPath: "/model/arn:aws:bedrock:us-east-1:123456789:model%2Fgpt-4/invoke",
		},
		{
			name:         "model name with spaces",
			modelName:    "my custom model",
			expectedPath: "/model/my%20custom%20model/invoke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToAwsOpenAITranslator("v1", tt.modelName)

			input := openai.ChatCompletionRequest{
				Stream: false,
				Model:  "original-model",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "test",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			}

			rawBody, err := json.Marshal(input)
			require.NoError(t, err)

			headers, _, err := translator.RequestBody(rawBody, &input, false)
			require.NoError(t, err)

			require.Len(t, headers, 2)
			require.Equal(t, pathHeaderName, headers[0][0])
			require.Equal(t, tt.expectedPath, headers[0][1])
		})
	}
}
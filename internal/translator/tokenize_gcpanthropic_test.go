// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestNewTokenizeToGCPAnthropicTranslator(t *testing.T) {
	translator := NewTokenizeToGCPAnthropicTranslator("custom-version", "override-model")

	require.NotNil(t, translator)
	concrete := translator.(*ToGCPAnthropicV1Tokenize)
	require.Equal(t, "custom-version", concrete.apiVersion)
	require.Equal(t, "override-model", concrete.modelNameOverride)
	require.Empty(t, concrete.requestModel)
}

func TestToGCPAnthropicV1Tokenize_RequestBody(t *testing.T) {
	t.Run("chat request - no model override", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Hello world"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		headers, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "claude-3-opus-20240229", translator.requestModel)
		require.Len(t, headers, 2)
		require.Equal(t, pathHeaderName, headers[0].Key())
		require.Contains(t, headers[0].Value(), "count-tokens")
		require.Contains(t, headers[0].Value(), "rawPredict")
		require.Equal(t, contentLengthHeaderName, headers[1].Key())
		require.Equal(t, strconv.Itoa(len(body)), headers[1].Value())

		// Verify the body contains Anthropic MessageCountTokens request
		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.NotNil(t, anthropicReq.Messages)
		require.Equal(t, anthropic.Model("claude-3-opus-20240229"), anthropicReq.Model)
		require.Len(t, anthropicReq.Messages, 1)
		require.Equal(t, anthropic.MessageParamRoleUser, anthropicReq.Messages[0].Role)
	})

	t.Run("chat request - with model override", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			modelNameOverride: "override-model",
		}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "original-model",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Test message"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		headers, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "override-model", translator.requestModel)
		require.Len(t, headers, 2)
		require.Contains(t, headers[0].Value(), "count-tokens")
	})

	t.Run("chat request - with system instruction", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{Value: "You are a helpful assistant"},
							Role:    openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.NotNil(t, anthropicReq.System)
		require.Len(t, anthropicReq.Messages, 1) // System message should not be in messages
		require.Equal(t, anthropic.MessageParamRoleUser, anthropicReq.Messages[0].Role)
	})

	t.Run("completion request - converted to chat", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			CompletionRequest: &tokenize.CompletionRequest{
				Model:  "claude-3-opus-20240229",
				Prompt: "Hello world",
			},
		}

		headers, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "claude-3-opus-20240229", translator.requestModel)
		require.Len(t, headers, 2)
		require.Contains(t, headers[0].Value(), "count-tokens")
		require.Contains(t, headers[0].Value(), "rawPredict")

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Len(t, anthropicReq.Messages, 1)
		require.Equal(t, anthropic.MessageParamRoleUser, anthropicReq.Messages[0].Role)
	})

	t.Run("empty model string", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "", // Empty model string
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Test"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		// Should handle empty model gracefully
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
	})

	t.Run("message conversion error", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		// Create a request with empty messages which should work fine
		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model:    "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{}, // Empty messages
			},
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err) // Empty messages are actually valid for Anthropic
	})

	t.Run("invalid union - both types set", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			CompletionRequest: &tokenize.CompletionRequest{
				Model:  "claude-3-opus-20240229",
				Prompt: "Hello",
			},
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Test"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "only one request type can be set")
	})

	t.Run("invalid union - no types set", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			// Neither completion nor chat request set
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "one request type must be set")
	})
}

func TestToGCPAnthropicV1Tokenize_ResponseBody(t *testing.T) {
	t.Run("valid Anthropic response", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			requestModel: "claude-3-opus-20240229",
		}

		anthropicResp := &anthropic.MessageTokensCount{
			InputTokens: 42,
		}

		responseJSON, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		headers, body, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil, // No span for simplicity
		)

		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "claude-3-opus-20240229", responseModel)
		require.Len(t, headers, 1)
		require.Equal(t, contentLengthHeaderName, headers[0].Key())

		// Verify the response is converted to OpenAI format
		var openAIResp tokenize.Response
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 42, openAIResp.Count)

		// Token usage should be extracted from Anthropic response
		_, hasTokens := tokenUsage.InputTokens()
		require.False(t, hasTokens) // Current behavior - token usage not implemented
	})

	t.Run("empty response model fallback", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			requestModel: "claude-3-haiku-20240307",
		}

		anthropicResp := &anthropic.MessageTokensCount{
			InputTokens: 0,
		}

		responseJSON, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, _, _, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)
		require.Equal(t, "claude-3-haiku-20240307", responseModel) // Falls back to request model
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{}

		invalidJSON := "invalid json response"

		_, _, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader([]byte(invalidJSON)),
			false,
			nil,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	t.Run("response conversion handles zero tokens", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{}

		anthropicResp := &anthropic.MessageTokensCount{
			InputTokens: 0, // Zero tokens
		}

		responseJSON, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)

		var openAIResp tokenize.Response
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 0, openAIResp.Count)
	})

	t.Run("response conversion handles large token count", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{}

		anthropicResp := &anthropic.MessageTokensCount{
			InputTokens: 100000, // Large token count
		}

		responseJSON, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)

		var openAIResp tokenize.Response
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 100000, openAIResp.Count)
	})
}

func TestToGCPAnthropicV1Tokenize_ResponseError(t *testing.T) {
	translator := &ToGCPAnthropicV1Tokenize{}

	tests := []struct {
		name            string
		responseHeaders map[string]string
		input           string
		expectedType    string
		expectedCode    string
		expectedMessage string
	}{
		{
			name: "Anthropic structured error response",
			responseHeaders: map[string]string{
				":status":      "400",
				"content-type": "application/json",
			},
			input: `{
				"type": "error",
				"error": {
					"type": "invalid_request_error",
					"message": "Invalid request: model not found"
				}
			}`,
			expectedType:    "invalid_request_error",
			expectedCode:    "400",
			expectedMessage: "Invalid request: model not found",
		},
		{
			name: "Non-JSON error response",
			responseHeaders: map[string]string{
				":status":      "503",
				"content-type": "text/plain",
			},
			input:           "Service temporarily unavailable",
			expectedType:    gcpAnthropicBackendError,
			expectedCode:    "503",
			expectedMessage: "Service temporarily unavailable",
		},
		{
			name: "HTML error response",
			responseHeaders: map[string]string{
				":status":      "504",
				"content-type": "text/html",
			},
			input:           "<html><body>Gateway Timeout</body></html>",
			expectedType:    gcpAnthropicBackendError,
			expectedCode:    "504",
			expectedMessage: "<html><body>Gateway Timeout</body></html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers, newBody, err := translator.ResponseError(tt.responseHeaders, strings.NewReader(tt.input))

			require.NoError(t, err)
			require.NotNil(t, headers)
			require.NotNil(t, newBody)

			require.Len(t, headers, 2)
			require.Equal(t, contentTypeHeaderName, headers[0].Key())
			require.Equal(t, jsonContentType, headers[0].Value()) //nolint:testifylint // comparing header value, not JSON
			require.Equal(t, contentLengthHeaderName, headers[1].Key())
			require.Equal(t, strconv.Itoa(len(newBody)), headers[1].Value())

			var openAIError openai.Error
			require.NoError(t, json.Unmarshal(newBody, &openAIError))
			require.Equal(t, "error", openAIError.Type)
			require.Equal(t, tt.expectedType, openAIError.Error.Type)
			require.Equal(t, tt.expectedCode, *openAIError.Error.Code)
			require.Equal(t, tt.expectedMessage, openAIError.Error.Message)
		})
	}

	t.Run("read error", func(t *testing.T) {
		// Create a reader that will fail
		pr, pw := io.Pipe()
		_ = pw.CloseWithError(fmt.Errorf("simulated read error"))

		_, _, err := translator.ResponseError(map[string]string{":status": "500"}, pr)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read raw error body")
	})
}

func TestToGCPAnthropicV1Tokenize_ResponseHeaders(t *testing.T) {
	translator := &ToGCPAnthropicV1Tokenize{}

	headers, err := translator.ResponseHeaders(map[string]string{
		"content-type":  "application/json",
		"custom-header": "value",
		"cache-control": "no-cache",
	})

	require.NoError(t, err)
	require.Nil(t, headers) // Current implementation returns nil
}

func TestToGCPAnthropicV1Tokenize_HelperMethods(t *testing.T) {
	translator := &ToGCPAnthropicV1Tokenize{}

	t.Run("openAIToAnthropicCountTokensParams", func(t *testing.T) {
		chatReq := &tokenize.ChatRequest{
			Model: "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Test message"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		}

		anthropicReq, err := openAIToAnthropicCountTokensParams(chatReq, "claude-3-opus-20240229")
		require.NoError(t, err)
		require.NotNil(t, anthropicReq)
		require.NotNil(t, anthropicReq.Messages)
		require.Equal(t, anthropic.Model("claude-3-opus-20240229"), anthropicReq.Model)
		require.Len(t, anthropicReq.Messages, 1)
		require.Equal(t, anthropic.MessageParamRoleUser, anthropicReq.Messages[0].Role)
	})

	t.Run("anthropicTokensCountToResponse", func(t *testing.T) {
		anthropicResp := &anthropic.MessageTokensCount{
			InputTokens: 25,
		}

		tokenizeResp, err := translator.anthropicTokensCountToResponse(anthropicResp)
		require.NoError(t, err)
		require.NotNil(t, tokenizeResp)
		require.Equal(t, 25, tokenizeResp.Count)
	})

	t.Run("openAIToAnthropicCountTokensParams with empty messages", func(t *testing.T) {
		chatReq := &tokenize.ChatRequest{
			Model:    "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{}, // Empty messages
		}

		_, err := openAIToAnthropicCountTokensParams(chatReq, "claude-3-opus-20240229")
		require.NoError(t, err) // Empty messages are valid for Anthropic
	})
}

func TestToGCPAnthropicV1Tokenize_IntegrationScenarios(t *testing.T) {
	t.Run("complete flow - request to response", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		// Step 1: Process request
		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{Value: "Count tokens in this message"},
							Role:    openai.ChatMessageRoleUser,
						},
					},
				},
			},
		}

		reqHeaders, reqBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, reqBody)
		require.Len(t, reqHeaders, 2)

		// Step 2: Simulate Anthropic response
		anthropicResponse := &anthropic.MessageTokensCount{
			InputTokens: 7, // "Count tokens in this message" = 7 tokens
		}
		anthropicResponseJSON, err := json.Marshal(anthropicResponse)
		require.NoError(t, err)

		// Step 3: Process response
		_, respBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(anthropicResponseJSON),
			false,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, respBody)
		require.Equal(t, "claude-3-opus-20240229", responseModel)

		// Step 4: Verify final OpenAI response
		var finalResp tokenize.Response
		require.NoError(t, json.Unmarshal(respBody, &finalResp))
		require.Equal(t, 7, finalResp.Count)

		// Token usage should ideally be populated but currently isn't
		_, hasTokens := tokenUsage.InputTokens()
		require.False(t, hasTokens) // Documents current limitation
	})

	t.Run("error flow - request error to response error", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		// Test error handling
		headers := map[string]string{
			":status":      "400",
			"content-type": "application/json",
		}

		errorBody := `{
			"type": "error",
			"error": {
				"type": "invalid_request_error",
				"message": "Invalid model specified"
			}
		}`

		_, respBody, err := translator.ResponseError(headers, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, respBody)

		var errorResp openai.Error
		require.NoError(t, json.Unmarshal(respBody, &errorResp))
		require.Equal(t, "error", errorResp.Type)
		require.Equal(t, "invalid_request_error", errorResp.Error.Type)
		require.Contains(t, errorResp.Error.Message, "Invalid model specified")
	})
}

func TestToGCPAnthropicV1Tokenize_SystemBlocks(t *testing.T) {
	t.Run("single system block uses string format", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Role:    openai.ChatMessageRoleSystem,
							Content: openai.ContentUnion{Value: "You are helpful"},
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.True(t, anthropicReq.System.OfString.Valid())
		require.Equal(t, "You are helpful", anthropicReq.System.OfString.Value)
	})

	t.Run("multiple system blocks use array format", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Role:    openai.ChatMessageRoleSystem,
							Content: openai.ContentUnion{Value: "System instruction 1"},
						},
					},
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Role:    openai.ChatMessageRoleSystem,
							Content: openai.ContentUnion{Value: "System instruction 2"},
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Len(t, anthropicReq.System.OfTextBlockArray, 2)
		require.Equal(t, "System instruction 1", anthropicReq.System.OfTextBlockArray[0].Text)
		require.Equal(t, "System instruction 2", anthropicReq.System.OfTextBlockArray[1].Text)
	})
}

func TestToGCPAnthropicV1Tokenize_ToolConversion(t *testing.T) {
	t.Run("tools converted to Anthropic format", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the current weather",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
								},
								"required": []any{"location"},
							},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Len(t, anthropicReq.Tools, 1)
	})

	t.Run("tool with nil function skipped", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
				Tools: []openai.Tool{
					{Type: "function", Function: nil},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Empty(t, anthropicReq.Tools)
	})
}

func TestToGCPAnthropicV1Tokenize_ModelVersionStripping(t *testing.T) {
	t.Run("@default suffix stripped from model override", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			modelNameOverride: "claude-3-opus@default",
		}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "original-model",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "claude-3-opus", translator.requestModel)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Equal(t, anthropic.Model("claude-3-opus"), anthropicReq.Model)
	})

	t.Run("@latest suffix stripped from model override", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			modelNameOverride: "claude-3-opus@latest",
		}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "original-model",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "claude-3-opus", translator.requestModel)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Equal(t, anthropic.Model("claude-3-opus"), anthropicReq.Model)
	})

	t.Run("@default suffix stripped from request model without override", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus@default",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
					}},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "claude-3-opus", translator.requestModel)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Equal(t, anthropic.Model("claude-3-opus"), anthropicReq.Model)
	})

	t.Run("@latest suffix stripped from request model without override", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-sonnet-4-6@latest",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
					}},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "claude-sonnet-4-6", translator.requestModel)

		var anthropicReq anthropic.MessageCountTokensParams
		require.NoError(t, json.Unmarshal(body, &anthropicReq))
		require.Equal(t, anthropic.Model("claude-sonnet-4-6"), anthropicReq.Model)
	})
}

func TestToGCPAnthropicV1Tokenize_AnthropicVersion(t *testing.T) {
	t.Run("custom API version included in body", func(t *testing.T) {
		translator := &ToGCPAnthropicV1Tokenize{
			apiVersion: "custom-version-2024",
		}

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(body, &raw))
		require.Equal(t, "custom-version-2024", raw["anthropic_version"])
	})

	t.Run("default API version when not set", func(t *testing.T) {
		translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "claude-3-opus-20240229",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(body, &raw))
		require.Equal(t, "vertex-2023-10-16", raw["anthropic_version"])
	})
}

func TestTranslateGCPAnthropicErrorToOpenAI(t *testing.T) {
	t.Run("JSON decode failure", func(t *testing.T) {
		_, _, err := translateGCPAnthropicErrorToOpenAI(
			map[string]string{
				":status":      "400",
				"content-type": "application/json",
			},
			strings.NewReader("not valid json{{{"),
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal JSON error body")
	})
}

// Benchmark tests for performance
func BenchmarkToGCPAnthropicV1Tokenize_RequestBody(b *testing.B) {
	translator := NewTokenizeToGCPAnthropicTranslator("", "").(*ToGCPAnthropicV1Tokenize)
	req := &tokenize.RequestUnion{
		ChatRequest: &tokenize.ChatRequest{
			Model: "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Benchmark test message"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := translator.RequestBody(nil, req, false)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkToGCPAnthropicV1Tokenize_ResponseBody(b *testing.B) {
	translator := &ToGCPAnthropicV1Tokenize{requestModel: "claude-3-opus-20240229"}
	anthropicResp := &anthropic.MessageTokensCount{
		InputTokens: 100,
	}
	responseJSON, _ := json.Marshal(anthropicResp)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _, err := translator.ResponseBody(nil, bytes.NewReader(responseJSON), false, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

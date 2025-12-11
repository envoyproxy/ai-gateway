// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
)

func TestNewTokenizeToAWSBedrockTranslator(t *testing.T) {
	translator := NewTokenizeToAWSBedrockTranslator("")

	require.NotNil(t, translator)
	concrete := translator.(*ToAWSBedrockTranslatorV1Tokenize)
	require.Empty(t, concrete.modelNameOverride)
	require.Empty(t, concrete.requestModel)
}

func TestToAWSBedrockTranslatorV1Tokenize_RequestBody(t *testing.T) {
	t.Run("chat request - no model override", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		req := &tokenize.TokenizeRequestUnion{
			TokenizeChatRequest: &tokenize.TokenizeChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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
		require.Equal(t, "anthropic.claude-3-opus-20240229-v1:0", translator.requestModel)
		require.Len(t, headers, 2)
		require.Equal(t, pathHeaderName, headers[0].Key())
		require.Contains(t, headers[0].Value(), "/model/")
		require.Contains(t, headers[0].Value(), "/count-tokens")
		require.Equal(t, contentLengthHeaderName, headers[1].Key())
		require.Equal(t, strconv.Itoa(len(body)), headers[1].Value())

		// Verify the body contains AWS Bedrock CountTokens request
		var bedrockReq awsbedrock.CountTokensInput
		require.NoError(t, json.Unmarshal(body, &bedrockReq))
		require.NotNil(t, bedrockReq.Messages)
		require.Len(t, bedrockReq.Messages, 1)
		require.Equal(t, awsbedrock.ConversationRoleUser, bedrockReq.Messages[0].Role)
		require.Len(t, bedrockReq.Messages[0].Content, 1)
	})

	t.Run("chat request - with model override", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{
			modelNameOverride: "override-model",
		}

		req := &tokenize.TokenizeRequestUnion{
			TokenizeChatRequest: &tokenize.TokenizeChatRequest{
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
		require.Contains(t, headers[0].Value(), "override-model")
	})

	t.Run("chat request - with system instruction", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		req := &tokenize.TokenizeRequestUnion{
			TokenizeChatRequest: &tokenize.TokenizeChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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

		var bedrockReq awsbedrock.CountTokensInput
		require.NoError(t, json.Unmarshal(body, &bedrockReq))
		require.NotNil(t, bedrockReq.System)
		require.Len(t, bedrockReq.System, 1)
		require.Equal(t, "You are a helpful assistant", bedrockReq.System[0].Text)
		require.Len(t, bedrockReq.Messages, 1) // System message should not be in messages
		require.Equal(t, awsbedrock.ConversationRoleUser, bedrockReq.Messages[0].Role)
	})

	t.Run("completion request - not supported", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		req := &tokenize.TokenizeRequestUnion{
			TokenizeCompletionRequest: &tokenize.TokenizeCompletionRequest{
				Model:  "anthropic.claude-3-opus-20240229-v1:0",
				Prompt: "Hello world",
			},
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "only TokenizeChatRequest is supported for AWS Bedrock models")
	})

	t.Run("invalid union - both types set", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		req := &tokenize.TokenizeRequestUnion{
			TokenizeCompletionRequest: &tokenize.TokenizeCompletionRequest{
				Model:  "anthropic.claude-3-opus-20240229-v1:0",
				Prompt: "Hello",
			},
			TokenizeChatRequest: &tokenize.TokenizeChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		req := &tokenize.TokenizeRequestUnion{
			// Neither completion nor chat request set
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "one request type must be set")
	})
}

func TestToAWSBedrockTranslatorV1Tokenize_ResponseBody(t *testing.T) {
	t.Run("valid AWS Bedrock response", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{
			requestModel: "anthropic.claude-3-opus-20240229-v1:0",
		}

		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 42,
		}

		responseJSON, err := json.Marshal(bedrockResp)
		require.NoError(t, err)

		headers, body, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil, // No span for simplicity
		)

		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "anthropic.claude-3-opus-20240229-v1:0", responseModel)
		require.Len(t, headers, 1)
		require.Equal(t, contentLengthHeaderName, headers[0].Key())

		// Verify the response is converted to OpenAI format
		var openAIResp tokenize.TokenizeResponse
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 42, openAIResp.Count)

		// Token usage should be extracted from Bedrock response
		_, hasTokens := tokenUsage.InputTokens()
		require.False(t, hasTokens) // Current behavior - token usage not implemented
	})

	t.Run("empty response model fallback", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{
			requestModel: "anthropic.claude-3-haiku-20240307-v1:0",
		}

		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 0,
		}

		responseJSON, err := json.Marshal(bedrockResp)
		require.NoError(t, err)

		_, _, _, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)
		require.Equal(t, "anthropic.claude-3-haiku-20240307-v1:0", responseModel) // Falls back to request model
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{}

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
		translator := &ToAWSBedrockTranslatorV1Tokenize{}

		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 0, // Zero tokens
		}

		responseJSON, err := json.Marshal(bedrockResp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)

		var openAIResp tokenize.TokenizeResponse
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 0, openAIResp.Count)
	})

	t.Run("response conversion handles large token count", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{}

		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 100000, // Large token count
		}

		responseJSON, err := json.Marshal(bedrockResp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)

		var openAIResp tokenize.TokenizeResponse
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 100000, openAIResp.Count)
	})

	t.Run("response handles zero input tokens", func(t *testing.T) {
		translator := &ToAWSBedrockTranslatorV1Tokenize{}

		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 0, // Zero input tokens
		}

		responseJSON, err := json.Marshal(bedrockResp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(
			nil,
			bytes.NewReader(responseJSON),
			false,
			nil,
		)

		require.NoError(t, err)

		var openAIResp tokenize.TokenizeResponse
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 0, openAIResp.Count)
	})
}

func TestToAWSBedrockTranslatorV1Tokenize_ResponseError(t *testing.T) {
	translator := &ToAWSBedrockTranslatorV1Tokenize{}

	tests := []struct {
		name            string
		responseHeaders map[string]string
		input           string
		expectedType    string
		expectedCode    string
		expectedMessage string
	}{
		{
			name: "AWS Bedrock structured error response",
			responseHeaders: map[string]string{
				":status":          "400",
				"content-type":     "application/json",
				"x-amzn-errortype": "ValidationException",
			},
			input: `{
				"message": "Invalid model ARN specified"
			}`,
			expectedType:    "ValidationException",
			expectedCode:    "400",
			expectedMessage: "Invalid model ARN specified",
		},
		{
			name: "Non-JSON error response",
			responseHeaders: map[string]string{
				":status":      "503",
				"content-type": "text/plain",
			},
			input:           "Service temporarily unavailable",
			expectedType:    awsBedrockBackendError,
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
			expectedType:    awsBedrockBackendError,
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
			require.Equal(t, jsonContentType, headers[0].Value())
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
		require.Contains(t, err.Error(), "failed to read error body")
	})
}

func TestToAWSBedrockTranslatorV1Tokenize_ResponseHeaders(t *testing.T) {
	translator := &ToAWSBedrockTranslatorV1Tokenize{}

	headers, err := translator.ResponseHeaders(map[string]string{
		"content-type":  "application/json",
		"custom-header": "value",
		"cache-control": "no-cache",
	})

	require.NoError(t, err)
	require.Nil(t, headers) // Current implementation returns nil
}

func TestToAWSBedrockTranslatorV1Tokenize_HelperMethods(t *testing.T) {
	translator := &ToAWSBedrockTranslatorV1Tokenize{}

	t.Run("tokenizeToBedrockCountTokens", func(t *testing.T) {
		chatReq := &tokenize.TokenizeChatRequest{
			Model: "anthropic.claude-3-opus-20240229-v1:0",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Test message"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		}

		bedrockReq, err := translator.tokenizeToBedrockCountTokens(chatReq)
		require.NoError(t, err)
		require.NotNil(t, bedrockReq)
		require.NotNil(t, bedrockReq.Messages)
		require.Len(t, bedrockReq.Messages, 1)
		require.Equal(t, awsbedrock.ConversationRoleUser, bedrockReq.Messages[0].Role)
	})

	t.Run("bedrockCountTokensToTokenizeResponse", func(t *testing.T) {
		bedrockResp := &awsbedrock.CountTokensResponse{
			InputTokens: 25,
		}

		tokenizeResp, err := translator.bedrockCountTokensToTokenizeResponse(bedrockResp)
		require.NoError(t, err)
		require.NotNil(t, tokenizeResp)
		require.Equal(t, 25, tokenizeResp.Count)
	})

	t.Run("tokenizeToBedrockCountTokens with empty messages", func(t *testing.T) {
		chatReq := &tokenize.TokenizeChatRequest{
			Model:    "anthropic.claude-3-opus-20240229-v1:0",
			Messages: []openai.ChatCompletionMessageParamUnion{}, // Empty messages
		}

		_, err := translator.tokenizeToBedrockCountTokens(chatReq)
		require.NoError(t, err) // Empty messages are valid for Bedrock
	})
}

func TestToAWSBedrockTranslatorV1Tokenize_IntegrationScenarios(t *testing.T) {
	t.Run("complete flow - request to response", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		// Step 1: Process request
		req := &tokenize.TokenizeRequestUnion{
			TokenizeChatRequest: &tokenize.TokenizeChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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

		// Step 2: Simulate AWS Bedrock CountTokens response
		bedrockResponse := &awsbedrock.CountTokensResponse{
			InputTokens: 7, // "Count tokens in this message" = 7 tokens
		}
		bedrockResponseJSON, err := json.Marshal(bedrockResponse)
		require.NoError(t, err)

		// Step 3: Process response
		_, respBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader(bedrockResponseJSON),
			false,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, respBody)
		require.Equal(t, "anthropic.claude-3-opus-20240229-v1:0", responseModel)

		// Step 4: Verify final OpenAI response
		var finalResp tokenize.TokenizeResponse
		require.NoError(t, json.Unmarshal(respBody, &finalResp))
		require.Equal(t, 7, finalResp.Count)

		// Token usage should ideally be populated but currently isn't
		_, hasTokens := tokenUsage.InputTokens()
		require.False(t, hasTokens) // Documents current limitation
	})

	t.Run("error flow - request error to response error", func(t *testing.T) {
		translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)

		// Test error handling
		headers := map[string]string{
			":status":          "400",
			"content-type":     "application/json",
			"x-amzn-errortype": "ValidationException",
		}

		errorBody := `{
			"message": "Invalid model ARN specified"
		}`

		_, respBody, err := translator.ResponseError(headers, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, respBody)

		var errorResp openai.Error
		require.NoError(t, json.Unmarshal(respBody, &errorResp))
		require.Equal(t, "error", errorResp.Type)
		require.Equal(t, "ValidationException", errorResp.Error.Type)
		require.Contains(t, errorResp.Error.Message, "Invalid model ARN specified")
	})
}

// Benchmark tests for performance
func BenchmarkToAWSBedrockTranslatorV1Tokenize_RequestBody(b *testing.B) {
	translator := NewTokenizeToAWSBedrockTranslator("").(*ToAWSBedrockTranslatorV1Tokenize)
	req := &tokenize.TokenizeRequestUnion{
		TokenizeChatRequest: &tokenize.TokenizeChatRequest{
			Model: "anthropic.claude-3-opus-20240229-v1:0",
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

func BenchmarkToAWSBedrockTranslatorV1Tokenize_ResponseBody(b *testing.B) {
	translator := &ToAWSBedrockTranslatorV1Tokenize{requestModel: "anthropic.claude-3-opus-20240229-v1:0"}
	bedrockResp := &awsbedrock.CountTokensResponse{
		InputTokens: 100,
	}
	responseJSON, _ := json.Marshal(bedrockResp)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _, err := translator.ResponseBody(nil, bytes.NewReader(responseJSON), false, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

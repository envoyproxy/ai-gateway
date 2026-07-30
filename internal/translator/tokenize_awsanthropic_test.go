// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestNewTokenizeToAWSAnthropicTranslator(t *testing.T) {
	translator := NewTokenizeToAWSAnthropicTranslator("bedrock-2023-05-31", "override-model")
	require.NotNil(t, translator)
	impl, ok := translator.(*ToAWSAnthropicV1Tokenize)
	require.True(t, ok)
	require.Equal(t, "bedrock-2023-05-31", impl.apiVersion)
	require.Equal(t, "override-model", impl.modelNameOverride)
}

func TestToAWSAnthropicV1Tokenize_RequestBody(t *testing.T) {
	t.Run("chat request - no model override", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					}},
				},
			},
		}

		headers, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Len(t, headers, 2)
		require.Contains(t, headers[0].Value(), "/model/anthropic.claude-3-opus-20240229-v1")
		require.Contains(t, headers[0].Value(), "/count-tokens")
		require.NotNil(t, body)
		require.Equal(t, "anthropic.claude-3-opus-20240229-v1:0", translator.requestModel)

		// Verify InvokeModel wrapper structure
		parsed := gjson.ParseBytes(body)
		require.True(t, parsed.Get("input.invokeModel.body").Exists())
		require.False(t, parsed.Get("input.converse").Exists())
	})

	t.Run("chat request - with model override", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "override-model").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "original-model",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					}},
				},
			},
		}

		headers, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Contains(t, headers[0].Value(), "override-model")
		require.Equal(t, "override-model", translator.requestModel)
	})

	t.Run("chat request - with system instruction", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role:    openai.ChatMessageRoleSystem,
						Content: openai.ContentUnion{Value: "You are helpful"},
					}},
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					}},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		// Decode the InvokeModel body and verify system is set
		innerBody := decodeInvokeModelBody(t, body)
		require.True(t, gjson.GetBytes(innerBody, "system").Exists())
	})

	t.Run("chat request - with multiple system blocks", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role:    openai.ChatMessageRoleSystem,
						Content: openai.ContentUnion{Value: "System instruction 1"},
					}},
					{OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role:    openai.ChatMessageRoleSystem,
						Content: openai.ContentUnion{Value: "System instruction 2"},
					}},
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					}},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		innerBody := decodeInvokeModelBody(t, body)
		system := gjson.GetBytes(innerBody, "system")
		require.True(t, system.Exists())
		require.True(t, system.IsArray())
		require.Len(t, system.Array(), 2)
	})

	t.Run("chat request - with tools", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
					}},
				},
				Tools: []openai.Tool{
					{
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
								},
								"required":             []any{"location"},
								"additionalProperties": false,
							},
						},
					},
					{Function: nil}, // nil function should be skipped
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		innerBody := decodeInvokeModelBody(t, body)
		tools := gjson.GetBytes(innerBody, "tools")
		require.True(t, tools.Exists())
		require.Len(t, tools.Array(), 1)
		require.Equal(t, "get_weather", tools.Array()[0].Get("name").String())

		inputSchema := tools.Array()[0].Get("input_schema")
		require.True(t, inputSchema.Exists())
		require.Equal(t, "object", inputSchema.Get("type").String())
		require.True(t, inputSchema.Get("properties.location").Exists())
	})

	t.Run("chat request - tool with no parameters", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
					}},
				},
				Tools: []openai.Tool{
					{
						Function: &openai.FunctionDefinition{
							Name:       "no_params_tool",
							Parameters: nil,
						},
					},
				},
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		innerBody := decodeInvokeModelBody(t, body)
		tools := gjson.GetBytes(innerBody, "tools")
		require.True(t, tools.Exists())
		require.Len(t, tools.Array(), 1)
		require.Equal(t, "no_params_tool", tools.Array()[0].Get("name").String())
	})

	t.Run("completion request - converted to chat", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			CompletionRequest: &tokenize.CompletionRequest{
				Model:  "anthropic.claude-3-opus-20240229-v1:0",
				Prompt: "Hello world",
			},
		}

		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		innerBody := decodeInvokeModelBody(t, body)
		messages := gjson.GetBytes(innerBody, "messages")
		require.True(t, messages.Exists())
		require.Len(t, messages.Array(), 1)
		require.Equal(t, "user", messages.Array()[0].Get("role").String())
	})

	t.Run("invalid union - both types set", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			CompletionRequest: &tokenize.CompletionRequest{Model: "m", Prompt: "p"},
			ChatRequest:       &tokenize.ChatRequest{Model: "m"},
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "only one request type can be set")
	})

	t.Run("invalid union - no types set", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		_, _, err := translator.RequestBody(nil, &tokenize.RequestUnion{}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "one request type must be set")
	})
}

func TestToAWSAnthropicV1Tokenize_InvokeModelBodyWrapping(t *testing.T) {
	translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

	req := &tokenize.RequestUnion{
		ChatRequest: &tokenize.ChatRequest{
			Model: "anthropic.claude-3-opus-20240229-v1:0",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
				}},
			},
		},
	}

	_, body, err := translator.RequestBody(nil, req, false)
	require.NoError(t, err)

	// Verify outer structure
	parsed := gjson.ParseBytes(body)
	require.True(t, parsed.Get("input.invokeModel.body").Exists())
	require.False(t, parsed.Get("input.converse").Exists())

	// Decode inner body
	innerBody := decodeInvokeModelBody(t, body)

	// Must have messages
	require.True(t, gjson.GetBytes(innerBody, "messages").Exists())

	// Must have anthropic_version (defaults to bedrock-2023-05-31)
	require.Equal(t, BedrockDefaultVersion, gjson.GetBytes(innerBody, "anthropic_version").String())

	// Must have max_tokens: 1 (workaround for InvokeModel validation)
	require.Equal(t, int64(1), gjson.GetBytes(innerBody, "max_tokens").Int())

	// Must NOT have model key (model is in URL path)
	require.False(t, gjson.GetBytes(innerBody, "model").Exists())
}

func TestToAWSAnthropicV1Tokenize_CRISPrefixStripping(t *testing.T) {
	t.Run("CRIS prefix stripped from model path", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "us.anthropic.claude-sonnet-4-6",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
					}},
				},
			},
		}

		headers, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Contains(t, headers[0].Value(), "anthropic.claude-sonnet-4-6")
		require.NotContains(t, headers[0].Value(), "us.anthropic")
	})

	t.Run("non-CRIS prefix not stripped", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-haiku-20240307-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "Hi"},
					}},
				},
			},
		}

		headers, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Contains(t, headers[0].Value(), "anthropic.claude-3-haiku-20240307-v1:0")
	})
}

func TestToAWSAnthropicV1Tokenize_AnthropicVersion(t *testing.T) {
	t.Run("default version used when apiVersion empty", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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

		innerBody := decodeInvokeModelBody(t, body)
		require.Equal(t, BedrockDefaultVersion, gjson.GetBytes(innerBody, "anthropic_version").String())
	})

	t.Run("custom apiVersion used when set", func(t *testing.T) {
		translator := NewTokenizeToAWSAnthropicTranslator("custom-2024-01-01", "").(*ToAWSAnthropicV1Tokenize)

		req := &tokenize.RequestUnion{
			ChatRequest: &tokenize.ChatRequest{
				Model: "anthropic.claude-3-opus-20240229-v1:0",
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

		innerBody := decodeInvokeModelBody(t, body)
		require.Equal(t, "custom-2024-01-01", gjson.GetBytes(innerBody, "anthropic_version").String())
	})
}

func TestToAWSAnthropicV1Tokenize_ResponseBody(t *testing.T) {
	t.Run("valid AWS response", func(t *testing.T) {
		translator := &ToAWSAnthropicV1Tokenize{requestModel: "anthropic.claude-3-opus-20240229-v1:0"}

		resp := &awsbedrock.CountTokensResponse{InputTokens: 42}
		responseJSON, err := json.Marshal(resp)
		require.NoError(t, err)

		headers, body, _, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(responseJSON), false, nil)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, "anthropic.claude-3-opus-20240229-v1:0", responseModel)
		require.Len(t, headers, 1)
		require.Equal(t, contentLengthHeaderName, headers[0].Key())

		var openAIResp tokenize.Response
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 42, openAIResp.Count)
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		translator := &ToAWSAnthropicV1Tokenize{}

		_, _, _, _, err := translator.ResponseBody(nil, bytes.NewReader([]byte("invalid")), false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	t.Run("zero tokens", func(t *testing.T) {
		translator := &ToAWSAnthropicV1Tokenize{requestModel: "model"}

		resp := &awsbedrock.CountTokensResponse{InputTokens: 0}
		responseJSON, err := json.Marshal(resp)
		require.NoError(t, err)

		_, body, _, _, err := translator.ResponseBody(nil, bytes.NewReader(responseJSON), false, nil)
		require.NoError(t, err)

		var openAIResp tokenize.Response
		require.NoError(t, json.Unmarshal(body, &openAIResp))
		require.Equal(t, 0, openAIResp.Count)
	})
}

func TestToAWSAnthropicV1Tokenize_ResponseError(t *testing.T) {
	translator := &ToAWSAnthropicV1Tokenize{}

	t.Run("AWS structured error response", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:       "400",
			contentTypeHeaderName:  "application/json",
			awsErrorTypeHeaderName: "ValidationException",
		}
		errorBody := `{"message": "The provided model doesn't support counting tokens"}`

		headers, body, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Len(t, headers, 2)

		var openAIError openai.Error
		require.NoError(t, json.Unmarshal(body, &openAIError))
		require.Equal(t, "ValidationException", openAIError.Error.Type)
		require.Contains(t, openAIError.Error.Message, "counting tokens")
	})

	t.Run("non-JSON error response", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "500",
			contentTypeHeaderName: "text/plain",
		}

		headers, body, err := translator.ResponseError(respHeaders, strings.NewReader("Internal Server Error"))
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Len(t, headers, 2)

		var openAIError openai.Error
		require.NoError(t, json.Unmarshal(body, &openAIError))
		require.Equal(t, awsBedrockBackendError, openAIError.Error.Type)
		require.Equal(t, "Internal Server Error", openAIError.Error.Message)
	})

	t.Run("read error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "500",
			contentTypeHeaderName: "text/plain",
		}

		_, _, err := translator.ResponseError(respHeaders, &errorReader{})
		require.Error(t, err)
	})
}

func TestToAWSAnthropicV1Tokenize_ResponseHeaders(t *testing.T) {
	translator := &ToAWSAnthropicV1Tokenize{}
	headers, err := translator.ResponseHeaders(nil)
	require.NoError(t, err)
	require.Nil(t, headers)
}

// decodeInvokeModelBody extracts and base64-decodes the inner body from an InvokeModel wrapper.
func decodeInvokeModelBody(t *testing.T, outerBody []byte) []byte {
	t.Helper()
	b64Body := gjson.GetBytes(outerBody, "input.invokeModel.body").String()
	require.NotEmpty(t, b64Body)
	decoded, err := base64.StdEncoding.DecodeString(b64Body)
	require.NoError(t, err)
	return decoded
}

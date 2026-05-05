// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestResponseModel_Anthropic(t *testing.T) {
	modelName := "claude-sonnet-4@20250514"
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", modelName)

	req := &openai.ChatCompletionRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
		},
	}
	reqBody, _ := json.Marshal(req)
	_, _, err := translator.RequestBody(reqBody, req, false)
	require.NoError(t, err)

	anthropicResponse := anthropic.Message{
		ID:   "msg_01XYZ",
		Type: constant.ValueOf[constant.Message](),
		Role: constant.ValueOf[constant.Assistant](),
		Content: []anthropic.ContentBlockUnion{
			{
				Type: "text",
				Text: "Hello!",
			},
		},
		StopReason: anthropic.StopReasonEndTurn,
		Usage: anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}

	body, err := json.Marshal(anthropicResponse)
	require.NoError(t, err)

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel)
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(10), inputTokens)
	outputTokens, ok := tokenUsage.OutputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(5), outputTokens)
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	openAIReq := &openai.ChatCompletionRequest{
		Model: "claude-3-opus-20240229",
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{Content: openai.ContentUnion{Value: "You are a helpful assistant."}, Role: openai.ChatMessageRoleSystem},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"}, Role: openai.ChatMessageRoleUser},
			},
		},
		MaxTokens:   ptr.To(int64(1024)),
		Temperature: ptr.To(0.7),
	}

	t.Run("basic request", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, body)

		// Verify path header.
		require.Equal(t, pathHeaderName, hm[0].Key())
		require.Equal(t, "/v1/messages", hm[0].Value())

		// Verify anthropic-version header is present and body has no anthropic_version.
		var versionHeaderFound bool
		for _, h := range hm {
			if h.Key() == "anthropic-version" {
				require.Equal(t, DefaultAnthropicVersion, h.Value())
				versionHeaderFound = true
			}
		}
		require.True(t, versionHeaderFound, "anthropic-version header should be present")

		// Verify body has no anthropic_version field (native API uses header, not body).
		require.False(t, gjson.GetBytes(body, "anthropic_version").Exists())

		// Verify body has no model field (native API does not require model in body).
		require.False(t, gjson.GetBytes(body, "model").Exists())
	})

	t.Run("model name override", func(t *testing.T) {
		override := "claude-3"
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", override)
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		// Path should still be /v1/messages (native API doesn't encode model in path).
		require.Equal(t, pathHeaderName, hm[0].Key())
		require.Equal(t, "/v1/messages", hm[0].Value())

		// System prompt text should still be present.
		require.Equal(t, "You are a helpful assistant.", gjson.GetBytes(body, "system.0.text").String())
	})

	t.Run("api version override", func(t *testing.T) {
		customVersion := "2024-02-15"
		translator := NewChatCompletionOpenAIToAnthropicTranslator(customVersion, "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		var versionHeaderFound bool
		for _, h := range hm {
			if h.Key() == "anthropic-version" {
				require.Equal(t, customVersion, h.Value())
				versionHeaderFound = true
			}
		}
		require.True(t, versionHeaderFound)

		require.Equal(t, openAIReq.Messages[1].OfUser.Content.Value.(string),
			gjson.GetBytes(body, "messages.0.content.0.text").String())
	})

	t.Run("invalid temperature above bound", func(t *testing.T) {
		invalidReq := &openai.ChatCompletionRequest{
			Model:       "claude-3-opus-20240229",
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.5),
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidReq, false)
		require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody)
	})

	t.Run("invalid temperature below bound", func(t *testing.T) {
		invalidReq := &openai.ChatCompletionRequest{
			Model:       "claude-3-opus-20240229",
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(-2.5),
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidReq, false)
		require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody)
	})

	t.Run("missing max tokens passes with zero", func(t *testing.T) {
		missingReq := &openai.ChatCompletionRequest{
			Model:    "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, missingReq, false)
		require.NoError(t, err)
		require.Equal(t, int64(0), gjson.GetBytes(body, "max_tokens").Int())
	})

	t.Run("streaming request", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		require.Equal(t, pathHeaderName, hm[0].Key())
		require.Equal(t, "/v1/messages", hm[0].Value())
		require.True(t, gjson.GetBytes(body, "stream").Bool(), `body should contain "stream": true`)
	})

	t.Run("request with thinking enabled", func(t *testing.T) {
		thinkingReq := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Thinking: &openai.ThinkingUnion{
				OfEnabled: &openai.ThinkingEnabled{
					BudgetTokens:    100,
					Type:            "enabled",
					IncludeThoughts: true,
				},
			},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		thinkingBlock := gjson.GetBytes(body, "thinking")
		require.True(t, thinkingBlock.Exists())
		require.True(t, thinkingBlock.IsObject())
		require.Equal(t, "enabled", thinkingBlock.Map()["type"].String())
	})

	t.Run("request with thinking disabled", func(t *testing.T) {
		thinkingReq := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Thinking: &openai.ThinkingUnion{
				OfDisabled: &openai.ThinkingDisabled{
					Type: "disabled",
				},
			},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		thinkingBlock := gjson.GetBytes(body, "thinking")
		require.True(t, thinkingBlock.Exists())
		require.True(t, thinkingBlock.IsObject())
		require.Equal(t, "disabled", thinkingBlock.Map()["type"].String())
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid json body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, _, _, err := translator.ResponseBody(map[string]string{statusHeaderName: "200"}, bytes.NewBufferString("invalid json"), true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	tests := []struct {
		name                   string
		inputResponse          *anthropic.Message
		respHeaders            map[string]string
		expectedOpenAIResponse openai.ChatCompletionResponse
	}{
		{
			name: "basic text response",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ123",
				Model:      "claude-3-5-sonnet-20241022",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "Hello there!"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 5, CacheCreationInputTokens: 3},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     18,
					CompletionTokens: 20,
					TotalTokens:      38,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens:        5,
						CacheCreationTokens: 3,
					},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						Message:      openai.ChatCompletionResponseChoiceMessage{Role: "assistant", Content: ptr.To("Hello there!")},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
		{
			name: "response with tool use",
			inputResponse: &anthropic.Message{
				ID:    "msg_01XYZ123",
				Model: "claude-3-5-sonnet-20241022",
				Role:  constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content: []anthropic.ContentBlockUnion{
					{Type: "text", Text: "Ok, I will call the tool."},
					{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: []byte(`{"location":"Tokyo","unit":"celsius"}`)},
				},
				StopReason: anthropic.StopReasonToolUse,
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15, CacheReadInputTokens: 10, CacheCreationInputTokens: 7},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens: 42, CompletionTokens: 15, TotalTokens: 57,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens:        10,
						CacheCreationTokens: 7,
					},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Role:    string(anthropic.MessageParamRoleAssistant),
							Content: ptr.To("Ok, I will call the tool."),
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID:   ptr.To("toolu_01"),
									Type: openai.ChatCompletionMessageToolCallTypeFunction,
									Function: openai.ChatCompletionMessageToolCallFunctionParam{
										Name:      "get_weather",
										Arguments: `{"location":"Tokyo","unit":"celsius"}`,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "response with thinking content",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ456",
				Model:      "claude-3-5-sonnet-20241022",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "thinking", Thinking: "Let me think about this...", Signature: "signature_123"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 15, OutputTokens: 25, CacheReadInputTokens: 3},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ456",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     18,
					CompletionTokens: 25,
					TotalTokens:      43,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens: 3,
					},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index: 0,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Role: "assistant",
							ReasoningContent: &openai.ReasoningContentUnion{
								Value: &openai.ReasoningContent{
									ReasoningContent: &awsbedrock.ReasoningContentBlock{
										ReasoningText: &awsbedrock.ReasoningTextBlock{
											Text:      "Let me think about this...",
											Signature: "signature_123",
										},
									},
								},
							},
						},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
		{
			name: "response with redacted thinking content",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ789",
				Model:      "claude-3-5-sonnet-20241022",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "redacted_thinking", Data: "redacted_data_content"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 12, OutputTokens: 18, CacheReadInputTokens: 1},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ789",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     13,
					CompletionTokens: 18,
					TotalTokens:      31,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens: 1,
					},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index: 0,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Role: "assistant",
							ReasoningContent: &openai.ReasoningContentUnion{
								Value: &openai.ReasoningContent{
									ReasoningContent: &awsbedrock.ReasoningContentBlock{
										RedactedContent: []byte("redacted_data_content"),
									},
								},
							},
						},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.inputResponse)
			require.NoError(t, err)

			translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
			hm, body, usedToken, _, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true, nil)

			require.NoError(t, err)
			require.NotNil(t, hm)
			require.NotNil(t, body)

			require.Len(t, hm, 1)
			require.Equal(t, contentLengthHeaderName, hm[0].Key())
			require.Equal(t, strconv.Itoa(len(body)), hm[0].Value())

			var gotResp openai.ChatCompletionResponse
			err = json.Unmarshal(body, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := tokenUsageFrom(
				int32(tt.expectedOpenAIResponse.Usage.PromptTokens),
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CachedTokens),
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CacheCreationTokens),
				int32(tt.expectedOpenAIResponse.Usage.CompletionTokens),
				int32(tt.expectedOpenAIResponse.Usage.TotalTokens),
				-1,
			)
			require.Equal(t, expectedTokenUsage, usedToken)

			if diff := cmp.Diff(tt.expectedOpenAIResponse, gotResp, cmpopts.IgnoreFields(openai.ChatCompletionResponse{}, "Created")); diff != "" {
				t.Errorf("ResponseBody mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		responseHeaders map[string]string
		inputBody       any
		expectedOutput  openai.Error
	}{
		{
			name: "non-json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "503",
				contentTypeHeaderName: "text/plain; charset=utf-8",
			},
			inputBody: "Service Unavailable",
			expectedOutput: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    anthropicBackendError,
					Code:    ptr.To("503"),
					Message: "Service Unavailable",
				},
			},
		},
		{
			name: "json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "400",
				contentTypeHeaderName: "application/json",
			},
			inputBody: &anthropic.ErrorResponse{
				Type: "error",
				Error: shared.ErrorObjectUnion{
					Type:    "invalid_request_error",
					Message: "Your max_tokens is too high.",
				},
			},
			expectedOutput: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "invalid_request_error",
					Code:    ptr.To("400"),
					Message: "Your max_tokens is too high.",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader
			if bodyStr, ok := tt.inputBody.(string); ok {
				reader = bytes.NewBufferString(bodyStr)
			} else {
				bodyBytes, err := json.Marshal(tt.inputBody)
				require.NoError(t, err)
				reader = bytes.NewBuffer(bodyBytes)
			}

			o := &openAIToAnthropicTranslatorV1ChatCompletion{}
			hm, body, err := o.ResponseError(tt.responseHeaders, reader)

			require.NoError(t, err)
			require.NotNil(t, body)
			require.NotNil(t, hm)
			require.Len(t, hm, 2)
			require.Equal(t, contentTypeHeaderName, hm[0].Key())
			require.Equal(t, jsonContentType, hm[0].Value())
			require.Equal(t, contentLengthHeaderName, hm[1].Key())
			require.Equal(t, strconv.Itoa(len(body)), hm[1].Value())

			var gotError openai.Error
			err = json.Unmarshal(body, &gotError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedOutput, gotError); diff != "" {
				t.Errorf("ResponseError() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	t.Run("non-streaming", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, err := translator.(*openAIToAnthropicTranslatorV1ChatCompletion).ResponseHeaders(nil)
		require.NoError(t, err)
		require.Empty(t, hm)
	})

	t.Run("streaming", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		hm, err := translator.(*openAIToAnthropicTranslatorV1ChatCompletion).ResponseHeaders(nil)
		require.NoError(t, err)
		require.Len(t, hm, 1)
		require.Equal(t, contentTypeHeaderName, hm[0].Key())
		require.Equal(t, eventStreamContentType, hm[0].Value())
	})
}

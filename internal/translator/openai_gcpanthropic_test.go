// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	openaigo "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

const (
	claudeTestModel = "claude-3-opus-20240229"
)

// TestResponseModel_GCPAnthropic tests that GCP Anthropic (non-streaming) returns the request model
// GCP Anthropic uses deterministic model mapping without virtualization
func TestResponseModel_GCPAnthropic(t *testing.T) {
	modelName := "claude-sonnet-4@20250514"
	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", modelName)

	// Initialize translator with the model
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

	// GCP Anthropic response doesn't have model field, uses Anthropic format
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
	require.Equal(t, modelName, responseModel) // Returns the request model since no virtualization
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(10), inputTokens)
	outputTokens, ok := tokenUsage.OutputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(5), outputTokens)
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	// Define a common input request to use for both standard and vertex tests.
	openAIReq := &openai.ChatCompletionRequest{
		Model: claudeTestModel,
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
	t.Run("Vertex Values Configured Correctly", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, body)

		// Check the path header.
		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", openAIReq.Model)
		require.Equal(t, expectedPath, pathHeader.Value())

		// Check the body content.

		require.NotNil(t, body)
		// Model should NOT be present in the body for GCP Vertex.
		require.False(t, gjson.GetBytes(body, "model").Exists())
		// Anthropic version should be present for GCP Vertex.
		require.Equal(t, anthropicVertex.DefaultVersion, gjson.GetBytes(body, "anthropic_version").String())
	})

	t.Run("Model Name Override", func(t *testing.T) {
		overrideModelName := "claude-3"
		// Instantiate the translator with the model name override.
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", overrideModelName)

		// Call RequestBody with the original request, which has a different model name.
		hm, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		// Check that the :path header uses the override model name.
		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", overrideModelName)
		require.Equal(t, expectedPath, pathHeader.Value())
	})

	t.Run("Image Content Request", func(t *testing.T) {
		imageReq := &openai.ChatCompletionRequest{
			MaxCompletionTokens: ptr.To(int64(200)),
			Model:               "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{OfText: &openai.ChatCompletionContentPartTextParam{Text: "What is in this image?"}},
								{OfImageURL: &openai.ChatCompletionContentPartImageParam{
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/jpeg;base64,dGVzdA==", // "test" in base64.
									},
								}},
							},
						},
						Role: openai.ChatMessageRoleUser,
					},
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)

		imageBlock := gjson.GetBytes(body, "messages.0.content.1")
		require.Equal(t, "image", imageBlock.Get("type").String())
		require.Equal(t, "base64", imageBlock.Get("source.type").String())
		require.Equal(t, "image/jpeg", imageBlock.Get("source.media_type").String())
		require.Equal(t, "dGVzdA==", imageBlock.Get("source.data").String())
	})

	t.Run("Multiple System Prompts Concatenated", func(t *testing.T) {
		firstMsg := "First system prompt."
		secondMsg := "Second developer prompt."
		thirdMsg := "Hello!"
		multiSystemReq := &openai.ChatCompletionRequest{
			Model: claudeTestModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfSystem: &openai.ChatCompletionSystemMessageParam{Content: openai.ContentUnion{Value: firstMsg}, Role: openai.ChatMessageRoleSystem}},
				{OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{Content: openai.ContentUnion{Value: secondMsg}, Role: openai.ChatMessageRoleDeveloper}},
				{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: thirdMsg}, Role: openai.ChatMessageRoleUser}},
			},
			MaxTokens: ptr.To(int64(100)),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, multiSystemReq, false)
		require.NoError(t, err)

		require.Equal(t, firstMsg, gjson.GetBytes(body, "system.0.text").String())
		require.Equal(t, secondMsg, gjson.GetBytes(body, "system.1.text").String())
		require.Equal(t, thirdMsg, gjson.GetBytes(body, "messages.0.content.0.text").String())
	})

	t.Run("Streaming Request Validation", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		// Check that the :path header uses the streamRawPredict specifier.
		pathHeader := hm
		require.Equal(t, pathHeaderName, pathHeader[0].Key())
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:streamRawPredict", streamReq.Model)
		require.Equal(t, expectedPath, pathHeader[0].Value())

		require.True(t, gjson.GetBytes(body, "stream").Bool(), `body should contain "stream": true`)
	})

	t.Run("Test message param", func(t *testing.T) {
		openaiRequest := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			Temperature: ptr.To(0.1),
			MaxTokens:   ptr.To(int64(100)),
			TopP:        ptr.To(0.1),
			Stop: openaigo.ChatCompletionNewParamsStopUnion{
				OfStringArray: []string{"stop1", "stop2"},
			},
		}
		messageParam, err := buildAnthropicParams(openaiRequest)
		require.NoError(t, err)
		require.Equal(t, int64(100), messageParam.MaxTokens)
		require.Equal(t, "0.1", messageParam.TopP.String())
		require.Equal(t, "0.1", messageParam.Temperature.String())
		require.Equal(t, []string{"stop1", "stop2"}, messageParam.StopSequences)
	})

	t.Run("Test single stop", func(t *testing.T) {
		openaiRequest := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			Temperature: ptr.To(0.1),
			MaxTokens:   ptr.To(int64(100)),
			TopP:        ptr.To(0.1),
			Stop: openaigo.ChatCompletionNewParamsStopUnion{
				OfString: openaigo.Opt[string]("stop1"),
			},
		}
		messageParam, err := buildAnthropicParams(openaiRequest)
		require.NoError(t, err)
		require.Equal(t, int64(100), messageParam.MaxTokens)
		require.Equal(t, "0.1", messageParam.TopP.String())
		require.Equal(t, "0.1", messageParam.Temperature.String())
		require.Equal(t, []string{"stop1"}, messageParam.StopSequences)
	})

	t.Run("Invalid Temperature (above bound)", func(t *testing.T) {
		invalidTempReq := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.5),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf(tempNotSupportedError, *invalidTempReq.Temperature))
	})

	t.Run("Invalid Temperature (below bound)", func(t *testing.T) {
		invalidTempReq := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(-2.5),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf(tempNotSupportedError, *invalidTempReq.Temperature))
	})

	// Test for missing required parameter.
	t.Run("Missing MaxTokens Throws Error", func(t *testing.T) {
		missingTokensReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: nil,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, missingTokensReq, false)
		require.ErrorContains(t, err, "the maximum number of tokens must be set for Anthropic, got nil instead")
	})
	t.Run("API Version Override", func(t *testing.T) {
		customAPIVersion := "bedrock-2023-05-31"
		// Instantiate the translator with the custom API version.
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator(customAPIVersion, "")

		// Call RequestBody with a standard request.
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		// Check that the anthropic_version in the body uses the custom version.

		require.Equal(t, customAPIVersion, gjson.GetBytes(body, "anthropic_version").String())
	})
	t.Run("Request with Thinking enabled", func(t *testing.T) {
		thinkingReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
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
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		require.NotNil(t, body)

		thinkingBlock := gjson.GetBytes(body, "thinking")
		require.True(t, thinkingBlock.Exists(), "The 'thinking' field should exist in the request body")
		require.True(t, thinkingBlock.IsObject(), "The 'thinking' field should be a JSON object")
		require.Equal(t, "enabled", thinkingBlock.Map()["type"].String())
	})
	t.Run("Request with Thinking disabled", func(t *testing.T) {
		thinkingReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Thinking: &openai.ThinkingUnion{
				OfDisabled: &openai.ThinkingDisabled{
					Type: "disabled",
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		require.NotNil(t, body)

		thinkingBlock := gjson.GetBytes(body, "thinking")
		require.True(t, thinkingBlock.Exists(), "The 'thinking' field should exist in the request body")
		require.True(t, thinkingBlock.IsObject(), "The 'thinking' field should be a JSON object")
		require.Equal(t, "disabled", thinkingBlock.Map()["type"].String())
	})
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid json body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
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
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 5},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     15,
					CompletionTokens: 20,
					TotalTokens:      35,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens: 5,
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
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15, CacheReadInputTokens: 10},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens: 35, CompletionTokens: 15, TotalTokens: 50,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens: 10,
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
			name: "response with model field set",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ123",
				Model:      "claude-3-5-sonnet-20241022",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "Model field test response."}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 8, OutputTokens: 12, CacheReadInputTokens: 2},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     10,
					CompletionTokens: 12,
					TotalTokens:      22,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens: 2,
					},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						Message:      openai.ChatCompletionResponseChoiceMessage{Role: "assistant", Content: ptr.To("Model field test response.")},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
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
			require.NoError(t, err, "Test setup failed: could not marshal input struct")

			translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
			hm, body, usedToken, _, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true, nil)

			require.NoError(t, err, "Translator returned an unexpected internal error")
			require.NotNil(t, hm)
			require.NotNil(t, body)

			newBody := body
			require.NotNil(t, newBody)
			require.Len(t, hm, 1)
			require.Equal(t, contentLengthHeaderName, hm[0].Key())
			require.Equal(t, strconv.Itoa(len(newBody)), hm[0].Value())

			var gotResp openai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := tokenUsageFrom(
				int32(tt.expectedOpenAIResponse.Usage.PromptTokens),                            // nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CachedTokens),        // nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CacheCreationTokens), // nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.CompletionTokens),                        // nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.TotalTokens),                             // nolint:gosec
			)
			require.Equal(t, expectedTokenUsage, usedToken)

			if diff := cmp.Diff(tt.expectedOpenAIResponse, gotResp, cmpopts.IgnoreFields(openai.ChatCompletionResponse{}, "Created")); diff != "" {
				t.Errorf("ResponseBody mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMessageTranslation adds specific coverage for assistant and tool message translations.
func TestMessageTranslation(t *testing.T) {
	tests := []struct {
		name                  string
		inputMessages         []openai.ChatCompletionMessageParamUnion
		expectedAnthropicMsgs []anthropic.MessageParam
		expectedSystemBlocks  []anthropic.TextBlockParam
		expectErr             bool
	}{
		{
			name: "assistant message with text",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{Value: "Hello from the assistant."},
						Role:    openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("Hello from the assistant.")},
				},
			},
		},
		{
			name: "assistant message with tool call",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       ptr.To(testTool),
								Type:     openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":"NYC"}`},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolUse: &anthropic.ToolUseBlockParam{
								ID:    testTool,
								Type:  "tool_use",
								Name:  "get_weather",
								Input: map[string]any{"location": "NYC"},
							},
						},
					},
				},
			},
		},
		{
			name: "assistant message with refusal",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: openai.ChatCompletionAssistantMessageParamContent{
								Type:    openai.ChatCompletionAssistantMessageParamContentTypeRefusal,
								Refusal: ptr.To("I cannot answer that."),
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("I cannot answer that.")},
				},
			},
		},
		{
			name: "tool message with text content",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: testTool,
						Content: openai.ContentUnion{
							Value: "The weather is 72 degrees and sunny.",
						},
						Role: openai.ChatMessageRoleTool,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: testTool,
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{
										OfText: &anthropic.TextBlockParam{
											Text: "The weather is 72 degrees and sunny.",
											Type: "text",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "system and developer messages",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{OfSystem: &openai.ChatCompletionSystemMessageParam{Content: openai.ContentUnion{Value: "System prompt."}, Role: openai.ChatMessageRoleSystem}},
				{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "User message."}, Role: openai.ChatMessageRoleUser}},
				{OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{Content: openai.ContentUnion{Value: "Developer prompt."}, Role: openai.ChatMessageRoleDeveloper}},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("User message.")},
				},
			},
			expectedSystemBlocks: []anthropic.TextBlockParam{
				{Text: "System prompt."},
				{Text: "Developer prompt."},
			},
		},
		{
			name: "user message with content error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: 0,
						},
						Role: openai.ChatMessageRoleUser,
					},
				},
			},
			expectErr: true,
		},
		{
			name: "assistant message with tool call error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       ptr.To(testTool),
								Type:     openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":`},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectErr: true,
		},
		{
			name: "tool message with content error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: testTool,
						Content:    openai.ContentUnion{Value: 123},
						Role:       openai.ChatMessageRoleTool,
					},
				},
			},
			expectErr: true,
		},
		{
			name: "tool message with text parts array",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_def",
						Content: openai.ContentUnion{
							Value: []openai.ChatCompletionContentPartTextParam{
								{
									Type: "text",
									Text: "Tool result with image: [image data]",
								},
							},
						},
						Role: openai.ChatMessageRoleTool,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: "tool_def",
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{
										OfText: &anthropic.TextBlockParam{
											Text: "Tool result with image: [image data]",
											Type: "text",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple tool messages aggregated correctly",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_1",
						Content:    openai.ContentUnion{Value: `{"temp": "72F"}`},
						Role:       openai.ChatMessageRoleTool,
					},
				},
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_2",
						Content:    openai.ContentUnion{Value: `{"time": "16:00"}`},
						Role:       openai.ChatMessageRoleTool,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: "tool_1",
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{OfText: &anthropic.TextBlockParam{Text: `{"temp": "72F"}`, Type: "text"}},
								},
								IsError: anthropic.Bool(false),
							},
						},
						{
							OfToolResult: &anthropic.ToolResultBlockParam{
								ToolUseID: "tool_2",
								Type:      "tool_result",
								Content: []anthropic.ToolResultBlockParamContentUnion{
									{OfText: &anthropic.TextBlockParam{Text: `{"time": "16:00"}`, Type: "text"}},
								},
								IsError: anthropic.Bool(false),
							},
						},
					},
				},
			},
		},
		{
			name: "assistant message with thinking content",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type:      openai.ChatCompletionAssistantMessageParamContentTypeThinking,
									Text:      ptr.To("Let me think about this step by step..."),
									Signature: ptr.To("signature-123"),
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewThinkingBlock("signature-123", "Let me think about this step by step..."),
					},
				},
			},
		},
		{
			name: "assistant message with thinking content missing signature",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type: openai.ChatCompletionAssistantMessageParamContentTypeThinking,
									Text: ptr.To("Let me think about this step by step..."),
									// Missing signature - should not create thinking block
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{},
				},
			},
		},
		{
			name: "assistant message with thinking content missing text",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type:      openai.ChatCompletionAssistantMessageParamContentTypeThinking,
									Signature: ptr.To("signature-123"),
									// Missing text - should not create thinking block
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{},
				},
			},
		},
		{
			name: "assistant message with redacted thinking content (string)",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type:            openai.ChatCompletionAssistantMessageParamContentTypeRedactedThinking,
									RedactedContent: &openai.RedactedContentUnion{Value: "redacted content as string"},
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectedAnthropicMsgs: []anthropic.MessageParam{
				{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{
						anthropic.NewRedactedThinkingBlock("redacted content as string"),
					},
				},
			},
		},
		{
			name: "assistant message with redacted thinking content ([]byte) - should fail",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type:            openai.ChatCompletionAssistantMessageParamContentTypeRedactedThinking,
									RedactedContent: &openai.RedactedContentUnion{Value: []byte("redacted content as bytes")},
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectErr: true,
		},
		{
			name: "assistant message with redacted thinking content (unsupported type) - should fail",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{
									Type:            openai.ChatCompletionAssistantMessageParamContentTypeRedactedThinking,
									RedactedContent: &openai.RedactedContentUnion{Value: 123},
								},
							},
						},
						Role: openai.ChatMessageRoleAssistant,
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIReq := &openai.ChatCompletionRequest{Messages: tt.inputMessages}
			anthropicMsgs, systemBlocks, err := openAIToAnthropicMessages(openAIReq.Messages)

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				// Compare the conversational messages.
				require.Len(t, anthropicMsgs, len(tt.expectedAnthropicMsgs), "Number of translated messages should match")
				for i, expectedMsg := range tt.expectedAnthropicMsgs {
					actualMsg := anthropicMsgs[i]
					require.Equal(t, expectedMsg.Role, actualMsg.Role, "Message roles should match")
					require.Len(t, actualMsg.Content, len(expectedMsg.Content), "Number of content blocks should match")
					for j, expectedContent := range expectedMsg.Content {
						actualContent := actualMsg.Content[j]
						require.Equal(t, *expectedContent.GetType(), *actualContent.GetType(), "Content block types should match")
						if expectedContent.OfText != nil {
							require.NotNil(t, actualContent.OfText)
							require.Equal(t, expectedContent.OfText.Text, actualContent.OfText.Text)
						}
						if expectedContent.OfToolUse != nil {
							require.NotNil(t, actualContent.OfToolUse)
							require.Equal(t, expectedContent.OfToolUse.ID, actualContent.OfToolUse.ID)
							require.Equal(t, expectedContent.OfToolUse.Name, actualContent.OfToolUse.Name)
							require.Equal(t, expectedContent.OfToolUse.Input, actualContent.OfToolUse.Input)
						}
						if expectedContent.OfToolResult != nil {
							require.NotNil(t, actualContent.OfToolResult)
							require.Equal(t, expectedContent.OfToolResult.ToolUseID, actualContent.OfToolResult.ToolUseID)
							require.Len(t, actualContent.OfToolResult.Content, len(expectedContent.OfToolResult.Content))
							if expectedContent.OfToolResult.Content[0].OfText != nil {
								require.Equal(t, expectedContent.OfToolResult.Content[0].OfText.Text, actualContent.OfToolResult.Content[0].OfText.Text)
							}
							if expectedContent.OfToolResult.Content[0].OfImage != nil {
								require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage, "Actual image block should not be nil")
								require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage.Source, "Actual image source should not be nil")
								if expectedContent.OfToolResult.Content[0].OfImage.Source.OfBase64 != nil {
									require.NotNil(t, actualContent.OfToolResult.Content[0].OfImage.Source.OfBase64, "Actual base64 source should not be nil")
									require.Equal(t, expectedContent.OfToolResult.Content[0].OfImage.Source.OfBase64.Data, actualContent.OfToolResult.Content[0].OfImage.Source.OfBase64.Data)
								}
							}
						}
					}
				}

				// Compare the system prompt blocks.
				require.Len(t, systemBlocks, len(tt.expectedSystemBlocks), "Number of system blocks should match")
				for i, expectedBlock := range tt.expectedSystemBlocks {
					actualBlock := systemBlocks[i]
					require.Equal(t, expectedBlock.Text, actualBlock.Text, "System block text should match")
				}
			}
		})
	}
}

// TestRedactedContentUnionSerialization tests the JSON marshaling/unmarshaling of RedactedContentUnion
func TestRedactedContentUnionSerialization(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedValue any
		expectError   bool
	}{
		{
			name:          "string value",
			input:         `"plain string"`,
			expectedValue: "plain string",
		},
		{
			name:          "base64 encoded bytes",
			input:         `"aGVsbG8gd29ybGQ="`, // "hello world" in base64
			expectedValue: []byte("hello world"),
		},
		{
			name:        "invalid json",
			input:       `{invalid}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var union openai.RedactedContentUnion
			err := json.Unmarshal([]byte(tt.input), &union)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectedValue, union.Value)

			// Test marshaling back
			marshaled, err := json.Marshal(union)
			require.NoError(t, err)

			// For byte arrays, check they're base64 encoded
			if bytes, ok := tt.expectedValue.([]byte); ok {
				expected := base64.StdEncoding.EncodeToString(bytes)
				require.Equal(t, `"`+expected+`"`, string(marshaled))
			} else {
				// For strings, check round-trip
				require.Equal(t, tt.input, string(marshaled))
			}
		})
	}
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseError(t *testing.T) {
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
					Type:    gcpBackendError,
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

			o := &openAIToGCPAnthropicTranslatorV1ChatCompletion{}
			hm, body, err := o.ResponseError(tt.responseHeaders, reader)

			require.NoError(t, err)
			require.NotNil(t, body)
			require.NotNil(t, hm)
			require.Len(t, hm, 2)
			require.Equal(t, contentTypeHeaderName, hm[0].Key())
			require.Equal(t, jsonContentType, hm[0].Value()) //nolint:testifylint
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

func TestAnthropicStreamParser_ErrorHandling(t *testing.T) {
	runStreamErrTest := func(t *testing.T, sseStream string, endOfStream bool) error {
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, _, _, _, err = translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), endOfStream, nil)
		return err
	}

	tests := []struct {
		name          string
		sseStream     string
		endOfStream   bool
		expectedError string
	}{
		{
			name:          "malformed message_start event",
			sseStream:     "event: message_start\ndata: {invalid\n\n",
			expectedError: "unmarshal message_start",
		},
		{
			name:          "malformed content_block_start event",
			sseStream:     "event: content_block_start\ndata: {invalid\n\n",
			expectedError: "failed to unmarshal content_block_start",
		},
		{
			name:          "malformed content_block_delta event",
			sseStream:     "event: content_block_delta\ndata: {invalid\n\n",
			expectedError: "unmarshal content_block_delta",
		},
		{
			name:          "malformed content_block_stop event",
			sseStream:     "event: content_block_stop\ndata: {invalid\n\n",
			expectedError: "unmarshal content_block_stop",
		},
		{
			name:          "malformed error event data",
			sseStream:     "event: error\ndata: {invalid\n\n",
			expectedError: "unparsable error event",
		},
		{
			name:        "unknown stop reason",
			endOfStream: true,
			sseStream: `event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "some_future_reason"}, "usage": {"output_tokens": 0}}

event: message_stop
data: {"type": "message_stop"}
`,
			expectedError: "received invalid stop reason",
		},
		{
			name:          "malformed_final_event_block",
			sseStream:     "event: message_stop\ndata: {invalid", // No trailing \n\n.
			endOfStream:   true,
			expectedError: "unmarshal message_stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runStreamErrTest(t, tt.sseStream, tt.endOfStream)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.expectedError)
		})
	}

	t.Run("body read error", func(t *testing.T) {
		parser := newAnthropicStreamParser("test-model")
		_, _, _, _, err := parser.Process(&mockErrorReader{}, false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read from stream body")
	})
}

// TestResponseModel_GCPAnthropicStreaming tests that GCP Anthropic streaming returns the request model
// GCP Anthropic uses deterministic model mapping without virtualization
func TestResponseModel_GCPAnthropicStreaming(t *testing.T) {
	modelName := "claude-sonnet-4@20250514"
	sseStream := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY", "type": "message", "role": "assistant", "content": [], "model": "claude-sonnet-4@20250514", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 10, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence":null}, "usage": {"output_tokens": 5}}

event: message_stop
data: {"type": "message_stop"}

`
	openAIReq := &openai.ChatCompletionRequest{
		Stream:    true,
		Model:     modelName, // Use the actual model name from documentation
		MaxTokens: new(int64),
	}

	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
	_, _, err := translator.RequestBody(nil, openAIReq, false)
	require.NoError(t, err)

	// Test streaming response - GCP Anthropic doesn't return model in response, uses request model
	_, _, tokenUsage, responseModel, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel) // Returns the request model since no virtualization
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(10), inputTokens)
	outputTokens, ok := tokenUsage.OutputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(5), outputTokens)
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody_Streaming(t *testing.T) {
	t.Run("handles simple text stream", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY", "type": "message", "role": "assistant", "content": [], "model": "claude-opus-4-20250514", "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 25, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "!"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence":null}, "usage": {"output_tokens": 15}}

event: message_stop
data: {"type": "message_stop"}

`
		openAIReq := &openai.ChatCompletionRequest{
			Stream:    true,
			Model:     "test-model",
			MaxTokens: new(int64),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)

		bodyStr := string(bm)
		require.Contains(t, bodyStr, `"content":"Hello"`)
		require.Contains(t, bodyStr, `"finish_reason":"stop"`)
		require.Contains(t, bodyStr, `"prompt_tokens":25`)
		require.Contains(t, bodyStr, `"completion_tokens":15`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles text and tool use stream", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_014p7gG3wDgGV9EUtLvnow3U","type":"message","role":"assistant","model":"claude-opus-4-20250514","stop_sequence":null,"usage":{"input_tokens":472,"output_tokens":2},"content":[],"stop_reason":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Okay"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":","}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" let"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"'s"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" check"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" the"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" for"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" San"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" Francisco"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":","}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" CA"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":":"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01T1x1fJ34qAmk2tNTrN7Up6","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" \"San"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" Francisc"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"o,"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" CA\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":", "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"unit\": \"fah"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"renheit\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":89}}

event: message_stop
data: {"type":"message_stop"}
`

		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// Parse all streaming events to verify the event flow
		var chunks []openai.ChatCompletionResponseChunk
		var textChunks []string
		var toolCallStarted bool
		var hasRole bool
		var toolCallCompleted bool
		var finalFinishReason openai.ChatCompletionChoicesFinishReason
		var finalUsageChunk *openai.ChatCompletionResponseChunk
		var toolCallChunks []string // Track partial JSON chunks

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)

			// Check if this is the final usage chunk
			if strings.Contains(jsonBody, `"usage"`) {
				finalUsageChunk = &chunk
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				// Check for role in first content chunk
				if choice.Delta != nil && choice.Delta.Content != nil && *choice.Delta.Content != "" && !hasRole {
					require.NotNil(t, choice.Delta.Role, "Role should be present on first content chunk")
					require.Equal(t, openai.ChatMessageRoleAssistant, choice.Delta.Role)
					hasRole = true
				}

				// Collect text content
				if choice.Delta != nil && choice.Delta.Content != nil {
					textChunks = append(textChunks, *choice.Delta.Content)
				}

				// Check tool calls - start and accumulate partial JSON
				if choice.Delta != nil && len(choice.Delta.ToolCalls) > 0 {
					toolCall := choice.Delta.ToolCalls[0]

					// Check tool call initiation
					if toolCall.Function.Name == "get_weather" && !toolCallStarted {
						require.Equal(t, "get_weather", toolCall.Function.Name)
						require.NotNil(t, toolCall.ID)
						require.Equal(t, "toolu_01T1x1fJ34qAmk2tNTrN7Up6", *toolCall.ID)
						require.Equal(t, int64(0), toolCall.Index, "Tool call should be at index 1 (after text content at index 0)")
						toolCallStarted = true
					}

					// Accumulate partial JSON arguments - these should also be at index 1
					if toolCall.Function.Arguments != "" {
						toolCallChunks = append(toolCallChunks, toolCall.Function.Arguments)

						// Verify the index remains consistent at 1 for all tool call chunks
						require.Equal(t, int64(0), toolCall.Index, "Tool call argument chunks should be at index 1")
					}
				}

				// Track finish reason
				if choice.FinishReason != "" {
					finalFinishReason = choice.FinishReason
					if finalFinishReason == "tool_calls" {
						toolCallCompleted = true
					}
				}
			}
		}

		// Check the final usage chunk for accumulated tool call arguments
		if finalUsageChunk != nil {
			require.Equal(t, 472, finalUsageChunk.Usage.PromptTokens)
			require.Equal(t, 89, finalUsageChunk.Usage.CompletionTokens)
		}

		// Verify partial JSON accumulation in streaming chunks
		if len(toolCallChunks) > 0 {
			// Verify we got multiple partial JSON chunks during streaming
			require.GreaterOrEqual(t, len(toolCallChunks), 2, "Should receive multiple partial JSON chunks for tool arguments")

			// Verify some expected partial content appears in the chunks
			fullPartialJSON := strings.Join(toolCallChunks, "")
			require.Contains(t, fullPartialJSON, `"location":`, "Partial JSON should contain location field")
			require.Contains(t, fullPartialJSON, `"unit":`, "Partial JSON should contain unit field")
			require.Contains(t, fullPartialJSON, "San Francisco", "Partial JSON should contain location value")
			require.Contains(t, fullPartialJSON, "fahrenheit", "Partial JSON should contain unit value")
		}

		// Verify streaming event assertions
		require.GreaterOrEqual(t, len(chunks), 5, "Should have multiple streaming chunks")
		require.True(t, hasRole, "Should have role in first content chunk")
		require.True(t, toolCallStarted, "Tool call should have been initiated")
		require.True(t, toolCallCompleted, "Tool call should have complete arguments in final chunk")
		require.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, finalFinishReason, "Final finish reason should be tool_calls")

		// Verify text content was streamed correctly
		fullText := strings.Join(textChunks, "")
		require.Contains(t, fullText, "Okay, let's check the weather for San Francisco, CA:")
		require.GreaterOrEqual(t, len(textChunks), 3, "Text should be streamed in multiple chunks")

		// Original aggregate response assertions
		require.Contains(t, bodyStr, `"content":"Okay"`)
		require.Contains(t, bodyStr, `"name":"get_weather"`)
		require.Contains(t, bodyStr, "\"arguments\":\"{\\\"location\\\":")
		require.NotContains(t, bodyStr, "\"arguments\":\"{}\"")
		require.Contains(t, bodyStr, "renheit\\\"}\"")
		require.Contains(t, bodyStr, `"finish_reason":"tool_calls"`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles streaming with web search tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01G...","type":"message","role":"assistant","usage":{"input_tokens":2679,"output_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I'll check"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" the current weather in New York City for you"}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_014hJH82Qum7Td6UV8gDXThB","name":"web_search","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"weather NYC today\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_014hJH82Qum7Td6UV8gDXThB","content":[{"type":"web_search_result","title":"Weather in New York City in May 2025 (New York)","url":"https://world-weather.info/forecast/usa/new_york/may-2025/","page_age":null}]}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"Here's the current weather information for New York"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":" City."}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":510}}

event: message_stop
data: {"type":"message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		require.Contains(t, bodyStr, `"content":"I'll check"`)
		require.Contains(t, bodyStr, `"content":" the current weather in New York City for you"`)
		require.Contains(t, bodyStr, `"name":"web_search"`)
		require.Contains(t, bodyStr, "\"arguments\":\"{\\\"query\\\":\\\"weather NYC today\\\"}\"")
		require.NotContains(t, bodyStr, "\"arguments\":\"{}\"")
		require.Contains(t, bodyStr, `"content":"Here's the current weather information for New York"`)
		require.Contains(t, bodyStr, `"finish_reason":"stop"`)
		require.Contains(t, bodyStr, string(sseDoneMessage))
	})

	t.Run("handles unterminated tool call at end of stream", func(t *testing.T) {
		// This stream starts a tool call but ends without a content_block_stop or message_stop.
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_abc","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\": \"SF\"}"}}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var finalToolCallChunk openai.ChatCompletionResponseChunk

		// Split the response into individual SSE messages and find the final data chunk.
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.HasPrefix(line, "data: [DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")
			// The final chunk with the accumulated tool call is the only one with a "usage" field.
			if strings.Contains(jsonBody, `"usage"`) {
				err := json.Unmarshal([]byte(jsonBody), &finalToolCallChunk)
				require.NoError(t, err, "Failed to unmarshal final tool call chunk")
				break
			}
		}

		require.NotEmpty(t, finalToolCallChunk.Choices, "Final chunk should have choices")
		require.NotNil(t, finalToolCallChunk.Choices[0].Delta.ToolCalls, "Final chunk should have tool calls")

		finalToolCall := finalToolCallChunk.Choices[0].Delta.ToolCalls[0]
		require.Equal(t, "tool_abc", *finalToolCall.ID)
		require.Equal(t, "get_weather", finalToolCall.Function.Name)
		require.JSONEq(t, `{"location": "SF"}`, finalToolCall.Function.Arguments)
	})
	t.Run("handles  thinking and tool use stream", func(t *testing.T) {
		sseStream := `
event: message_start
data: {"type": "message_start", "message": {"id": "msg_123", "type": "message", "role": "assistant", "usage": {"input_tokens": 50, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "name": "web_searcher"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "text": "Searching for information..."}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: content_block_start
data: {"type": "content_block_start", "index": 1, "content_block": {"type": "tool_use", "id": "toolu_abc123", "name": "get_weather", "input": {"location": "San Francisco, CA"}}}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "tool_use"}, "usage": {"output_tokens": 35}}

event: message_stop
data: {"type": "message_stop"}
`
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, _, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), true, nil)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var contentDeltas []string
		var foundToolCallWithArgs bool
		var finalFinishReason openai.ChatCompletionChoicesFinishReason

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)

			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				if choice.Delta.Content != nil {
					contentDeltas = append(contentDeltas, *choice.Delta.Content)
				}
				if len(choice.Delta.ToolCalls) > 0 {
					toolCall := choice.Delta.ToolCalls[0]
					// Check if this is the tool chunk that contains the arguments.
					if toolCall.Function.Arguments != "" {
						expectedArgs := `{"location":"San Francisco, CA"}`
						assert.JSONEq(t, expectedArgs, toolCall.Function.Arguments, "Tool call arguments do not match")
						assert.Equal(t, "get_weather", toolCall.Function.Name)
						assert.Equal(t, "toolu_abc123", *toolCall.ID)
						foundToolCallWithArgs = true
					} else {
						// This should be the initial tool call chunk with empty arguments since input is provided upfront
						assert.Equal(t, "get_weather", toolCall.Function.Name)
						assert.Equal(t, "toolu_abc123", *toolCall.ID)
					}
				}
			}
			if choice.FinishReason != "" {
				finalFinishReason = choice.FinishReason
			}
		}

		fullContent := strings.Join(contentDeltas, "")
		assert.Contains(t, fullContent, "Searching for information...")
		require.True(t, foundToolCallWithArgs, "Did not find a tool call chunk with arguments to assert against")
		assert.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, finalFinishReason, "Final finish reason should be 'tool_calls'")
	})
}

func TestAnthropicStreamParser_EventTypes(t *testing.T) {
	runStreamTest := func(t *testing.T, sseStream string, endOfStream bool) ([]byte, metrics.TokenUsage, error) {
		openAIReq := &openai.ChatCompletionRequest{Stream: true, Model: "test-model", MaxTokens: new(int64)}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "").(*openAIToGCPAnthropicTranslatorV1ChatCompletion)
		_, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		_, bm, tokenUsage, _, err := translator.ResponseBody(map[string]string{}, strings.NewReader(sseStream), endOfStream, nil)
		return bm, tokenUsage, err
	}

	t.Run("handles message_start event", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_123", "usage": {"input_tokens": 15}}}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		assert.Empty(t, string(bm), "message_start should produce an empty chunk")
	})

	t.Run("handles content_block events for tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "id": "tool_abc", "name": "get_weather", "input":{}}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "input_json_delta", "partial_json": "{\"location\": \"SF\"}"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// 1. Split the stream into individual data chunks
		//    and remove the "data: " prefix.
		var chunks []openai.ChatCompletionResponseChunk
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err = json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)
		}

		// 2. Inspect the Go structs directly.
		require.Len(t, chunks, 2, "Expected two data chunks for this tool call stream")

		// Check the first chunk (the tool call initiation).
		firstChunk := chunks[0]
		require.NotNil(t, firstChunk.Choices[0].Delta.ToolCalls)
		require.Equal(t, "tool_abc", *firstChunk.Choices[0].Delta.ToolCalls[0].ID)
		require.Equal(t, "get_weather", firstChunk.Choices[0].Delta.ToolCalls[0].Function.Name)
		// With empty input, arguments should be empty string, not "{}"
		require.Empty(t, firstChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)

		// Check the second chunk (the arguments delta).
		secondChunk := chunks[1]
		require.NotNil(t, secondChunk.Choices[0].Delta.ToolCalls)
		argumentsJSON := secondChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments

		// 3. Unmarshal the arguments string to verify its contents.
		var args map[string]string
		err = json.Unmarshal([]byte(argumentsJSON), &args)
		require.NoError(t, err)
		require.Equal(t, "SF", args["location"])
	})

	t.Run("handles ping event", func(t *testing.T) {
		sseStream := `event: ping
data: {"type": "ping"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "ping should produce an empty chunk")
	})

	t.Run("handles error event", func(t *testing.T) {
		sseStream := `event: error
data: {"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}

`
		_, _, err := runStreamTest(t, sseStream, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "anthropic stream error: overloaded_error - Overloaded")
	})

	t.Run("gracefully handles unknown event types", func(t *testing.T) {
		sseStream := `event: future_event_type
data: {"some_new_data": "value"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "unknown events should be ignored and produce an empty chunk")
	})

	t.Run("handles message_stop event", func(t *testing.T) {
		sseStream := `event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "max_tokens"}, "usage": {"output_tokens": 1}}

event: message_stop
data: {"type": "message_stop"}

`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		require.Contains(t, string(bm), `"finish_reason":"length"`)
	})

	t.Run("handles chunked input_json_delta for tool use", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "id": "tool_123", "name": "get_weather"}}

event: content_block_delta
data: {"type": "content_block_delta","index": 0,"delta": {"type": "input_json_delta","partial_json": "{\"location\": \"San Fra"}}

event: content_block_delta
data: {"type": "content_block_delta","index": 0,"delta": {"type": "input_json_delta","partial_json": "ncisco\"}"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}
`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// 1. Unmarshal all the chunks from the stream response.
		var chunks []openai.ChatCompletionResponseChunk
		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonBody := strings.TrimPrefix(line, "data: ")

			var chunk openai.ChatCompletionResponseChunk
			err := json.Unmarshal([]byte(jsonBody), &chunk)
			require.NoError(t, err, "Failed to unmarshal chunk: %s", jsonBody)
			chunks = append(chunks, chunk)
		}

		// 2. We expect 3 chunks: start, delta part 1, delta part 2.
		require.Len(t, chunks, 3, "Expected three data chunks for this stream")

		// 3. Verify the contents of each relevant chunk.

		// Chunk 1: Tool call start.
		chunk1ToolCalls := chunks[0].Choices[0].Delta.ToolCalls
		require.NotNil(t, chunk1ToolCalls)
		require.Equal(t, "get_weather", chunk1ToolCalls[0].Function.Name)

		// Chunk 2: First part of the arguments.
		chunk2Args := chunks[1].Choices[0].Delta.ToolCalls[0].Function.Arguments
		require.Equal(t, `{"location": "San Fra`, chunk2Args) //nolint:testifylint

		// Chunk 3: Second part of the arguments.
		chunk3Args := chunks[2].Choices[0].Delta.ToolCalls[0].Function.Arguments
		require.Equal(t, `ncisco"}`, chunk3Args)
	})
	t.Run("sends role on first chunk", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
`
		// Set endOfStream to true to ensure all events in the buffer are processed.
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		var contentChunk openai.ChatCompletionResponseChunk
		foundChunk := false

		lines := strings.SplitSeq(strings.TrimSpace(bodyStr), "\n\n")
		for line := range lines {
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				jsonBody := after
				// We only care about the chunk that has the text content.
				if strings.Contains(jsonBody, `"content"`) {
					err := json.Unmarshal([]byte(jsonBody), &contentChunk)
					require.NoError(t, err, "Failed to unmarshal content chunk")
					foundChunk = true
					break
				}
			}
		}

		require.True(t, foundChunk, "Did not find a data chunk with content in the output")

		require.NotNil(t, contentChunk.Choices[0].Delta.Role, "Role should be present on the first chunk")
		require.Equal(t, openai.ChatMessageRoleAssistant, contentChunk.Choices[0].Delta.Role)
	})

	t.Run("accumulates output tokens", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":20}}}

event: message_delta
data: {"type":"message_delta","delta":{},"usage":{"output_tokens":10}}

event: message_delta
data: {"type":"message_delta","delta":{},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}
`
		// Run with endOfStream:true to get the final usage chunk.
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		// The final usage chunk should sum the tokens from all message_delta events.
		require.Contains(t, bodyStr, `"completion_tokens":15`)
		require.Contains(t, bodyStr, `"prompt_tokens":20`)
		require.Contains(t, bodyStr, `"total_tokens":35`)
	})

	t.Run("ignores SSE comments", func(t *testing.T) {
		sseStream := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10}}}

: this is a comment and should be ignored

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
`
		bm, _, err := runStreamTest(t, sseStream, true)
		require.NoError(t, err)
		require.NotNil(t, bm)
		bodyStr := string(bm)

		require.Contains(t, bodyStr, `"content":"Hello"`)
		require.NotContains(t, bodyStr, "this is a comment")
	})
	t.Run("handles data-only event as a message event", func(t *testing.T) {
		sseStream := `data: some text

data: another message with two lines
`
		bm, _, err := runStreamTest(t, sseStream, false)
		require.NoError(t, err)
		require.Empty(t, bm, "data-only events should be treated as no-op 'message' events and produce an empty chunk")
	})
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_Cache(t *testing.T) {
	t.Run("full request with mixed caching", func(t *testing.T) {
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				// System message with cache enabled.
				{OfSystem: &openai.ChatCompletionSystemMessageParam{
					Role: openai.ChatMessageRoleSystem,
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "You are a helpful assistant.",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					}},
				}},
				// User message with cache enabled.
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: []openai.ChatCompletionContentPartUserUnionParam{
						{OfText: &openai.ChatCompletionContentPartTextParam{
							Type: "text",
							Text: "How's the weather?",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						}},
					}},
				}},
				{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role:    openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{Value: "I'll check the weather for you."},
					ToolCalls: []openai.ChatCompletionMessageToolCallParam{
						{
							ID: ptr.To("call_789"),
							Function: openai.ChatCompletionMessageToolCallFunctionParam{
								Name:      "get_weather",
								Arguments: `{"location": "New York"}`,
							},
							Type: openai.ChatCompletionMessageToolCallTypeFunction,
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					},
				}},
				// Tool message with cache enabled.
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_789",
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "It's sunny and 75°F in New York.",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					}},
				}},
				// User message with cache disabled.
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "Thanks! What about tomorrow?"},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check system message (cache enabled).
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("system.0.cache_control.type").String())

		// Check user message (cache enabled).
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.0.cache_control.type").String())

		// Check assistant message (text part is not cached, tool_use part IS cached)
		require.False(t, result.Get("messages.1.content.0.cache_control").Exists(), "text part of assistant message should not be cached")
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.1.content.1.cache_control.type").String(), "tool_use block should be cached")

		// Check tool message (aggregated into a user message, cache enabled)
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.2.content.0.cache_control.type").String())

		// Check second user message (cache disabled)
		require.False(t, result.Get("messages.3.content.0.cache_control").Exists())
	})

	t.Run("cache with different structures", func(t *testing.T) {
		type testCase struct {
			name        string
			content     any
			expectCache bool
		}

		testCases := []testCase{
			{
				name: "multi-part text cache enabled",
				content: []openai.ChatCompletionContentPartUserUnionParam{
					{OfText: &openai.ChatCompletionContentPartTextParam{
						Type: "text", Text: "This is a content part",
						AnthropicContentFields: &openai.AnthropicContentFields{CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()}},
					}},
				},
				expectCache: true,
			},
			{
				name: "multi-part text cache disabled (empty type)",
				content: []openai.ChatCompletionContentPartUserUnionParam{
					{OfText: &openai.ChatCompletionContentPartTextParam{
						Type: "text", Text: "This is a content part",
						AnthropicContentFields: &openai.AnthropicContentFields{CacheControl: anthropic.CacheControlEphemeralParam{Type: ""}},
					}},
				},
				expectCache: false,
			},
			{
				name: "multi-part text cache disabled (anthropic fields empty)",
				content: []openai.ChatCompletionContentPartUserUnionParam{
					{OfText: &openai.ChatCompletionContentPartTextParam{
						Type: "text", Text: "This is a content part",
						AnthropicContentFields: &openai.AnthropicContentFields{},
					}},
				},
				expectCache: false,
			},
			{
				name: "multi-part text cache disabled (missing anthropic fields)",
				content: []openai.ChatCompletionContentPartUserUnionParam{
					{OfText: &openai.ChatCompletionContentPartTextParam{
						Type: "text", Text: "This is a content part",
					}},
				},
				expectCache: false,
			},
			{
				name: "multi-part text cache missing",
				content: []openai.ChatCompletionContentPartUserUnionParam{
					{OfText: &openai.ChatCompletionContentPartTextParam{Type: "text", Text: "This is a content part"}},
				},
				expectCache: false,
			},
			{
				name:        "simple string content (caching not possible)",
				content:     "This is a test message",
				expectCache: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				req := &openai.ChatCompletionRequest{
					Model: "claude-3-haiku",
					Messages: []openai.ChatCompletionMessageParamUnion{
						{OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: tc.content},
						}},
					},
					MaxTokens: ptr.To(int64(10)),
				}

				translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
				_, body, err := translator.RequestBody(nil, req, false)
				require.NoError(t, err)

				result := gjson.ParseBytes(body)
				cacheControl := result.Get("messages.0.content.0.cache_control")

				if tc.expectCache {
					require.True(t, cacheControl.Exists())
					require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), cacheControl.Get("type").String())
				} else {
					require.False(t, cacheControl.Exists())
				}
			})
		}
	})
	t.Run("cache with image content", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "claude-3-opus",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							{OfText: &openai.ChatCompletionContentPartTextParam{
								Text: "What's in this image?", Type: "text",
								AnthropicContentFields: &openai.AnthropicContentFields{
									CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
								},
							}},
							{OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: "image_url",
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:image/jpeg;base64,dGVzdA==",
								},
								AnthropicContentFields: &openai.AnthropicContentFields{
									CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
								},
							}},
						},
					},
				}},
			},
			MaxTokens: ptr.To(int64(50)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check that both the text part and the image part have cache_control.
		require.True(t, result.Get("messages.0.content.0.cache_control").Exists(), "cache should exist for text part")
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.0.cache_control.type").String())

		require.True(t, result.Get("messages.0.content.1.cache_control").Exists(), "cache should exist for image part")
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.1.cache_control.type").String())
	})
	t.Run("cache with mixed multi-modal content", func(t *testing.T) {
		// This test ensures that in a multi-part (text/image) message, one part
		// can be cached while the other is not.
		req := &openai.ChatCompletionRequest{
			Model: "claude-3-opus",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role: openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{
						Value: []openai.ChatCompletionContentPartUserUnionParam{
							// Text part: Caching NOT enabled
							{OfText: &openai.ChatCompletionContentPartTextParam{
								Text: "What's in this image?", Type: "text",
							}},
							// Image part: Caching IS enabled
							{OfImageURL: &openai.ChatCompletionContentPartImageParam{
								Type: "image_url",
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:image/jpeg;base64,dGVzdA==",
								},
								AnthropicContentFields: &openai.AnthropicContentFields{
									CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
								},
							}},
						},
					},
				}},
			},
			MaxTokens: ptr.To(int64(50)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check text part (index 0) - should NOT be cached.
		require.False(t, result.Get("messages.0.content.0.cache_control").Exists(), "text part should not be cached")

		// Check image part (index 1) - SHOULD be cached.
		require.True(t, result.Get("messages.0.content.1.cache_control").Exists(), "image part should be cached")
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.1.cache_control.type").String())
	})
	t.Run("developer message caching", func(t *testing.T) {
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				// Developer message with cache enabled.
				{OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Role: openai.ChatMessageRoleDeveloper,
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "You are an expert Go programmer.",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					}},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check that the developer message, which becomes part of the 'system' prompt, is cached.
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("system.0.cache_control.type").String())
	})
	t.Run("tool definition caching", func(t *testing.T) {
		// This test verifies that a cache_control field on a
		// FunctionDefinition (in the 'tools' array) is correctly translated.
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Tools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name: "get_weather",
						Parameters: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"location": map[string]any{"type": "string"},
							},
						},
						AnthropicContentFields: &openai.AnthropicContentFields{
							CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
						},
					},
				},
			},
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{
					Role:    openai.ChatMessageRoleUser,
					Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather in New York?"},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check that the tool definition in the 'tools' array is cached.
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("tools.0.cache_control.type").String(), "tool definition should be cached")
		require.Equal(t, "get_weather", result.Get("tools.0.name").String())
	})
	t.Run("aggregated tool messages with mixed caching", func(t *testing.T) {
		// This test ensures that caching is applied on a per-tool-message basis,
		// even when they are aggregated into a single user message.
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				// First tool message, cache disabled.
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					Content:    openai.ContentUnion{Value: "Result for tool 1"},
					ToolCallID: "call_001",
				}},
				// Second tool message, cache not  constant.ValueOf[constant.Ephemeral]() (i.e., disabled).
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					Content:    openai.ContentUnion{Value: "Result for tool 2"},
					ToolCallID: "call_002",
				}},
				// Third tool message, cache enabled.
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_003",
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "Result for tool 3",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					}},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// The translator creates a single user message with three tool_result blocks.
		// The first & second block should NOT have cache_control.
		require.False(t, result.Get("messages.0.content.0.cache_control").Exists(), "first tool_result should not be cached")
		require.False(t, result.Get("messages.0.content.1.cache_control").Exists(), "second tool_result should not be cached")

		// The third block SHOULD have cache_control.
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.2.cache_control.type").String(), "third tool_result should be cached")
	})
	t.Run("assistant tool_call caching", func(t *testing.T) {
		// This test verifies that a cache_control field on a
		// ToolCall (in an assistant message) is correctly translated
		// to the corresponding tool_use block.
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role:    openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{Value: "OK, I'll use the tool."},
					ToolCalls: []openai.ChatCompletionMessageToolCallParam{
						{
							ID:   ptr.To("call_789"),
							Type: openai.ChatCompletionMessageToolCallTypeFunction,
							Function: openai.ChatCompletionMessageToolCallFunctionParam{
								Name:      "get_weather",
								Arguments: `{"location": "New York"}`,
							},
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// The assistant message has two content parts: text and tool_use.
		// The text part should not be cached.
		require.False(t, result.Get("messages.0.content.0.cache_control").Exists(), "text part of assistant message should not be cached")

		// The tool_use part (index 1) should be cached.
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.1.cache_control.type").String(), "tool_use block should be cached")
		require.Equal(t, "tool_use", result.Get("messages.0.content.1.type").String())
		require.Equal(t, "call_789", result.Get("messages.0.content.1.id").String())
	})
	t.Run("assistant text content caching", func(t *testing.T) {
		// This test verifies that a cache_control field on an
		// assistant's text content part is correctly translated.
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Role: openai.ChatMessageRoleAssistant,
					Content: openai.StringOrAssistantRoleContentUnion{
						Value: openai.ChatCompletionAssistantMessageParamContent{
							Type: openai.ChatCompletionAssistantMessageParamContentTypeText,
							Text: ptr.To("This is a cached assistant text response."),
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		// Check the assistant message's text content (index 0).
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.0.cache_control.type").String(), "assistant text block should be cached")
		require.Equal(t, "text", result.Get("messages.0.content.0.type").String())
		require.Equal(t, "This is a cached assistant text response.", result.Get("messages.0.content.0.text").String())
	})
	t.Run("aggregated tool messages with granular caching", func(t *testing.T) {
		// This test validates the logic in the 'case msg.OfTool != nil:' block.
		// It checks that caching is applied on a per-tool-message basis,
		// and that it correctly reads the cache flag from within the content parts.
		openAIReq := &openai.ChatCompletionRequest{
			Model: "gcp.claude-3.5-haiku",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					Content:    openai.ContentUnion{Value: "Result for tool 1"},
					ToolCallID: "call_001",
				}},
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_002",
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "Result for tool 2 (no cache)",
						},
					}},
				}},
				{OfTool: &openai.ChatCompletionToolMessageParam{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: "call_003",
					Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: "text",
							Text: "Part 1 of result 3 (not cached)",
						},
						{
							Type: "text",
							Text: "Part 2 of result 3 (cached)",
							AnthropicContentFields: &openai.AnthropicContentFields{
								CacheControl: anthropic.CacheControlEphemeralParam{Type: constant.ValueOf[constant.Ephemeral]()},
							},
						},
					}},
				}},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		result := gjson.ParseBytes(body)

		require.Equal(t, "call_001", result.Get("messages.0.content.0.tool_use_id").String())
		require.False(t, result.Get("messages.0.content.0.cache_control").Exists(), "tool 1 (string) should not be cached")

		require.Equal(t, "call_002", result.Get("messages.0.content.1.tool_use_id").String())
		require.False(t, result.Get("messages.0.content.1.cache_control").Exists(), "tool 2 (no cache) should not be cached")

		require.Equal(t, "call_003", result.Get("messages.0.content.2.tool_use_id").String())
		require.Equal(t, string(constant.ValueOf[constant.Ephemeral]()), result.Get("messages.0.content.2.cache_control.type").String(), "tool 3 (with cache) should be cached")
	})
}

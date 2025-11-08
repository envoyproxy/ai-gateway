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
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	"github.com/google/go-cmp/cmp"
	openaigo "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	claudeTestModel = "claude-3-opus-20240229"
	testTool        = "test_123"
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
	require.Equal(t, uint32(10), tokenUsage.InputTokens)
	require.Equal(t, uint32(5), tokenUsage.OutputTokens)
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
		hm, bm, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, bm)

		// Check the path header.
		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", openAIReq.Model)
		require.Equal(t, expectedPath, pathHeader.Value())

		// Check the body content.
		body := bm
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
		_, bm, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)
		body := bm
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
		_, bm, err := translator.RequestBody(nil, multiSystemReq, false)
		require.NoError(t, err)
		body := bm
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
		hm, bm, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		// Check that the :path header uses the streamRawPredict specifier.
		pathHeader := hm
		require.Equal(t, pathHeaderName, pathHeader[0].Key())
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:streamRawPredict", streamReq.Model)
		require.Equal(t, expectedPath, pathHeader[0].Value())

		body := bm
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
		_, bm, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, bm)

		// Check that the anthropic_version in the body uses the custom version.
		body := bm
		require.Equal(t, customAPIVersion, gjson.GetBytes(body, "anthropic_version").String())
	})
	t.Run("Request with Thinking enabled", func(t *testing.T) {
		thinkingReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			AnthropicVendorFields: &openai.AnthropicVendorFields{
				Thinking: &anthropic.ThinkingConfigParamUnion{
					OfEnabled: &anthropic.ThinkingConfigEnabledParam{},
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, bm, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, bm)

		body := bm
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
			AnthropicVendorFields: &openai.AnthropicVendorFields{
				Thinking: &anthropic.ThinkingConfigParamUnion{
					OfDisabled: &anthropic.ThinkingConfigDisabledParam{},
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
		_, bm, err := translator.RequestBody(nil, thinkingReq, false)
		require.NoError(t, err)
		require.NotNil(t, bm)

		body := bm
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
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "Hello there!"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 5},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
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
				Role: constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content: []anthropic.ContentBlockUnion{
					{Type: "text", Text: "Ok, I will call the tool."},
					{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: json.RawMessage(`{"location": "Tokyo", "unit": "celsius"}`)},
				},
				StopReason: anthropic.StopReasonToolUse,
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15, CacheReadInputTokens: 10},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage: openai.Usage{
					PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40,
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
				Model:  "claude-3-5-sonnet-20241022",
				Object: "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     8,
					CompletionTokens: 12,
					TotalTokens:      20,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.inputResponse)
			require.NoError(t, err, "Test setup failed: could not marshal input struct")

			translator := NewChatCompletionOpenAIToGCPAnthropicTranslator("", "")
			hm, bm, usedToken, _, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true, nil)

			require.NoError(t, err, "Translator returned an unexpected internal error")
			require.NotNil(t, hm)
			require.NotNil(t, bm)

			newBody := bm
			require.NotNil(t, newBody)
			require.Len(t, hm, 1)
			require.Equal(t, contentLengthHeaderName, hm[0].Key())
			require.Equal(t, strconv.Itoa(len(newBody)), hm[0].Value())

			var gotResp openai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := LLMTokenUsage{
				InputTokens:       uint32(tt.expectedOpenAIResponse.Usage.PromptTokens),                     //nolint:gosec
				OutputTokens:      uint32(tt.expectedOpenAIResponse.Usage.CompletionTokens),                 //nolint:gosec
				TotalTokens:       uint32(tt.expectedOpenAIResponse.Usage.TotalTokens),                      //nolint:gosec
				CachedInputTokens: uint32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CachedTokens), //nolint:gosec
			}
			require.Equal(t, expectedTokenUsage, usedToken)

			if diff := cmp.Diff(tt.expectedOpenAIResponse, gotResp); diff != "" {
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

func TestOpenAIToGCPAnthropicTranslator_ResponseError(t *testing.T) {
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
			hm, bm, err := o.ResponseError(tt.responseHeaders, reader)

			require.NoError(t, err)
			require.NotNil(t, bm)
			require.NotNil(t, hm)
			require.Len(t, hm, 2)
			require.Equal(t, contentTypeHeaderName, hm[0].Key())
			require.Equal(t, jsonContentType, hm[0].Value()) //nolint:testifylint
			require.Equal(t, contentLengthHeaderName, hm[1].Key())
			require.Equal(t, strconv.Itoa(len(bm)), hm[1].Value())

			var gotError openai.Error
			err = json.Unmarshal(bm, &gotError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedOutput, gotError); diff != "" {
				t.Errorf("ResponseError() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// New test function for helper coverage.
func TestHelperFunctions(t *testing.T) {
	t.Run("anthropicToOpenAIFinishReason invalid reason", func(t *testing.T) {
		_, err := anthropicToOpenAIFinishReason("unknown_reason")
		require.Error(t, err)
		require.Contains(t, err.Error(), "received invalid stop reason")
	})

	t.Run("anthropicRoleToOpenAIRole invalid role", func(t *testing.T) {
		_, err := anthropicRoleToOpenAIRole("unknown_role")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid anthropic role")
	})
}

func TestTranslateOpenAItoAnthropicTools(t *testing.T) {
	anthropicTestTool := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
	}
	openaiTestTool := []openai.Tool{
		{Type: "function", Function: &openai.FunctionDefinition{Name: "get_weather"}},
	}
	tests := []struct {
		name               string
		openAIReq          *openai.ChatCompletionRequest
		expectedTools      []anthropic.ToolUnionParam
		expectedToolChoice anthropic.ToolChoiceUnionParam
		expectErr          bool
	}{
		{
			name: "auto tool choice",
			openAIReq: &openai.ChatCompletionRequest{
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{
					DisableParallelToolUse: anthropic.Bool(false),
				},
			},
		},
		{
			name: "any tool choice",
			openAIReq: &openai.ChatCompletionRequest{
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "any"},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			},
		},
		{
			name: "specific tool choice by name",
			openAIReq: &openai.ChatCompletionRequest{
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: openai.ChatCompletionNamedToolChoice{Type: "function", Function: openai.ChatCompletionNamedToolChoiceFunction{Name: "my_func"}}},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{Type: "tool", Name: "my_func"},
			},
		},
		{
			name: "tool definition",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the weather",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]any{
								"location": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
		{
			name: "tool_definition_with_required_field",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the weather with a required location",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
									"unit":     map[string]any{"type": "string"},
								},
								"required": []any{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather with a required location"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]any{
								"location": map[string]any{"type": "string"},
								"unit":     map[string]any{"type": "string"},
							},
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "tool definition with no parameters",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_time",
							Description: "Get the current time",
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_time",
						Description: anthropic.String("Get the current time"),
					},
				},
			},
		},
		{
			name: "disable parallel tool calls",
			openAIReq: &openai.ChatCompletionRequest{
				ToolChoice:        &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
				Tools:             openaiTestTool,
				ParallelToolCalls: ptr.To(false),
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{
					DisableParallelToolUse: anthropic.Bool(true),
				},
			},
		},
		{
			name: "explicitly enable parallel tool calls",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:             openaiTestTool,
				ToolChoice:        &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
				ParallelToolCalls: ptr.To(true),
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(false)},
			},
		},
		{
			name: "default disable parallel tool calls to false (nil)",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(false)},
			},
		},
		{
			name: "none tool choice",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "none"},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			},
		},
		{
			name: "function tool choice",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "function"},
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{Name: "function"},
			},
		},
		{
			name: "invalid tool choice string",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: "invalid_choice"},
			},
			expectErr: true,
		},
		{
			name: "skips function tool with nil function definition",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type:     "function",
						Function: nil, // This tool has the correct type but a nil definition and should be skipped.
					},
					{
						Type:     "function",
						Function: &openai.FunctionDefinition{Name: "get_weather"}, // This is a valid tool.
					},
				},
			},
			// We expect only the valid function tool to be translated.
			expectedTools: []anthropic.ToolUnionParam{
				{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
			},
			expectErr: false,
		},
		{
			name: "skips non-function tools",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "retrieval",
					},
					{
						Type:     "function",
						Function: &openai.FunctionDefinition{Name: "get_weather"},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{OfTool: &anthropic.ToolParam{Name: "get_weather", Description: anthropic.String("")}},
			},
			expectErr: false,
		},
		{
			name: "tool definition without type field",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the weather without type",
							Parameters: map[string]any{
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
								},
								"required": []any{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather without type"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "",
							Properties: map[string]any{
								"location": map[string]any{"type": "string"},
							},
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "tool definition without properties field",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the weather without properties",
							Parameters: map[string]any{
								"type":     "object",
								"required": []any{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "get_weather",
						Description: anthropic.String("Get the weather without properties"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type:     "object",
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "unsupported tool_choice type",
			openAIReq: &openai.ChatCompletionRequest{
				Tools:      openaiTestTool,
				ToolChoice: &openai.ChatCompletionToolChoiceUnion{Value: 123}, // Use an integer to trigger the default case.
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, toolChoice, err := translateOpenAItoAnthropicTools(tt.openAIReq.Tools, tt.openAIReq.ToolChoice, tt.openAIReq.ParallelToolCalls)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.openAIReq.ToolChoice != nil {
					require.NotNil(t, toolChoice)
					require.Equal(t, *tt.expectedToolChoice.GetType(), *toolChoice.GetType())
					if tt.expectedToolChoice.GetName() != nil {
						require.Equal(t, *tt.expectedToolChoice.GetName(), *toolChoice.GetName())
					}
					if tt.expectedToolChoice.OfTool != nil {
						require.Equal(t, tt.expectedToolChoice.OfTool.Name, toolChoice.OfTool.Name)
					}
					if tt.expectedToolChoice.OfAuto != nil {
						require.Equal(t, tt.expectedToolChoice.OfAuto.DisableParallelToolUse, toolChoice.OfAuto.DisableParallelToolUse)
					}
				}
				if tt.openAIReq.Tools != nil {
					require.NotNil(t, tools)
					require.Len(t, tools, len(tt.expectedTools))
					require.Equal(t, tt.expectedTools[0].GetName(), tools[0].GetName())
					require.Equal(t, tt.expectedTools[0].GetType(), tools[0].GetType())
					require.Equal(t, tt.expectedTools[0].GetDescription(), tools[0].GetDescription())
					if tt.expectedTools[0].GetInputSchema().Properties != nil {
						require.Equal(t, tt.expectedTools[0].GetInputSchema().Properties, tools[0].GetInputSchema().Properties)
					}
				}
			}
		})
	}
}

// TestFinishReasonTranslation covers specific cases for the anthropicToOpenAIFinishReason function.
func TestFinishReasonTranslation(t *testing.T) {
	tests := []struct {
		name                 string
		input                anthropic.StopReason
		expectedFinishReason openai.ChatCompletionChoicesFinishReason
		expectErr            bool
	}{
		{
			name:                 "max tokens stop reason",
			input:                anthropic.StopReasonMaxTokens,
			expectedFinishReason: openai.ChatCompletionChoicesFinishReasonLength,
		},
		{
			name:                 "refusal stop reason",
			input:                anthropic.StopReasonRefusal,
			expectedFinishReason: openai.ChatCompletionChoicesFinishReasonContentFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, err := anthropicToOpenAIFinishReason(tt.input)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedFinishReason, reason)
			}
		})
	}
}

// TestToolParameterDereferencing tests the JSON schema dereferencing functionality
// for tool parameters when translating from OpenAI to GCP Anthropic.
func TestToolParameterDereferencing(t *testing.T) {
	tests := []struct {
		name               string
		openAIReq          *openai.ChatCompletionRequest
		expectedTools      []anthropic.ToolUnionParam
		expectedToolChoice anthropic.ToolChoiceUnionParam
		expectErr          bool
		expectedErrMsg     string
	}{
		{
			name: "tool with complex nested $ref - successful dereferencing",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "complex_tool",
							Description: "Tool with complex nested references",
							Parameters: map[string]any{
								"type": "object",
								"$defs": map[string]any{
									"BaseType": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"id": map[string]any{
												"type": "string",
											},
											"required": []any{"id"},
										},
									},
									"NestedType": map[string]any{
										"allOf": []any{
											map[string]any{"$ref": "#/$defs/BaseType"},
											map[string]any{
												"properties": map[string]any{
													"name": map[string]any{
														"type": "string",
													},
												},
											},
										},
									},
								},
								"properties": map[string]any{
									"nested": map[string]any{
										"$ref": "#/$defs/NestedType",
									},
								},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "complex_tool",
						Description: anthropic.String("Tool with complex nested references"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]any{
								"nested": map[string]any{
									"allOf": []any{
										map[string]any{
											"type": "object",
											"properties": map[string]any{
												"id": map[string]any{
													"type": "string",
												},
												"required": []any{"id"},
											},
										},
										map[string]any{
											"properties": map[string]any{
												"name": map[string]any{
													"type": "string",
												},
											},
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
			name: "tool with invalid $ref - dereferencing error",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "invalid_ref_tool",
							Description: "Tool with invalid reference",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{
										"$ref": "#/$defs/NonExistent",
									},
								},
							},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "failed to dereference tool parameters",
		},
		{
			name: "tool with circular $ref - dereferencing error",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "circular_ref_tool",
							Description: "Tool with circular reference",
							Parameters: map[string]any{
								"type": "object",
								"$defs": map[string]any{
									"A": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"b": map[string]any{
												"$ref": "#/$defs/B",
											},
										},
									},
									"B": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"a": map[string]any{
												"$ref": "#/$defs/A",
											},
										},
									},
								},
								"properties": map[string]any{
									"circular": map[string]any{
										"$ref": "#/$defs/A",
									},
								},
							},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "failed to dereference tool parameters",
		},
		{
			name: "tool without $ref - no dereferencing needed",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "simple_tool",
							Description: "Simple tool without references",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{
										"type": "string",
									},
								},
								"required": []any{"location"},
							},
						},
					},
				},
			},
			expectedTools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name:        "simple_tool",
						Description: anthropic.String("Simple tool without references"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
							Properties: map[string]any{
								"location": map[string]any{
									"type": "string",
								},
							},
							Required: []string{"location"},
						},
					},
				},
			},
		},
		{
			name: "tool parameter dereferencing returns non-map type - casting error",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "problematic_tool",
							Description: "Tool with parameters that can't be properly dereferenced to map",
							// This creates a scenario where jsonSchemaDereference might return a non-map type
							// though this is a contrived example since normally the function should return map[string]any
							Parameters: map[string]any{
								"$ref": "#/$defs/StringType", // This would resolve to a string, not a map
								"$defs": map[string]any{
									"StringType": "not-a-map", // This would cause the casting to fail
								},
							},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "failed to cast dereferenced tool parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, toolChoice, err := translateOpenAItoAnthropicTools(tt.openAIReq.Tools, tt.openAIReq.ToolChoice, tt.openAIReq.ParallelToolCalls)

			if tt.expectErr {
				require.Error(t, err)
				if tt.expectedErrMsg != "" {
					require.Contains(t, err.Error(), tt.expectedErrMsg)
				}
				return
			}

			require.NoError(t, err)

			if tt.openAIReq.Tools != nil {
				require.NotNil(t, tools)
				require.Len(t, tools, len(tt.expectedTools))

				for i, expectedTool := range tt.expectedTools {
					actualTool := tools[i]
					require.Equal(t, expectedTool.GetName(), actualTool.GetName())
					require.Equal(t, expectedTool.GetType(), actualTool.GetType())
					require.Equal(t, expectedTool.GetDescription(), actualTool.GetDescription())

					expectedSchema := expectedTool.GetInputSchema()
					actualSchema := actualTool.GetInputSchema()

					require.Equal(t, expectedSchema.Type, actualSchema.Type)
					require.Equal(t, expectedSchema.Required, actualSchema.Required)

					// For properties, we'll do a deep comparison to verify dereferencing worked
					if expectedSchema.Properties != nil {
						require.NotNil(t, actualSchema.Properties)
						require.Equal(t, expectedSchema.Properties, actualSchema.Properties)
					}
				}
			}

			if tt.openAIReq.ToolChoice != nil {
				require.NotNil(t, toolChoice)
				require.Equal(t, *tt.expectedToolChoice.GetType(), *toolChoice.GetType())
			}
		})
	}
}

// TestContentTranslationCoverage adds specific coverage for the openAIToAnthropicContent helper.
func TestContentTranslationCoverage(t *testing.T) {
	tests := []struct {
		name            string
		inputContent    any
		expectedContent []anthropic.ContentBlockParamUnion
		expectErr       bool
	}{
		{
			name:         "nil content",
			inputContent: nil,
		},
		{
			name:         "empty string content",
			inputContent: "",
		},
		{
			name: "pdf data uri",
			inputContent: []openai.ChatCompletionContentPartUserUnionParam{
				{OfImageURL: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "data:application/pdf;base64,dGVzdA=="}}},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfDocument: &anthropic.DocumentBlockParam{
						Source: anthropic.DocumentBlockParamSourceUnion{
							OfBase64: &anthropic.Base64PDFSourceParam{
								Type:      constant.ValueOf[constant.Base64](),
								MediaType: constant.ValueOf[constant.ApplicationPDF](),
								Data:      "dGVzdA==",
							},
						},
					},
				},
			},
		},
		{
			name: "pdf url",
			inputContent: []openai.ChatCompletionContentPartUserUnionParam{
				{OfImageURL: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "https://example.com/doc.pdf"}}},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfDocument: &anthropic.DocumentBlockParam{
						Source: anthropic.DocumentBlockParamSourceUnion{
							OfURL: &anthropic.URLPDFSourceParam{
								Type: constant.ValueOf[constant.URL](),
								URL:  "https://example.com/doc.pdf",
							},
						},
					},
				},
			},
		},
		{
			name: "image url",
			inputContent: []openai.ChatCompletionContentPartUserUnionParam{
				{OfImageURL: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "https://example.com/image.png"}}},
			},
			expectedContent: []anthropic.ContentBlockParamUnion{
				{
					OfImage: &anthropic.ImageBlockParam{
						Source: anthropic.ImageBlockParamSourceUnion{
							OfURL: &anthropic.URLImageSourceParam{
								Type: constant.ValueOf[constant.URL](),
								URL:  "https://example.com/image.png",
							},
						},
					},
				},
			},
		},
		{
			name:         "audio content error",
			inputContent: []openai.ChatCompletionContentPartUserUnionParam{{OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{}}},
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := openAIToAnthropicContent(tt.inputContent)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Use direct assertions instead of cmp.Diff to avoid panics on unexported fields.
			require.Len(t, content, len(tt.expectedContent), "Number of content blocks should match")

			// Use direct assertions instead of cmp.Diff to avoid panics on unexported fields.
			require.Len(t, content, len(tt.expectedContent), "Number of content blocks should match")
			for i, expectedBlock := range tt.expectedContent {
				actualBlock := content[i]
				require.Equal(t, expectedBlock.GetType(), actualBlock.GetType(), "Content block types should match")
				if expectedBlock.OfDocument != nil {
					require.NotNil(t, actualBlock.OfDocument, "Expected a document block, but got nil")
					require.NotNil(t, actualBlock.OfDocument.Source, "Document source should not be nil")

					if expectedBlock.OfDocument.Source.OfBase64 != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfBase64, "Expected a base64 source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfBase64.Data, actualBlock.OfDocument.Source.OfBase64.Data)
					}
					if expectedBlock.OfDocument.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfURL, "Expected a URL source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfURL.URL, actualBlock.OfDocument.Source.OfURL.URL)
					}
				}
				if expectedBlock.OfImage != nil {
					require.NotNil(t, actualBlock.OfImage, "Expected an image block, but got nil")
					require.NotNil(t, actualBlock.OfImage.Source, "Image source should not be nil")

					if expectedBlock.OfImage.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfImage.Source.OfURL, "Expected a URL image source")
						require.Equal(t, expectedBlock.OfImage.Source.OfURL.URL, actualBlock.OfImage.Source.OfURL.URL)
					}
				}
			}

			for i, expectedBlock := range tt.expectedContent {
				actualBlock := content[i]
				if expectedBlock.OfDocument != nil {
					require.NotNil(t, actualBlock.OfDocument, "Expected a document block, but got nil")
					require.NotNil(t, actualBlock.OfDocument.Source, "Document source should not be nil")

					if expectedBlock.OfDocument.Source.OfBase64 != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfBase64, "Expected a base64 source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfBase64.Data, actualBlock.OfDocument.Source.OfBase64.Data)
					}
					if expectedBlock.OfDocument.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfDocument.Source.OfURL, "Expected a URL source")
						require.Equal(t, expectedBlock.OfDocument.Source.OfURL.URL, actualBlock.OfDocument.Source.OfURL.URL)
					}
				}
				if expectedBlock.OfImage != nil {
					require.NotNil(t, actualBlock.OfImage, "Expected an image block, but got nil")
					require.NotNil(t, actualBlock.OfImage.Source, "Image source should not be nil")

					if expectedBlock.OfImage.Source.OfURL != nil {
						require.NotNil(t, actualBlock.OfImage.Source.OfURL, "Expected a URL image source")
						require.Equal(t, expectedBlock.OfImage.Source.OfURL.URL, actualBlock.OfImage.Source.OfURL.URL)
					}
				}
			}
		})
	}
}

// TestSystemPromptExtractionCoverage adds specific coverage for the extractSystemPromptFromDeveloperMsg helper.
func TestSystemPromptExtractionCoverage(t *testing.T) {
	tests := []struct {
		name           string
		inputMsg       openai.ChatCompletionDeveloperMessageParam
		expectedPrompt string
	}{
		{
			name: "developer message with content parts",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
					{Type: "text", Text: "part 1"},
					{Type: "text", Text: " part 2"},
				}},
			},
			expectedPrompt: "part 1 part 2",
		},
		{
			name:           "developer message with nil content",
			inputMsg:       openai.ChatCompletionDeveloperMessageParam{Content: openai.ContentUnion{Value: nil}},
			expectedPrompt: "",
		},
		{
			name: "developer message with string content",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ContentUnion{Value: "simple string"},
			},
			expectedPrompt: "simple string",
		},
		{
			name: "developer message with text parts array",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ContentUnion{Value: []openai.ChatCompletionContentPartTextParam{
					{Type: "text", Text: "text part"},
				}},
			},
			expectedPrompt: "text part",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := extractSystemPromptFromDeveloperMsg(tt.inputMsg)
			require.Equal(t, tt.expectedPrompt, prompt)
		})
	}
}

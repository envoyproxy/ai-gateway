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
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	claudeTestModel = "claude-3-opus-20240229"
	testTool        = "test_123"
)

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	// Define a common input request to use for both standard and vertex tests.
	openAIReq := &openai.ChatCompletionRequest{
		Model: claudeTestModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				Type:  openai.ChatMessageRoleSystem,
				Value: openai.ChatCompletionSystemMessageParam{Content: openai.StringOrArray{Value: "You are a helpful assistant."}},
			},
			{
				Type:  openai.ChatMessageRoleUser,
				Value: openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"}},
			},
		},
		MaxTokens:   ptr.To(int64(1024)),
		Temperature: ptr.To(0.7),
	}

	t.Run("Vertex Values Configured Correctly", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		hm, bm, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, bm)

		// Check the path header.
		pathHeader := hm.SetHeaders[0]
		require.Equal(t, ":path", pathHeader.Header.Key)
		expectedPath := fmt.Sprintf("publishers/anthropic/models/%s:rawPredict", openAIReq.Model)
		require.Equal(t, expectedPath, string(pathHeader.Header.RawValue))

		// Check the body content.
		body := bm.GetBody()
		require.NotNil(t, body)
		// Model should NOT be present in the body for GCP Vertex.
		require.False(t, gjson.GetBytes(body, "model").Exists())
		// Anthropic version should be present for GCP Vertex.
		require.Equal(t, anthropicVertex.DefaultVersion, gjson.GetBytes(body, "anthropic_version").String())
	})

	t.Run("Image Content Request", func(t *testing.T) {
		imageReq := &openai.ChatCompletionRequest{
			Model: "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					Type: openai.ChatMessageRoleUser,
					Value: openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "What is in this image?"}},
								{ImageContent: &openai.ChatCompletionContentPartImageParam{
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/jpeg;base64,dGVzdA==", // "test" in base64.
									},
								}},
							},
						},
					},
				},
			},
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, bm, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)
		body := bm.GetBody()
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
				{Type: openai.ChatMessageRoleSystem, Value: openai.ChatCompletionSystemMessageParam{Content: openai.StringOrArray{Value: firstMsg}}},
				{Type: openai.ChatMessageRoleDeveloper, Value: openai.ChatCompletionDeveloperMessageParam{Content: openai.StringOrArray{Value: secondMsg}}},
				{Type: openai.ChatMessageRoleUser, Value: openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: thirdMsg}}},
			},
			MaxTokens: ptr.To(int64(100)),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, bm, err := translator.RequestBody(nil, multiSystemReq, false)
		require.NoError(t, err)
		body := bm.GetBody()
		require.Equal(t, firstMsg, gjson.GetBytes(body, "system.0.text").String())
		require.Equal(t, secondMsg, gjson.GetBytes(body, "system.1.text").String())
		require.Equal(t, thirdMsg, gjson.GetBytes(body, "messages.0.content.0.text").String())
	})

	t.Run("Streaming Request Error", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), errStreamingNotSupported.Error())
	})

	t.Run("Invalid Temperature (above bound)", func(t *testing.T) {
		invalidTempReq := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.5),
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
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
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf(tempNotSupportedError, *invalidTempReq.Temperature))
	})

	// Test for missing required parameter.
	t.Run("Missing MaxTokens Uses Default", func(t *testing.T) {
		missingTokensReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: nil,
		}
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, bm, err := translator.RequestBody(nil, missingTokensReq, false)
		require.NoError(t, err)
		body := bm.GetBody()
		require.Equal(t, defaultMaxTokens, gjson.GetBytes(body, "max_tokens").Int())
	})
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid json body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
		_, _, _, err := translator.ResponseBody(map[string]string{statusHeaderName: "200"}, bytes.NewBufferString("invalid json"), true)
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
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage:  openai.ChatCompletionResponseUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
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
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage:  openai.ChatCompletionResponseUsage{PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Role:    string(anthropic.MessageParamRoleAssistant),
							Content: ptr.To("Ok, I will call the tool."),
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID:   "toolu_01",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.inputResponse)
			require.NoError(t, err, "Test setup failed: could not marshal input struct")

			translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()
			hm, bm, usedToken, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true)

			require.NoError(t, err, "Translator returned an unexpected internal error")
			require.NotNil(t, hm)
			require.NotNil(t, bm)

			newBody := bm.GetBody()
			require.NotNil(t, newBody)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var gotResp openai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := LLMTokenUsage{
				InputTokens:  uint32(tt.expectedOpenAIResponse.Usage.PromptTokens),     //nolint:gosec
				OutputTokens: uint32(tt.expectedOpenAIResponse.Usage.CompletionTokens), //nolint:gosec
				TotalTokens:  uint32(tt.expectedOpenAIResponse.Usage.TotalTokens),      //nolint:gosec
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
					Type: openai.ChatMessageRoleAssistant,
					Value: openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{Value: "Hello from the assistant."},
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
					Type: openai.ChatMessageRoleAssistant,
					Value: openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       testTool,
								Type:     openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":"NYC"}`},
							},
						},
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
								Input: map[string]interface{}{"location": "NYC"},
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
					Type: openai.ChatMessageRoleAssistant,
					Value: openai.ChatCompletionAssistantMessageParam{
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: openai.ChatCompletionAssistantMessageParamContent{
								Type:    openai.ChatCompletionAssistantMessageParamContentTypeRefusal,
								Refusal: ptr.To("I cannot answer that."),
							},
						},
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
					Type: openai.ChatMessageRoleTool,
					Value: openai.ChatCompletionToolMessageParam{
						ToolCallID: testTool,
						Content: openai.StringOrArray{
							Value: "The weather is 72 degrees and sunny.",
						},
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
				{Type: openai.ChatMessageRoleSystem, Value: openai.ChatCompletionSystemMessageParam{Content: openai.StringOrArray{Value: "System prompt."}}},
				{Type: openai.ChatMessageRoleUser, Value: openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "User message."}}},
				{Type: openai.ChatMessageRoleDeveloper, Value: openai.ChatCompletionDeveloperMessageParam{Content: openai.StringOrArray{Value: "Developer prompt."}}},
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
					Type: openai.ChatMessageRoleUser,
					Value: openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: 0,
						},
					},
				},
			},
			expectErr: true,
		},
		{
			name: "assistant message with tool call error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					Type: openai.ChatMessageRoleAssistant,
					Value: openai.ChatCompletionAssistantMessageParam{
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:       testTool,
								Type:     openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: "get_weather", Arguments: `{"location":`},
							},
						},
					},
				},
			},
			expectErr: true,
		},
		{
			name: "tool message with content error",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					Type: openai.ChatMessageRoleTool,
					Value: openai.ChatCompletionToolMessageParam{
						ToolCallID: testTool,
						Content:    openai.StringOrArray{Value: 123},
					},
				},
			},
			expectErr: true,
		},
		{
			name: "tool message with image content",
			inputMessages: []openai.ChatCompletionMessageParamUnion{
				{
					Type: openai.ChatMessageRoleTool,
					Value: openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_def",
						Content: openai.StringOrArray{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{
									ImageContent: &openai.ChatCompletionContentPartImageParam{
										ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
											URL: "data:image/png;base64,dGVzdA==",
										},
									},
								},
							},
						},
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
										OfImage: &anthropic.ImageBlockParam{
											Source: anthropic.ImageBlockParamSourceUnion{
												OfBase64: &anthropic.Base64ImageSourceParam{
													Data:      "dGVzdA==",
													MediaType: "image/png",
													Type:      "base64",
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
						require.Equal(t, expectedContent.GetType(), actualContent.GetType(), "Content block types should match")
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
		inputBody       interface{}
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

			newBody := bm.GetBody()
			require.NotNil(t, newBody)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var gotError openai.Error
			err = json.Unmarshal(newBody, &gotError)
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

	t.Run("process stop with nil", func(t *testing.T) {
		val, err := processStop(nil)
		require.NoError(t, err)
		require.Nil(t, val)
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
				ToolChoice: "auto",
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
				ToolChoice: "any",
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
				ToolChoice: openai.ToolChoice{Type: "function", Function: openai.ToolFunction{Name: "my_func"}},
				Tools:      openaiTestTool,
			},
			expectedTools: anthropicTestTool,
			expectedToolChoice: anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{Type: "function", Name: "my_func"},
			},
		},
		{
			name: "invalid tool parameters",
			openAIReq: &openai.ChatCompletionRequest{
				Tools: []openai.Tool{
					{Type: "function", Function: &openai.FunctionDefinition{Name: "test", Parameters: "not-a-map"}},
				},
			},
			expectErr: true,
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
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
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
							Properties: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
								},
							},
							Type:        "function",
							ExtraFields: nil,
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
				ToolChoice:        "auto",
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
				ToolChoice:        "auto",
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
				ToolChoice: "auto",
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
				ToolChoice: "none",
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
				ToolChoice: "function",
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
				ToolChoice: "invalid_choice",
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

// TestContentTranslationCoverage adds specific coverage for the openAIToAnthropicContent helper.
func TestContentTranslationCoverage(t *testing.T) {
	tests := []struct {
		name            string
		inputContent    interface{}
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
				{ImageContent: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "data:application/pdf;base64,dGVzdA=="}}},
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
				{ImageContent: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "https://example.com/doc.pdf"}}},
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
				{ImageContent: &openai.ChatCompletionContentPartImageParam{ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: "https://example.com/image.png"}}},
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
			inputContent: []openai.ChatCompletionContentPartUserUnionParam{{InputAudioContent: &openai.ChatCompletionContentPartInputAudioParam{}}},
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
				Content: openai.StringOrArray{Value: []openai.ChatCompletionContentPartUserUnionParam{
					{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "part 1"}},
					{TextContent: &openai.ChatCompletionContentPartTextParam{Text: " part 2"}},
				}},
			},
			expectedPrompt: "part 1 part 2",
		},
		{
			name:           "developer message with nil content",
			inputMsg:       openai.ChatCompletionDeveloperMessageParam{Content: openai.StringOrArray{Value: nil}},
			expectedPrompt: "",
		},
		{
			name: "developer message with StringOrArray of string",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.StringOrArray{Value: openai.StringOrArray{Value: "nested string"}},
			},
			expectedPrompt: "nested string",
		},
		{
			name: "developer message with StringOrArray of parts",
			inputMsg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.StringOrArray{Value: openai.StringOrArray{Value: []openai.ChatCompletionContentPartUserUnionParam{
					{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "nested part"}},
				}}},
			},
			expectedPrompt: "nested part",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := extractSystemPromptFromDeveloperMsg(tt.inputMsg)
			require.Equal(t, tt.expectedPrompt, prompt)
		})
	}
}

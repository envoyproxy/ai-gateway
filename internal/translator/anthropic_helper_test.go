// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

const (
	testTool = "test_123"
)

// tokenUsageFrom is a test helper to create TokenUsage from token counts
func tokenUsageFrom(in, cachedInput, out, total int32) metrics.TokenUsage {
	var usage metrics.TokenUsage
	if in >= 0 {
		usage.SetInputTokens(uint32(in)) // nolint:gosec
	}
	if cachedInput >= 0 {
		usage.SetCachedInputTokens(uint32(cachedInput)) // nolint:gosec
	}
	if out >= 0 {
		usage.SetOutputTokens(uint32(out)) // nolint:gosec
	}
	if total >= 0 {
		usage.SetTotalTokens(uint32(total)) // nolint:gosec
	}
	return usage
}

// mockErrorReader is a helper for testing io.Reader failures.
type mockErrorReader struct{}

func (r *mockErrorReader) Read(_ []byte) (n int, err error) {
	return 0, fmt.Errorf("mock reader error")
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
			prompt, _ := extractSystemPromptFromDeveloperMsg(tt.inputMsg)
			require.Equal(t, tt.expectedPrompt, prompt)
		})
	}
}

func TestExtractLLMTokenUsage(t *testing.T) {
	tests := []struct {
		name                 string
		inputTokens          int64
		outputTokens         int64
		cacheReadTokens      int64
		cacheCreationTokens  int64
		expectedInputTokens  uint32
		expectedOutputTokens uint32
		expectedTotalTokens  uint32
		expectedCachedTokens uint32
	}{
		{
			name:                 "basic usage without cache",
			inputTokens:          100,
			outputTokens:         50,
			cacheReadTokens:      0,
			cacheCreationTokens:  0,
			expectedInputTokens:  100,
			expectedOutputTokens: 50,
			expectedTotalTokens:  150,
			expectedCachedTokens: 0,
		},
		{
			name:                 "usage with cache read tokens",
			inputTokens:          80,
			outputTokens:         30,
			cacheReadTokens:      20,
			cacheCreationTokens:  0,
			expectedInputTokens:  100, // 80 + 0 + 20
			expectedOutputTokens: 30,
			expectedTotalTokens:  130, // 100 + 30
			expectedCachedTokens: 20,  // 20 + 0
		},
		{
			name:                 "usage with cache creation tokens",
			inputTokens:          60,
			outputTokens:         40,
			cacheReadTokens:      0,
			cacheCreationTokens:  15,
			expectedInputTokens:  75, // 60 + 15 + 0
			expectedOutputTokens: 40,
			expectedTotalTokens:  115, // 75 + 40
			expectedCachedTokens: 15,  // 0 + 15
		},
		{
			name:                 "usage with both cache types",
			inputTokens:          70,
			outputTokens:         25,
			cacheReadTokens:      10,
			cacheCreationTokens:  5,
			expectedInputTokens:  85, // 70 + 5 + 10
			expectedOutputTokens: 25,
			expectedTotalTokens:  110, // 85 + 25
			expectedCachedTokens: 15,  // 10 + 5
		},
		{
			name:                 "zero values",
			inputTokens:          0,
			outputTokens:         0,
			cacheReadTokens:      0,
			cacheCreationTokens:  0,
			expectedInputTokens:  0,
			expectedOutputTokens: 0,
			expectedTotalTokens:  0,
			expectedCachedTokens: 0,
		},
		{
			name:                 "large values",
			inputTokens:          100000,
			outputTokens:         50000,
			cacheReadTokens:      25000,
			cacheCreationTokens:  15000,
			expectedInputTokens:  140000, // 100000 + 15000 + 25000
			expectedOutputTokens: 50000,
			expectedTotalTokens:  190000, // 140000 + 50000
			expectedCachedTokens: 40000,  // 25000 + 15000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractTokenUsageFromAnthropic(
				tt.inputTokens,
				tt.outputTokens,
				tt.cacheReadTokens,
				tt.cacheCreationTokens,
			)

			expected := tokenUsageFrom(
				int32(tt.expectedInputTokens), // nolint:gosec
				-1,
				int32(tt.expectedOutputTokens), // nolint:gosec
				int32(tt.expectedTotalTokens),  // nolint:gosec
			)
			expected.SetCachedInputTokens(tt.expectedCachedTokens)
			assert.Equal(t, expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromUsage(t *testing.T) {
	tests := []struct {
		name                 string
		usage                anthropic.Usage
		expectedInputTokens  int32
		expectedOutputTokens int32
		expectedTotalTokens  int32
		expectedCachedTokens uint32
	}{
		{
			name: "non-streaming response without cache",
			usage: anthropic.Usage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:  150,
			expectedOutputTokens: 75,
			expectedTotalTokens:  225,
			expectedCachedTokens: 0,
		},
		{
			name: "non-streaming response with cache read",
			usage: anthropic.Usage{
				InputTokens:              100,
				OutputTokens:             50,
				CacheReadInputTokens:     25,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:  125, // 100 + 0 + 25
			expectedOutputTokens: 50,
			expectedTotalTokens:  175, // 125 + 50
			expectedCachedTokens: 25,  // 25 + 0
		},
		{
			name: "non-streaming response with both cache types",
			usage: anthropic.Usage{
				InputTokens:              90,
				OutputTokens:             60,
				CacheReadInputTokens:     15,
				CacheCreationInputTokens: 10,
			},
			expectedInputTokens:  115, // 90 + 10 + 15
			expectedOutputTokens: 60,
			expectedTotalTokens:  175, // 115 + 60
			expectedCachedTokens: 25,  // 15 + 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractTokenUsageFromAnthropic(tt.usage.InputTokens,
				tt.usage.OutputTokens,
				tt.usage.CacheReadInputTokens,
				tt.usage.CacheCreationInputTokens,
			)
			expected := tokenUsageFrom(tt.expectedInputTokens, 0, tt.expectedOutputTokens, tt.expectedTotalTokens)
			expected.SetCachedInputTokens(tt.expectedCachedTokens)
			assert.Equal(t, expected, result)
		})
	}
}

func TestExtractLLMTokenUsageFromDeltaUsage(t *testing.T) {
	tests := []struct {
		name                 string
		usage                anthropic.MessageDeltaUsage
		expectedInputTokens  int32
		expectedOutputTokens int32
		expectedTotalTokens  int32
		expectedCachedTokens uint32
	}{
		{
			name: "message_delta event with final totals",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              250,
				OutputTokens:             120,
				CacheReadInputTokens:     30,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:  280, // 250 + 0 + 30
			expectedOutputTokens: 120,
			expectedTotalTokens:  400, // 280 + 120
			expectedCachedTokens: 30,  // 30 + 0
		},
		{
			name: "message_delta event with only output tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              0,
				OutputTokens:             85,
				CacheReadInputTokens:     0,
				CacheCreationInputTokens: 0,
			},
			expectedInputTokens:  0,
			expectedOutputTokens: 85,
			expectedTotalTokens:  85,
			expectedCachedTokens: 0,
		},
		{
			name: "message_delta with cache creation tokens",
			usage: anthropic.MessageDeltaUsage{
				InputTokens:              150,
				OutputTokens:             75,
				CacheReadInputTokens:     10,
				CacheCreationInputTokens: 5,
			},
			expectedInputTokens:  165, // 150 + 5 + 10
			expectedOutputTokens: 75,
			expectedTotalTokens:  240, // 165 + 75
			expectedCachedTokens: 15,  // 10 + 5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractTokenUsageFromAnthropic(tt.usage.InputTokens,
				tt.usage.OutputTokens,
				tt.usage.CacheReadInputTokens,
				tt.usage.CacheCreationInputTokens,
			)
			expected := tokenUsageFrom(tt.expectedInputTokens, 0, tt.expectedOutputTokens, tt.expectedTotalTokens)
			expected.SetCachedInputTokens(tt.expectedCachedTokens)
			assert.Equal(t, expected, result)
		})
	}
}

// Test edge cases and boundary conditions.
func TestExtractLLMTokenUsage_EdgeCases(t *testing.T) {
	t.Run("negative values should be handled", func(t *testing.T) {
		// Note: In practice, the Anthropic API shouldn't return negative values,
		// but our function should handle them gracefully by casting to uint32.
		result := ExtractTokenUsageFromAnthropic(-10, -5, -2, -1)

		// Negative int64 values will wrap around when cast to uint32.
		// This test documents current behavior rather than prescribing it.
		// The exact values aren't important, just that it doesn't panic.
		assert.NotNil(t, result)
	})

	t.Run("maximum int64 values", func(t *testing.T) {
		// Test with very large values to ensure no overflow issues.
		// Note: This will result in truncation when casting to uint32.
		result := ExtractTokenUsageFromAnthropic(9223372036854775807, 1000, 500, 100)
		assert.NotNil(t, result)
	})
}

// Test that demonstrates the correct calculation according to Claude API docs.
func TestExtractLLMTokenUsage_ClaudeAPIDocumentationCompliance(t *testing.T) {
	t.Run("claude API documentation example", func(t *testing.T) {
		// This test verifies compliance with Claude API documentation:
		// "Total input tokens in a request is the summation of input_tokens,
		// cache_creation_input_tokens, and cache_read_input_tokens".

		inputTokens := int64(100)
		cacheCreationTokens := int64(20)
		cacheReadTokens := int64(30)
		outputTokens := int64(50)

		result := ExtractTokenUsageFromAnthropic(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)

		// Total input should be sum of all input token types.
		expectedTotalInputInt := inputTokens + cacheCreationTokens + cacheReadTokens
		expectedTotalInput := uint32(expectedTotalInputInt) // #nosec G115 - test values are small and safe
		inputTokensVal, ok := result.InputTokens()
		assert.True(t, ok)
		assert.Equal(t, expectedTotalInput, inputTokensVal,
			"InputTokens should be sum of input_tokens + cache_creation_input_tokens + cache_read_input_tokens")

		// Total cache should be sum of cache token types.
		expectedCacheTokensInt := cacheCreationTokens + cacheReadTokens
		expectedCacheTokens := uint32(expectedCacheTokensInt) // #nosec G115 - test values are small and safe
		cachedTokens, ok := result.CachedInputTokens()
		assert.True(t, ok)
		assert.Equal(t, expectedCacheTokens, cachedTokens,
			"CachedInputTokens should be sum of cache_creation_input_tokens + cache_read_input_tokens")

		// Total tokens should be input + output.
		expectedTotal := expectedTotalInput + uint32(outputTokens)
		totalTokens, ok := result.TotalTokens()
		assert.True(t, ok)
		assert.Equal(t, expectedTotal, totalTokens,
			"TotalTokens should be InputTokens + OutputTokens")
	})
}

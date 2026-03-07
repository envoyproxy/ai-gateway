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
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// mockErrorReader is a helper for testing io.Reader failures.
type mockErrorReader struct{}

func (r *mockErrorReader) Read(_ []byte) (n int, err error) {
	return 0, fmt.Errorf("mock reader error")
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
		{
			name: "nested schema in tool's defintions",
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
								"$defs": map[string]any{
									"ReferencePassage": map[string]any{
										"properties": map[string]any{
											"url": map[string]any{
												"title": "Url",
												"type":  "string",
											},
											"passage_id": map[string]any{
												"title": "Passage Id",
												"type":  "string",
											},
										},
										"required": []string{"url", "passage_id"},
										"title":    "ReferencePassage",
										"type":     "object",
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
						Name:        "get_weather",
						Description: anthropic.String("Get the weather without type"),
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "",
							Properties: map[string]any{
								"location": map[string]any{"type": "string"},
							},
							Required: []string{"location"},
							ExtraFields: map[string]any{
								"$defs": map[string]any{
									"ReferencePassage": map[string]any{
										"properties": map[string]any{
											"url": map[string]any{
												"title": "Url",
												"type":  "string",
											},
											"passage_id": map[string]any{
												"title": "Passage Id",
												"type":  "string",
											},
										},
										"required": []string{"url", "passage_id"},
										"title":    "ReferencePassage",
										"type":     "object",
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
					if tt.expectedTools[0].GetInputSchema().ExtraFields != nil {
						require.Equal(t, tt.expectedTools[0].GetInputSchema().ExtraFields, tools[0].GetInputSchema().ExtraFields)
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

func TestOutputConfigAvailable(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{
			name:     "claude-sonnet-4-5-20250514 supported",
			model:    "claude-sonnet-4-5-20250514",
			expected: true,
		},
		{
			name:     "claude-opus-4-6-20250514 supported",
			model:    "claude-opus-4-6-20250514",
			expected: true,
		},
		{
			name:     "anthropic.claude-4-5-sonnet-v1 supported",
			model:    "anthropic.claude-4-5-sonnet-v1",
			expected: true,
		},
		{
			name:     "claude-3-sonnet not supported",
			model:    "claude-3-sonnet",
			expected: false,
		},
		{
			name:     "claude-3.5-sonnet not supported",
			model:    "claude-3.5-sonnet",
			expected: false,
		},
		{
			name:     "gpt-4 not supported",
			model:    "gpt-4",
			expected: false,
		},
		{
			name:     "empty model not supported",
			model:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := outputConfigAvailable(tt.model)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildAnthropicParamsWithStructuredOutput(t *testing.T) {
	tests := []struct {
		name           string
		request        *openai.ChatCompletionRequest
		expectSchema   bool
		expectedSchema map[string]any
		expectErr      bool
	}{
		{
			name: "structured output with json_schema on supported model",
			request: &openai.ChatCompletionRequest{
				Model:               "claude-sonnet-4-5-20250514",
				MaxCompletionTokens: ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "test"},
					}},
				},
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: "json_schema",
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Name:   "test_schema",
							Schema: []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`),
						},
					},
				},
			},
			expectSchema: true,
			expectedSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
		},
		{
			name: "structured output skipped on unsupported model",
			request: &openai.ChatCompletionRequest{
				Model:               "claude-3-sonnet",
				MaxCompletionTokens: ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "test"},
					}},
				},
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: "json_schema",
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Name:   "test_schema",
							Schema: []byte(`{"type":"object"}`),
						},
					},
				},
			},
			expectSchema: false,
		},
		{
			name: "no response format",
			request: &openai.ChatCompletionRequest{
				Model:               "claude-sonnet-4-5-20250514",
				MaxCompletionTokens: ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "test"},
					}},
				},
			},
			expectSchema: false,
		},
		{
			name: "invalid json schema returns error",
			request: &openai.ChatCompletionRequest{
				Model:               "claude-sonnet-4-5-20250514",
				MaxCompletionTokens: ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "test"},
					}},
				},
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: "json_schema",
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Name:   "invalid_schema",
							Schema: []byte(`{invalid json`),
						},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := buildAnthropicParams(tt.request, "AWSAnthropic")

			if tt.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, params)

			if tt.expectSchema {
				require.NotNil(t, params.OutputConfig.Format.Schema)
				require.Equal(t, constant.JSONSchema("json_schema"), params.OutputConfig.Format.Type)
				require.Equal(t, tt.expectedSchema, params.OutputConfig.Format.Schema)
			} else {
				require.Nil(t, params.OutputConfig.Format.Schema)
			}
		})
	}
}

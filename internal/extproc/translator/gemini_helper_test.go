// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestOpenAIMessagesToGeminiContents(t *testing.T) {
	tests := []struct {
		name                      string
		messages                  []openai.ChatCompletionMessageParamUnion
		expectedErrorMsg          string
		expectedContents          []genai.Content
		expectedSystemInstruction *genai.Content
	}{
		{
			name: "happy-path",
			messages: []openai.ChatCompletionMessageParamUnion{
				{
					Type: openai.ChatMessageRoleDeveloper,
					Value: openai.ChatCompletionDeveloperMessageParam{
						Role:    openai.ChatMessageRoleDeveloper,
						Content: openai.StringOrArray{Value: "This is a developer message"},
					},
				},
				{
					Type: openai.ChatMessageRoleSystem,
					Value: openai.ChatCompletionSystemMessageParam{
						Role:    openai.ChatMessageRoleSystem,
						Content: openai.StringOrArray{Value: "This is a system message"},
					},
				},
				{
					Type: openai.ChatMessageRoleUser,
					Value: openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "This is a user message"},
					},
				},
				{
					Type: openai.ChatMessageRoleAssistant,
					Value: openai.ChatCompletionAssistantMessageParam{
						Role:    openai.ChatMessageRoleAssistant,
						Audio:   openai.ChatCompletionAssistantMessageParamAudio{},
						Content: openai.StringOrAssistantRoleContentUnion{Value: "This is a assistant message"},
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID: "tool_call_1",
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "example_tool",
									Arguments: "{\"param1\":\"value1\"}",
								},
								Type: openai.ChatCompletionMessageToolCallTypeFunction,
							},
						},
					},
				},
				{
					Type: openai.ChatMessageRoleTool,
					Value: openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_call_1",
						Content:    openai.StringOrArray{Value: "This is a message from the example_tool"},
					},
				},
			},
			expectedContents: []genai.Content{
				{
					Parts: []*genai.Part{
						{Text: "This is a user message"},
					},
					Role: genai.RoleUser,
				},
				{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								Name: "example_tool",
								Args: map[string]any{
									"param1": "value1",
								},
							},
						},
						{Text: "This is a assistant message"},
					},
				},
				{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "example_tool",
								Response: map[string]any{
									"output": "This is a message from the example_tool",
								},
							},
						},
					},
				},
			},
			expectedSystemInstruction: &genai.Content{
				Parts: []*genai.Part{
					{Text: "This is a developer message"},
					{Text: "This is a system message"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contents, systemInstruction, err := openAIMessagesToGeminiContents(tc.messages)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				if d := cmp.Diff(tc.expectedContents, contents); d != "" {
					t.Errorf("Gemini Contents mismatch (-want +got):\n%s", d)
				}
				if d := cmp.Diff(tc.expectedSystemInstruction, systemInstruction); d != "" {
					t.Errorf("SystemInstruction mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

// TestAssistantMsgToGeminiParts tests the assistantMsgToGeminiParts function.
func TestAssistantMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name              string
		msg               openai.ChatCompletionAssistantMessageParam
		expectedParts     []*genai.Part
		expectedToolCalls map[string]string
		expectedErrorMsg  string
	}{
		{
			name: "empty text content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: "",
				},
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
		{
			name: "invalid content type",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: 10, // Invalid type.
				},
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
			expectedErrorMsg:  "unsupported content type in assistant message: int",
		},
		{
			name: "simple text content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: "Hello, I'm an AI assistant",
				},
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromText("Hello, I'm an AI assistant"),
			},
			expectedToolCalls: map[string]string{},
		},
		// Currently noting is returned for refusal messages.
		{
			name: "text content with refusal message",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: []openai.ChatCompletionAssistantMessageParamContent{
						{
							Type:    openai.ChatCompletionAssistantMessageParamContentTypeRefusal,
							Refusal: ptr.To("Response was refused"),
						},
					},
				},
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
		{
			name: "content with an array of texts",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: []openai.ChatCompletionAssistantMessageParamContent{
						{
							Type: openai.ChatCompletionAssistantMessageParamContentTypeText,
							Text: ptr.To("Hello, I'm an AI assistant"),
						},
						{
							Type: openai.ChatCompletionAssistantMessageParamContentTypeText,
							Text: ptr.To("How can I assist you today?"),
						},
					},
				},
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromText("Hello, I'm an AI assistant"),
				genai.NewPartFromText("How can I assist you today?"),
			},
			expectedToolCalls: map[string]string{},
		},
		{
			name: "tool calls without content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: "",
				},
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_123",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York","unit":"celsius"}`,
						},
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedParts: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Args: map[string]any{"location": "New York", "unit": "celsius"},
						Name: "get_weather",
					},
				},
			},
			expectedToolCalls: map[string]string{
				"call_123": "get_weather",
			},
		},
		{
			name: "multiple tool calls with content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Content: openai.StringOrAssistantRoleContentUnion{
					Value: "I'll help you with that",
				},
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_789",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York","unit":"celsius"}`,
						},
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
					},
					{
						ID: "call_abc",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_time",
							Arguments: `{"timezone":"EST"}`,
						},
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedParts: []*genai.Part{
				genai.NewPartFromFunctionCall("get_weather", map[string]any{
					"location": "New York",
					"unit":     "celsius",
				}),
				genai.NewPartFromFunctionCall("get_time", map[string]any{
					"timezone": "EST",
				}),
				genai.NewPartFromText("I'll help you with that"),
			},
			expectedToolCalls: map[string]string{
				"call_789": "get_weather",
				"call_abc": "get_time",
			},
		},
		{
			name: "invalid tool call arguments",
			msg: openai.ChatCompletionAssistantMessageParam{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: "call_def",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York"`, // Invalid JSON.
						},
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
					},
				},
			},
			expectedErrorMsg: "function arguments should be valid json string",
		},
		{
			name: "nil content",
			msg: openai.ChatCompletionAssistantMessageParam{
				Role: openai.ChatMessageRoleAssistant,
			},
			expectedParts:     nil,
			expectedToolCalls: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, toolCalls, err := assistantMsgToGeminiParts(tc.msg)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedParts, parts); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
				if d := cmp.Diff(tc.expectedToolCalls, toolCalls); d != "" {
					t.Errorf("Tools mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestDeveloperMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name             string
		msg              openai.ChatCompletionDeveloperMessageParam
		expectedParts    []*genai.Part
		expectedErrorMsg string
	}{
		{
			name: "string content",
			msg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.StringOrArray{
					Value: "This is a system message",
				},
				Role: openai.ChatMessageRoleSystem,
			},
			expectedParts: []*genai.Part{
				{Text: "This is a system message"},
			},
		},
		{
			name: "content as string array",
			msg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.StringOrArray{
					Value: []openai.ChatCompletionContentPartTextParam{
						{Text: "This is a system message"},
						{Text: "It can be multiline"},
					},
				},
				Role: openai.ChatMessageRoleSystem,
			},
			expectedParts: []*genai.Part{
				{Text: "This is a system message"},
				{Text: "It can be multiline"},
			},
		},
		{
			name: "invalid content type",
			msg: openai.ChatCompletionDeveloperMessageParam{
				Content: openai.StringOrArray{
					Value: 10, // Invalid type.
				},
				Role: openai.ChatMessageRoleSystem,
			},
			expectedParts: []*genai.Part{
				{Text: "This is a system message"},
			},
			expectedErrorMsg: "unsupported content type in developer message: int",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, err := developerMsgToGeminiParts(tc.msg)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedParts, content); d != "" {
					t.Errorf("Content mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestToolMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name             string
		msg              openai.ChatCompletionToolMessageParam
		knownToolCalls   map[string]string
		expectedPart     *genai.Part
		expectedErrorMsg string
	}{
		{
			name: "Tool message with invalid content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.StringOrArray{
					Value: 10, // Invalid type.
				},
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls:   map[string]string{"tool_123": "get_weather"},
			expectedErrorMsg: "unsupported content type in tool message: int",
		},
		{
			name: "Tool message with string content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.StringOrArray{
					Value: "This is a tool message",
				},
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls: map[string]string{"tool_123": "get_weather"},
			expectedPart: &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "get_weather",
					Response: map[string]interface{}{"output": "This is a tool message"},
				},
			},
		},
		{
			name: "Tool message with string array content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.StringOrArray{
					Value: []openai.ChatCompletionContentPartTextParam{
						{
							Type: string(openai.ChatCompletionContentPartTextTypeText),
							Text: "This is a tool message. ",
						},
						{
							Type: string(openai.ChatCompletionContentPartTextTypeText),
							Text: "And this is another part",
						},
					},
				},
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls: map[string]string{"tool_123": "get_weather"},
			expectedPart: &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "get_weather",
					Response: map[string]interface{}{"output": "This is a tool message. And this is another part"},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := toolMsgToGeminiParts(tc.msg, tc.knownToolCalls)

			if tc.expectedErrorMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expectedPart, parts); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

// TestUserMsgToGeminiParts tests the gcpPartsFromUserMsgToGeminiParts function with different inputs.
func TestUserMsgToGeminiParts(t *testing.T) {
	tests := []struct {
		name           string
		msg            openai.ChatCompletionUserMessageParam
		expectedParts  []*genai.Part
		expectedErrMsg string
	}{
		{
			name: "simple string content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: "Hello, how are you?",
				},
			},
			expectedParts: []*genai.Part{
				{Text: "Hello, how are you?"},
			},
		},
		{
			name: "empty string content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: "",
				},
			},
			expectedParts: nil,
		},
		{
			name: "array with multiple text contents",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							TextContent: &openai.ChatCompletionContentPartTextParam{
								Type: string(openai.ChatCompletionContentPartTextTypeText),
								Text: "First message",
							},
						},
						{
							TextContent: &openai.ChatCompletionContentPartTextParam{
								Type: string(openai.ChatCompletionContentPartTextTypeText),
								Text: "Second message",
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{Text: "First message"},
				{Text: "Second message"},
			},
		},
		{
			name: "image content with URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "https://example.com/image.jpg",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{FileData: &genai.FileData{FileURI: "https://example.com/image.jpg", MIMEType: "image/jpeg"}},
			},
		},
		{
			name: "empty image URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "",
								},
							},
						},
					},
				},
			},
			expectedParts: nil,
		},
		{
			name: "invalid image URL",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: ":%invalid-url%:",
								},
							},
						},
					},
				},
			},
			expectedErrMsg: "invalid image URL",
		},
		{
			name: "mixed content - text and image",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							TextContent: &openai.ChatCompletionContentPartTextParam{
								Type: string(openai.ChatCompletionContentPartTextTypeText),
								Text: "Check this image:",
							},
						},
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "https://example.com/image.jpg",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{Text: "Check this image:"},
				{FileData: &genai.FileData{FileURI: "https://example.com/image.jpg", MIMEType: "image/jpeg"}},
			},
		},
		{
			name: "data URI image content",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD3+iiigD//2Q==",
								},
							},
						},
					},
				},
			},
			expectedParts: []*genai.Part{
				{
					InlineData: &genai.Blob{
						Data:     []byte("This field is ignored during testcase comparison"),
						MIMEType: "image/jpeg",
					},
				},
			},
		},
		{
			name: "invalid data URI format",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							ImageContent: &openai.ChatCompletionContentPartImageParam{
								Type: openai.ChatCompletionContentPartImageTypeImageURL,
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:invalid-format",
								},
							},
						},
					},
				},
			},
			expectedErrMsg: "data uri does not have a valid format",
		},
		{
			name: "audio content - not supported",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: []openai.ChatCompletionContentPartUserUnionParam{
						{
							InputAudioContent: &openai.ChatCompletionContentPartInputAudioParam{
								Type: "audio",
							},
						},
					},
				},
			},
			expectedErrMsg: "audio content not supported yet",
		},
		{
			name: "unsupported content type",
			msg: openai.ChatCompletionUserMessageParam{
				Role: openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{
					Value: 42, // not a string or array.
				},
			},
			expectedErrMsg: "unsupported content type in user message: int",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := userMsgToGeminiParts(tc.msg)

			if tc.expectedErrMsg != "" || err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrMsg)
			} else {
				if d := cmp.Diff(tc.expectedParts, parts, cmpopts.IgnoreFields(genai.Blob{}, "Data")); d != "" {
					t.Errorf("Parts mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestOpenAIReqToGeminiGenerationConfig(t *testing.T) {
	tests := []struct {
		name                     string
		input                    *openai.ChatCompletionRequest
		expectedGenerationConfig *genai.GenerationConfig
		expectedErrMsg           string
	}{
		{
			name: "all fields set",
			input: &openai.ChatCompletionRequest{
				Temperature:      ptr.To(0.7),
				TopP:             ptr.To(0.9),
				Seed:             ptr.To(42),
				TopLogProbs:      ptr.To(3),
				LogProbs:         ptr.To(true),
				N:                ptr.To(2),
				MaxTokens:        ptr.To(int64(256)),
				PresencePenalty:  ptr.To(float32(1.1)),
				FrequencyPenalty: ptr.To(float32(0.5)),
				Stop:             []*string{ptr.To("stop1"), ptr.To("stop2")},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				Temperature:      ptr.To(float32(0.7)),
				TopP:             ptr.To(float32(0.9)),
				Seed:             ptr.To(int32(42)),
				Logprobs:         ptr.To(int32(3)),
				ResponseLogprobs: true,
				CandidateCount:   2,
				MaxOutputTokens:  256,
				PresencePenalty:  ptr.To(float32(1.1)),
				FrequencyPenalty: ptr.To(float32(0.5)),
				StopSequences:    []string{"stop1", "stop2"},
			},
		},
		{
			name:                     "minimal fields",
			input:                    &openai.ChatCompletionRequest{},
			expectedGenerationConfig: &genai.GenerationConfig{},
		},
		{
			name: "text",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeText,
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "text/plain"},
		},
		{
			name: "json object",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONObject,
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "application/json"},
		},
		{
			name: "json schema (map)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
					JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Schema: map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
		},
		{
			name: "json schema (string)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
					JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Schema: `{"type":"string"}`,
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
		},
		{
			name: "json schema (invalid string)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
					JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Schema: `{"type":`, // invalid JSON.
					},
				},
			},
			expectedErrMsg: "invalid JSON schema string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openAIReqToGeminiGenerationConfig(tc.input)
			if tc.expectedErrMsg != "" && err == nil {
				t.Errorf("expected error but got nil. Expected: %s", tc.expectedErrMsg)
				return
			}
			if tc.expectedErrMsg == "" && err != nil {
				t.Errorf("unexpected error. Error: %s", err.Error())
				return
			}
			if tc.expectedErrMsg != "" && err != nil && !strings.Contains(err.Error(), tc.expectedErrMsg) {
				t.Errorf("expected error message %q but got %q", tc.expectedErrMsg, err.Error())
				return
			}

			if diff := cmp.Diff(tc.expectedGenerationConfig, got, cmpopts.IgnoreUnexported(genai.GenerationConfig{})); diff != "" {
				t.Errorf("GenerationConfig mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToolsToGeminiTools(t *testing.T) {
	funcParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "integer"},
			"b": map[string]any{"type": "integer"},
		},
		"required": []any{"a", "b"},
	}
	tests := []struct {
		name          string
		openaiTools   []openai.Tool
		expected      []genai.Tool
		expectedError string
	}{
		{
			name:        "empty tools",
			openaiTools: nil,
			expected:    nil,
		},
		{
			name: "single function tool with parameters",
			openaiTools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "add",
						Description: "Add two numbers",
						Parameters:  funcParams,
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:                 "add",
							Description:          "Add two numbers",
							ParametersJsonSchema: funcParams,
						},
					},
				},
			},
		},
		{
			name: "multiple function tools",
			openaiTools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "foo",
						Description: "Foo function",
					},
				},
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "bar",
						Description: "Bar function",
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:                 "foo",
							Description:          "Foo function",
							Parameters:           nil,
							ParametersJsonSchema: map[string]any(nil),
						},
						{
							Name:                 "bar",
							Description:          "Bar function",
							Parameters:           nil,
							ParametersJsonSchema: map[string]any(nil),
						},
					},
				},
			},
		},
		{
			name: "tool with invalid parameters schema",
			openaiTools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "bad",
						Description: "Bad function",
						Parameters:  "not-a-map",
					},
				},
			},
			expectedError: "tool's param should be a valid JSON string. invalid JSON schema string provided for tool 'bad'",
		},
		{
			name: "non-function tool is ignored",
			openaiTools: []openai.Tool{
				{
					Type: "retrieval",
				},
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := openAIToolsToGeminiTools(tc.openaiTools)
			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				require.NoError(t, err)
				if d := cmp.Diff(tc.expected, result, cmpopts.IgnoreUnexported(genai.Schema{})); d != "" {
					t.Errorf("Result mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestOpenAIToolChoiceToGeminiToolConfig(t *testing.T) {
	tests := []struct {
		name      string
		input     interface{}
		expected  *genai.ToolConfig
		expectErr string
	}{
		{
			name:     "string auto",
			input:    "auto",
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}},
		},
		{
			name:     "string none",
			input:    "none",
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeNone}},
		},
		{
			name:     "string required",
			input:    "required",
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}},
		},
		{
			name: "ToolChoice struct",
			input: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "myfunc"},
			},
			expected: &genai.ToolConfig{
				FunctionCallingConfig: &genai.FunctionCallingConfig{
					Mode:                 genai.FunctionCallingConfigModeAny,
					AllowedFunctionNames: []string{"myfunc"},
				},
				RetrievalConfig: nil,
			},
		},
		{
			name:      "unsupported type",
			input:     123,
			expectErr: "unsupported tool choice type",
		},
		{
			name:      "unsupported string value",
			input:     "invalid",
			expectErr: "unsupported tool choice: 'invalid'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openAIToolChoiceToGeminiToolConfig(tc.input)
			if tc.expectErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectErr) {
					t.Errorf("expected error %q, got %v", tc.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.expected, got, cmpopts.IgnoreUnexported(genai.ToolConfig{}, genai.FunctionCallingConfig{})); diff != "" {
				t.Errorf("ToolConfig mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

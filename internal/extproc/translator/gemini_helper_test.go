// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	openaigo "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
					OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
						Role:    openai.ChatMessageRoleDeveloper,
						Content: openai.ContentUnion{Value: "This is a developer message"},
					},
				},
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role:    openai.ChatMessageRoleSystem,
						Content: openai.ContentUnion{Value: "This is a system message"},
					},
				},
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{Value: "This is a user message"},
					},
				},
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role:    openai.ChatMessageRoleAssistant,
						Audio:   openai.ChatCompletionAssistantMessageParamAudio{},
						Content: openai.StringOrAssistantRoleContentUnion{Value: "This is a assistant message"},
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID: ptr.To("tool_call_1"),
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
					OfTool: &openai.ChatCompletionToolMessageParam{
						ToolCallID: "tool_call_1",
						Content:    openai.ContentUnion{Value: "This is a message from the example_tool"},
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
						ID: ptr.To("call_123"),
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
						ID: ptr.To("call_789"),
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      "get_weather",
							Arguments: `{"location":"New York","unit":"celsius"}`,
						},
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
					},
					{
						ID: ptr.To("call_abc"),
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
						ID: ptr.To("call_def"),
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
				Content: openai.ContentUnion{
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
				Content: openai.ContentUnion{
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
				Content: openai.ContentUnion{
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
				Content: openai.ContentUnion{
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
				Content: openai.ContentUnion{
					Value: "This is a tool message",
				},
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "tool_123",
			},
			knownToolCalls: map[string]string{"tool_123": "get_weather"},
			expectedPart: &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "get_weather",
					Response: map[string]any{"output": "This is a tool message"},
				},
			},
		},
		{
			name: "Tool message with string array content",
			msg: openai.ChatCompletionToolMessageParam{
				Content: openai.ContentUnion{
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
					Response: map[string]any{"output": "This is a tool message. And this is another part"},
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
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: string(openai.ChatCompletionContentPartTextTypeText),
								Text: "First message",
							},
						},
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
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
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfText: &openai.ChatCompletionContentPartTextParam{
								Type: string(openai.ChatCompletionContentPartTextTypeText),
								Text: "Check this image:",
							},
						},
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
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
							OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
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
		expectedResponseMode     geminiResponseMode
		expectedErrMsg           string
		requestModel             internalapi.RequestModel
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
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"stop1", "stop2"},
				},
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
			expectedResponseMode: responseModeNone,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name:                     "minimal fields",
			input:                    &openai.ChatCompletionRequest{},
			expectedGenerationConfig: &genai.GenerationConfig{},
			expectedResponseMode:     responseModeNone,
		},
		{
			name: "stop sequences",
			input: &openai.ChatCompletionRequest{
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfString: openaigo.Opt[string]("stop1"),
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				StopSequences: []string{"stop1"},
			},
			expectedResponseMode: responseModeNone,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name: "text",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfText: &openai.ChatCompletionResponseFormatTextParam{
						Type: openai.ChatCompletionResponseFormatTypeText,
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "text/plain"},
			expectedResponseMode:     responseModeText,
			requestModel:             "gemini-2.5-flash",
		},
		{
			name: "json object",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONObject: &openai.ChatCompletionResponseFormatJSONObjectParam{
						Type: openai.ChatCompletionResponseFormatTypeJSONObject,
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{ResponseMIMEType: "application/json"},
			expectedResponseMode:     responseModeJSON,
			requestModel:             "gemini-2.5-flash",
		},
		{
			name: "json schema (map)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{"type": "string"}`),
						},
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
			expectedResponseMode: responseModeJSON,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name: "json schema (string)",
			input: &openai.ChatCompletionRequest{
				Model: "gemini-2.5",
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{"type":"string"}`),
						},
					},
				},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: map[string]any{"type": "string"},
			},
			expectedResponseMode: responseModeJSON,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name: "json schema (invalid string)",
			input: &openai.ChatCompletionRequest{
				Model: "gemini-2.5",
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{"type":`), // invalid JSON.
						},
					},
				},
			},
			expectedErrMsg: "invalid JSON schema",
			requestModel:   "gemini-2.5-flash",
		},
		{
			name: "guided choice",
			input: &openai.ChatCompletionRequest{
				GuidedChoice: []string{"Positive", "Negative"},
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "text/x.enum",
				ResponseSchema:   &genai.Schema{Type: "STRING", Enum: []string{"Positive", "Negative"}},
			},
			expectedResponseMode: responseModeEnum,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name: "guided regex",
			input: &openai.ChatCompletionRequest{
				GuidedRegex: "\\w+@\\w+\\.com\\n",
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema:   &genai.Schema{Type: "STRING", Pattern: "\\w+@\\w+\\.com\\n"},
			},
			expectedResponseMode: responseModeRegex,
			requestModel:         "gemini-2.5-flash",
		},
		{
			name: "guided json",
			input: &openai.ChatCompletionRequest{
				GuidedJSON: json.RawMessage(`{"type": "string"}`),
			},
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType:   "application/json",
				ResponseJsonSchema: json.RawMessage(`{"type": "string"}`),
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "multiple format specifiers - ResponseFormat and GuidedChoice",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfText: &openai.ChatCompletionResponseFormatTextParam{
						Type: openai.ChatCompletionResponseFormatTypeText,
					},
				},
				GuidedChoice: []string{"A", "B"},
			},
			expectedErrMsg: "multiple format specifiers specified",
			requestModel:   "gemini-2.5-flash",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, responseMode, err := openAIReqToGeminiGenerationConfig(tc.input, tc.requestModel)
			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)

				if diff := cmp.Diff(tc.expectedGenerationConfig, got, cmpopts.IgnoreUnexported(genai.GenerationConfig{})); diff != "" {
					t.Errorf("GenerationConfig mismatch (-want +got):\n%s", diff)
				}

				if responseMode != tc.expectedResponseMode {
					t.Errorf("geminiResponseMode mismatch: got %v, want %v", responseMode, tc.expectedResponseMode)
				}
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
							ParametersJsonSchema: nil,
						},
						{
							Name:                 "bar",
							Description:          "Bar function",
							Parameters:           nil,
							ParametersJsonSchema: nil,
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
						Parameters:  "invalid-json",
					},
				},
			},
			expected: []genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Description:          "Bad function",
							Name:                 "bad",
							ParametersJsonSchema: "invalid-json",
						},
					},
				},
			},
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
				require.ErrorContains(t, err, tc.expectedError)
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
		input     *openai.ChatCompletionToolChoiceUnion
		expected  *genai.ToolConfig
		expectErr string
	}{
		{
			name:     "string auto",
			input:    &openai.ChatCompletionToolChoiceUnion{Value: "auto"},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}},
		},
		{
			name:     "string none",
			input:    &openai.ChatCompletionToolChoiceUnion{Value: "none"},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeNone}},
		},
		{
			name:     "string required",
			input:    &openai.ChatCompletionToolChoiceUnion{Value: "required"},
			expected: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}},
		},
		{
			name: "ToolChoice struct",
			input: &openai.ChatCompletionToolChoiceUnion{
				Value: openai.ChatCompletionNamedToolChoice{
					Type:     openai.ToolTypeFunction,
					Function: openai.ChatCompletionNamedToolChoiceFunction{Name: "myfunc"},
				},
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
			input:     &openai.ChatCompletionToolChoiceUnion{Value: 123},
			expectErr: "unsupported tool choice type",
		},
		{
			name:      "unsupported string value",
			input:     &openai.ChatCompletionToolChoiceUnion{Value: "invalid"},
			expectErr: "unsupported tool choice: 'invalid'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openAIToolChoiceToGeminiToolConfig(tc.input)
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
			} else {
				require.NoError(t, err)
				if diff := cmp.Diff(tc.expected, got, cmpopts.IgnoreUnexported(genai.ToolConfig{}, genai.FunctionCallingConfig{})); diff != "" {
					t.Errorf("ToolConfig mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestGeminiLogprobsToOpenAILogprobs(t *testing.T) {
	tests := []struct {
		name     string
		input    genai.LogprobsResult
		expected openai.ChatCompletionChoicesLogprobs
	}{
		{
			name:     "empty logprobs result",
			input:    genai.LogprobsResult{},
			expected: openai.ChatCompletionChoicesLogprobs{},
		},
		{
			name: "single chosen candidate without top candidates",
			input: genai.LogprobsResult{
				ChosenCandidates: []*genai.LogprobsResultCandidate{
					{
						Token:          "hello",
						LogProbability: -0.5,
					},
				},
			},
			expected: openai.ChatCompletionChoicesLogprobs{
				Content: []openai.ChatCompletionTokenLogprob{
					{
						Token:       "hello",
						Logprob:     -0.5,
						TopLogprobs: nil,
					},
				},
			},
		},
		{
			name: "multiple chosen candidates with top candidates",
			input: genai.LogprobsResult{
				ChosenCandidates: []*genai.LogprobsResultCandidate{
					{
						Token:          "hello",
						LogProbability: -0.5,
					},
					{
						Token:          "world",
						LogProbability: -0.3,
					},
				},
				TopCandidates: []*genai.LogprobsResultTopCandidates{
					{
						Candidates: []*genai.LogprobsResultCandidate{
							{Token: "hello", LogProbability: -0.5},
							{Token: "hi", LogProbability: -1.2},
							{Token: "hey", LogProbability: -1.5},
						},
					},
					{
						Candidates: []*genai.LogprobsResultCandidate{
							{Token: "world", LogProbability: -0.3},
							{Token: "earth", LogProbability: -1.1},
						},
					},
				},
			},
			expected: openai.ChatCompletionChoicesLogprobs{
				Content: []openai.ChatCompletionTokenLogprob{
					{
						Token:   "hello",
						Logprob: -0.5,
						TopLogprobs: []openai.ChatCompletionTokenLogprobTopLogprob{
							{Token: "hello", Logprob: -0.5},
							{Token: "hi", Logprob: -1.2},
							{Token: "hey", Logprob: -1.5},
						},
					},
					{
						Token:   "world",
						Logprob: -0.3,
						TopLogprobs: []openai.ChatCompletionTokenLogprobTopLogprob{
							{Token: "world", Logprob: -0.3},
							{Token: "earth", Logprob: -1.1},
						},
					},
				},
			},
		},
		{
			name: "chosen candidates with nil top candidates entry",
			input: genai.LogprobsResult{
				ChosenCandidates: []*genai.LogprobsResultCandidate{
					{
						Token:          "test",
						LogProbability: -0.8,
					},
				},
				TopCandidates: []*genai.LogprobsResultTopCandidates{
					nil,
				},
			},
			expected: openai.ChatCompletionChoicesLogprobs{
				Content: []openai.ChatCompletionTokenLogprob{
					{
						Token:       "test",
						Logprob:     -0.8,
						TopLogprobs: nil,
					},
				},
			},
		},
		{
			name: "chosen candidates with empty top candidates",
			input: genai.LogprobsResult{
				ChosenCandidates: []*genai.LogprobsResultCandidate{
					{
						Token:          "empty",
						LogProbability: -0.2,
					},
				},
				TopCandidates: []*genai.LogprobsResultTopCandidates{
					{
						Candidates: []*genai.LogprobsResultCandidate{},
					},
				},
			},
			expected: openai.ChatCompletionChoicesLogprobs{
				Content: []openai.ChatCompletionTokenLogprob{
					{
						Token:       "empty",
						Logprob:     -0.2,
						TopLogprobs: nil,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := geminiLogprobsToOpenAILogprobs(tt.input)
			// Use cmp.Equal with EquateApprox for float comparison due to float32->float64 conversion.
			if !cmp.Equal(tt.expected, result, cmpopts.EquateApprox(0, 0.0001)) {
				t.Errorf("geminiLogprobsToOpenAILogprobs() diff:\n%s", cmp.Diff(tt.expected, result, cmpopts.EquateApprox(0, 0.0001)))
			}
		})
	}
}

func TestExtractToolCallsFromGeminiParts(t *testing.T) {
	toolCalls := []openai.ChatCompletionMessageToolCallParam{}
	tests := []struct {
		name     string
		input    []*genai.Part
		expected []openai.ChatCompletionMessageToolCallParam
		wantErr  bool
	}{
		{
			name:     "nil parts",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty parts",
			input:    []*genai.Part{},
			expected: nil,
		},
		{
			name: "parts without function calls",
			input: []*genai.Part{
				{Text: "some text"},
				nil,
				{Text: "more text"},
			},
			expected: nil,
		},
		{
			name: "single function call",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "get_weather",
						Args: map[string]any{
							"location": "San Francisco",
							"unit":     "celsius",
						},
					},
				},
			},
			expected: []openai.ChatCompletionMessageToolCallParam{
				{
					ID:   ptr.To("0"),
					Type: openai.ChatCompletionMessageToolCallTypeFunction,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      "get_weather",
						Arguments: `{"location":"San Francisco","unit":"celsius"}`,
					},
				},
			},
		},
		{
			name: "multiple function calls",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "function1",
						Args: map[string]any{"param1": "value1"},
					},
				},
				{Text: "some text between"},
				{
					FunctionCall: &genai.FunctionCall{
						Name: "function2",
						Args: map[string]any{"param2": float64(42)},
					},
				},
			},
			expected: []openai.ChatCompletionMessageToolCallParam{
				{
					ID:   ptr.To("0"),
					Type: openai.ChatCompletionMessageToolCallTypeFunction,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      "function1",
						Arguments: `{"param1":"value1"}`,
					},
				},
				{
					ID:   ptr.To("1"),
					Type: openai.ChatCompletionMessageToolCallTypeFunction,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      "function2",
						Arguments: `{"param2":42}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, err := extractToolCallsFromGeminiParts(toolCalls, tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Normalize IDs since they're generated.
			for i := range calls {
				calls[i].ID = ptr.To(fmt.Sprintf("%d", i))
			}

			require.Equal(t, tt.expected, calls)
		})
	}
}

func TestGeminiFinishReasonToOpenAI(t *testing.T) {
	tests := []struct {
		name      string
		input     genai.FinishReason
		toolCalls []openai.ChatCompletionMessageToolCallParam
		expected  openai.ChatCompletionChoicesFinishReason
	}{
		{
			name:      "stop reason",
			input:     genai.FinishReasonStop,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonStop,
		},
		{
			name:  "tool calls reason",
			input: genai.FinishReasonStop,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{
				{
					ID: ptr.To("tool_call_1"),
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      "example_tool",
						Arguments: "{\"param1\":\"value1\"}",
					},
					Type: openai.ChatCompletionMessageToolCallTypeFunction,
				},
			},
			expected: openai.ChatCompletionChoicesFinishReasonToolCalls,
		},
		{
			name:      "max tokens reason",
			input:     genai.FinishReasonMaxTokens,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonLength,
		},
		{
			name:      "empty reason for streaming",
			input:     "",
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  "",
		},
		{
			name:      "safety reason",
			input:     genai.FinishReasonSafety,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonContentFilter,
		},
		{
			name:      "recitation reason",
			input:     genai.FinishReasonRecitation,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonContentFilter,
		},
		{
			name:      "other reason",
			input:     genai.FinishReasonOther,
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonContentFilter,
		},
		{
			name:      "unknown reason",
			input:     genai.FinishReason("unknown_reason"),
			toolCalls: []openai.ChatCompletionMessageToolCallParam{},
			expected:  openai.ChatCompletionChoicesFinishReasonContentFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := geminiFinishReasonToOpenAI(tt.input, tt.toolCalls)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractToolCallsFromGeminiPartsStream(t *testing.T) {
	toolCalls := []openai.ChatCompletionChunkChoiceDeltaToolCall{}
	tests := []struct {
		name     string
		input    []*genai.Part
		expected func([]openai.ChatCompletionChunkChoiceDeltaToolCall) bool // validator function since UUIDs are random
		wantErr  bool
		errMsg   string
	}{
		{
			name:  "nil parts",
			input: nil,
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				return calls == nil
			},
		},
		{
			name:  "empty parts",
			input: []*genai.Part{},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				return calls == nil
			},
		},
		{
			name: "parts without function calls",
			input: []*genai.Part{
				{Text: "some text"},
				nil,
				{Text: "more text"},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				return calls == nil
			},
		},
		{
			name: "single function call",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "get_weather",
						Args: map[string]any{
							"location": "San Francisco",
							"unit":     "celsius",
						},
					},
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 1 {
					return false
				}
				call := calls[0]
				return call.ID != nil && *call.ID == "call_get_weather_0" && // Deterministic ID based on function name and index
					call.Type == openai.ChatCompletionMessageToolCallTypeFunction &&
					call.Function.Name == "get_weather" &&
					call.Function.Arguments == `{"location":"San Francisco","unit":"celsius"}` &&
					call.Index == 0 // First tool call should have index 0
			},
		},
		{
			name: "multiple function calls",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "function1",
						Args: map[string]any{"param1": "value1"},
					},
				},
				{Text: "some text between"},
				{
					FunctionCall: &genai.FunctionCall{
						Name: "function2",
						Args: map[string]any{"param2": float64(42)},
					},
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 2 {
					return false
				}
				// Verify first call
				call1 := calls[0]
				if call1.ID == nil || *call1.ID != "call_function1_0" ||
					call1.Type != openai.ChatCompletionMessageToolCallTypeFunction ||
					call1.Function.Name != "function1" ||
					call1.Function.Arguments != `{"param1":"value1"}` ||
					call1.Index != 0 { // First tool call should have index 0
					return false
				}
				// Verify second call
				call2 := calls[1]
				if call2.ID == nil || *call2.ID != "call_function2_1" ||
					call2.Type != openai.ChatCompletionMessageToolCallTypeFunction ||
					call2.Function.Name != "function2" ||
					call2.Function.Arguments != `{"param2":42}` ||
					call2.Index != 1 { // Second tool call should have index 1
					return false
				}
				// Verify IDs are different (deterministic IDs should be unique)
				return *call1.ID != *call2.ID
			},
		},
		{
			name: "function call with nil part",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "test_func",
						Args: map[string]any{"test": "value"},
					},
				},
				nil, // nil part should be skipped
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 1 {
					return false
				}
				call := calls[0]
				return call.ID != nil && *call.ID == "call_test_func_0" &&
					call.Function.Name == "test_func" &&
					call.Index == 0 // Single tool call should have index 0
			},
		},
		{
			name: "function call with empty args",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "no_args_func",
						Args: map[string]any{},
					},
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 1 {
					return false
				}
				call := calls[0]
				return call.ID != nil && *call.ID == "call_no_args_func_0" &&
					call.Function.Name == "no_args_func" &&
					call.Function.Arguments == `{}` &&
					call.Index == 0 // Single tool call should have index 0
			},
		},
		{
			name: "function call with nil args",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "nil_args_func",
						Args: nil,
					},
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 1 {
					return false
				}
				call := calls[0]
				return call.ID != nil && *call.ID == "call_nil_args_func_0" &&
					call.Function.Name == "nil_args_func" &&
					call.Function.Arguments == `null` &&
					call.Index == 0 // Single tool call should have index 0
			},
		},
		{
			name: "function call with complex nested args",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "complex_func",
						Args: map[string]any{
							"user": map[string]any{
								"name": "John",
								"age":  30,
							},
							"items": []any{
								map[string]any{"id": 1, "name": "item1"},
								map[string]any{"id": 2, "name": "item2"},
							},
							"active": true,
						},
					},
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				if len(calls) != 1 {
					return false
				}
				call := calls[0]
				// Parse the JSON to verify structure since order might vary
				var args map[string]any
				if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
					return false
				}
				user, ok := args["user"].(map[string]any)
				if !ok || user["name"] != "John" || user["age"] != float64(30) {
					return false
				}
				items, ok := args["items"].([]any)
				if !ok || len(items) != 2 {
					return false
				}
				active, ok := args["active"].(bool)
				if !ok || !active {
					return false
				}
				return call.ID != nil && *call.ID == "call_complex_func_0" &&
					call.Function.Name == "complex_func" &&
					call.Index == 0 // Single tool call should have index 0
			},
		},
		{
			name: "part with nil function call",
			input: []*genai.Part{
				{
					FunctionCall: nil,
					Text:         "some text",
				},
			},
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				return calls == nil
			},
		},
		{
			name: "function call with unmarshalable args",
			input: []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						Name: "test_func",
						Args: map[string]any{
							"channel": make(chan int), // channels cannot be marshaled to JSON
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "failed to marshal function arguments",
			expected: func(calls []openai.ChatCompletionChunkChoiceDeltaToolCall) bool {
				return calls == nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, err := extractToolCallsFromGeminiPartsStream(toolCalls, tt.input)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}

			require.NoError(t, err)

			if !tt.expected(calls) {
				t.Errorf("extractToolCallsFromGeminiPartsStream() result validation failed. Got: %+v", calls)
			}
		})
	}
}

// TestExtractToolCallsStreamVsNonStream tests the differences between streaming and non-streaming extraction
func TestExtractToolCallsStreamVsNonStream(t *testing.T) {
	toolCalls := []openai.ChatCompletionMessageToolCallParam{}
	toolCallsStream := []openai.ChatCompletionChunkChoiceDeltaToolCall{}
	parts := []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				Name: "test_function",
				Args: map[string]any{
					"param1": "value1",
					"param2": 42,
				},
			},
		},
	}

	// Get results from both functions
	streamCalls, err := extractToolCallsFromGeminiPartsStream(toolCallsStream, parts)
	require.NoError(t, err)
	require.Len(t, streamCalls, 1)

	nonStreamCalls, err := extractToolCallsFromGeminiParts(toolCalls, parts)
	require.NoError(t, err)
	require.Len(t, nonStreamCalls, 1)

	streamCall := streamCalls[0]
	nonStreamCall := nonStreamCalls[0]

	// Verify function name and arguments are the same
	assert.Equal(t, nonStreamCall.Function.Name, streamCall.Function.Name)
	assert.Equal(t, nonStreamCall.Function.Arguments, streamCall.Function.Arguments)
	assert.Equal(t, openai.ChatCompletionMessageToolCallTypeFunction, streamCall.Type)

	// Verify differences:
	// 1. Stream version should have Index field set to 0 for the first tool call
	assert.Equal(t, int64(0), streamCall.Index)

	// 2. Stream version should have a deterministic ID based on function name and index
	assert.NotNil(t, streamCall.ID)
	assert.NotEmpty(t, *streamCall.ID)
	// Deterministic ID should follow pattern "call_<functionName>_<index>"
	assert.Equal(t, "call_get_weather_0", *streamCall.ID, "Stream ID should be deterministic")

	// 3. Non-stream version should have a UUID as well
	assert.NotNil(t, nonStreamCall.ID)
	assert.NotEmpty(t, *nonStreamCall.ID)

	// 4. IDs should be different between the two calls (stream uses deterministic, non-stream uses UUID)
	assert.NotEqual(t, *streamCall.ID, *nonStreamCall.ID)

	// Type checking: ensure we get the right types back
	assert.IsType(t, []openai.ChatCompletionChunkChoiceDeltaToolCall{}, streamCalls)
	assert.IsType(t, []openai.ChatCompletionMessageToolCallParam{}, nonStreamCalls)
}

// TestExtractToolCallsStreamIndexing specifically tests that multiple tool calls get correct indices
func TestExtractToolCallsStreamIndexing(t *testing.T) {
	toolCalls := []openai.ChatCompletionChunkChoiceDeltaToolCall{}
	parts := []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				Name: "first_function",
				Args: map[string]any{"param": "value1"},
			},
		},
		{Text: "some text"}, // non-function part should be skipped
		{
			FunctionCall: &genai.FunctionCall{
				Name: "second_function",
				Args: map[string]any{"param": "value2"},
			},
		},
		{
			FunctionCall: &genai.FunctionCall{
				Name: "third_function",
				Args: map[string]any{"param": "value3"},
			},
		},
	}

	calls, err := extractToolCallsFromGeminiPartsStream(toolCalls, parts)
	require.NoError(t, err)
	require.Len(t, calls, 3)

	// Verify each tool call has the correct index
	for i, call := range calls {
		assert.Equal(t, int64(i), call.Index, "Tool call %d should have index %d", i, i)
		assert.NotNil(t, call.ID)
		assert.NotEmpty(t, *call.ID)
		assert.Equal(t, openai.ChatCompletionMessageToolCallTypeFunction, call.Type)
	}

	// Verify specific function names and arguments
	assert.Equal(t, "first_function", calls[0].Function.Name)
	assert.JSONEq(t, `{"param":"value1"}`, calls[0].Function.Arguments)

	assert.Equal(t, "second_function", calls[1].Function.Name)
	assert.JSONEq(t, `{"param":"value2"}`, calls[1].Function.Arguments)

	assert.Equal(t, "third_function", calls[2].Function.Name)
	assert.JSONEq(t, `{"param":"value3"}`, calls[2].Function.Arguments)

	// Verify all IDs are unique
	ids := make(map[string]bool)
	for _, call := range calls {
		assert.False(t, ids[*call.ID], "Tool call ID should be unique: %s", *call.ID)
		ids[*call.ID] = true
	}
}

func TestExtractTextFromGeminiParts(t *testing.T) {
	tests := []struct {
		name         string
		parts        []*genai.Part
		responseMode geminiResponseMode
		expected     string
	}{
		{
			name:         "nil parts",
			parts:        nil,
			responseMode: responseModeNone,
			expected:     "",
		},
		{
			name:         "empty parts",
			parts:        []*genai.Part{},
			responseMode: responseModeNone,
			expected:     "",
		},
		{
			name: "multiple text parts without regex mode",
			parts: []*genai.Part{
				{Text: "Hello, "},
				{Text: "world!"},
			},
			responseMode: responseModeJSON,
			expected:     "Hello, world!",
		},
		{
			name: "regex mode with mixed quoted and unquoted text",
			parts: []*genai.Part{
				{Text: `"positive"`},
				{Text: `unquoted`},
				{Text: `"negative"`},
			},
			responseMode: responseModeRegex,
			expected:     "positiveunquotednegative",
		},
		{
			name: "regex mode with only double-quoted first and last words",
			parts: []*genai.Part{
				{Text: "\"\"ERROR\" Unable to connect to database \"DatabaseModule\"\""},
			},
			responseMode: responseModeRegex,
			expected:     "\"ERROR\" Unable to connect to database \"DatabaseModule\"",
		},
		{
			name: "non-regex mode with double-quoted text (should not remove quotes)",
			parts: []*genai.Part{
				{Text: `"positive"`},
			},
			responseMode: responseModeJSON,
			expected:     `"positive"`,
		},
		{
			name: "regex mode with text containing internal quotes",
			parts: []*genai.Part{
				{Text: `"He said \"hello\" to me"`},
			},
			responseMode: responseModeRegex,
			expected:     `He said \"hello\" to me`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractTextFromGeminiParts(tc.parts, tc.responseMode)
			if result != tc.expected {
				t.Errorf("extractTextFromGeminiParts() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestOpenAIReqToGeminiGenerationConfigWithJsonSchemaToGemini(t *testing.T) {
	tests := []struct {
		name                     string
		input                    *openai.ChatCompletionRequest
		requestModel             internalapi.RequestModel
		expectedGenerationConfig *genai.GenerationConfig
		expectedResponseMode     geminiResponseMode
		expectedErrMsg           string
	}{
		{
			name: "json schema for older gemini model (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"name": {"type": "string"},
									"age": {"type": "number"}
								},
								"required": ["name", "age"]
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash", // older model that doesn't support native JSON schema
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"name": {Type: "string"},
						"age":  {Type: "number"},
					},
					Required: []string{"name", "age"},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "complex json schema with nested objects (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"user": {
										"type": "object",
										"properties": {
											"name": {"type": "string"},
											"contact": {
												"type": "object",
												"properties": {
													"email": {"type": "string"},
													"phone": {"type": "string"}
												}
											}
										}
									},
									"items": {
										"type": "array",
										"items": {
											"type": "object",
											"properties": {
												"id": {"type": "number"},
												"description": {"type": "string"}
											}
										}
									}
								},
								"required": ["user", "items"]
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash", // older model
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"user": {
							Type: "object",
							Properties: map[string]*genai.Schema{
								"name": {Type: "string"},
								"contact": {
									Type: "object",
									Properties: map[string]*genai.Schema{
										"email": {Type: "string"},
										"phone": {Type: "string"},
									},
								},
							},
						},
						"items": {
							Type: "array",
							Items: &genai.Schema{
								Type: "object",
								Properties: map[string]*genai.Schema{
									"id":          {Type: "number"},
									"description": {Type: "string"},
								},
							},
						},
					},
					Required: []string{"user", "items"},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "json schema with anyOf (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"value": {
										"anyOf": [
											{"type": "string"},
											{"type": "number"}
										]
									}
								}
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash",
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"value": {
							AnyOf: []*genai.Schema{
								{Type: "string"},
								{Type: "number"},
							},
						},
					},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "json schema with nullable type (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"optional_field": {
										"anyOf": [
											{"type": "string"},
											{"type": "null"}
										]
									}
								}
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash",
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"optional_field": {
							AnyOf: []*genai.Schema{
								{Type: "string"},
							},
							Nullable: ptr.To(true),
						},
					},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "json schema with $ref (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"user": {"$ref": "#/$defs/User"}
								},
								"$defs": {
									"User": {
										"type": "object",
										"properties": {
											"name": {"type": "string"},
											"age": {"type": "number"}
										}
									}
								}
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash",
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"user": {
							Type: "object",
							Properties: map[string]*genai.Schema{
								"name": {Type: "string"},
								"age":  {Type: "number"},
							},
						},
					},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
		{
			name: "invalid json schema causes jsonSchemaToGemini error",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"invalid_ref": {"$ref": "#/nonexistent/path"}
								}
							}`),
						},
					},
				},
			},
			requestModel:   "gemini-2.0-flash",
			expectedErrMsg: "invalid JSON schema",
		},
		{
			name: "complex nested schema with arrays and refs (uses jsonSchemaToGemini)",
			input: &openai.ChatCompletionRequest{
				ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
					OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Schema: json.RawMessage(`{
								"type": "object",
								"properties": {
									"steps": {
										"type": "array",
										"items": {"$ref": "#/$defs/Step"}
									},
									"final_answer": {"type": "string"}
								},
								"required": ["steps", "final_answer"],
								"$defs": {
									"Step": {
										"type": "object",
										"properties": {
											"explanation": {"type": "string"},
											"output": {"type": "string"}
										},
										"required": ["explanation", "output"]
									}
								}
							}`),
						},
					},
				},
			},
			requestModel: "gemini-2.0-flash",
			expectedGenerationConfig: &genai.GenerationConfig{
				ResponseMIMEType: "application/json",
				ResponseSchema: &genai.Schema{
					Type: "object",
					Properties: map[string]*genai.Schema{
						"steps": {
							Type: "array",
							Items: &genai.Schema{
								Type: "object",
								Properties: map[string]*genai.Schema{
									"explanation": {Type: "string"},
									"output":      {Type: "string"},
								},
								Required: []string{"explanation", "output"},
							},
						},
						"final_answer": {Type: "string"},
					},
					Required: []string{"steps", "final_answer"},
				},
			},
			expectedResponseMode: responseModeJSON,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, responseMode, err := openAIReqToGeminiGenerationConfig(tc.input, tc.requestModel)
			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)

				if diff := cmp.Diff(tc.expectedGenerationConfig, got, cmpopts.IgnoreUnexported(genai.GenerationConfig{}, genai.Schema{})); diff != "" {
					t.Errorf("GenerationConfig mismatch (-want +got):\n%s", diff)
				}

				if responseMode != tc.expectedResponseMode {
					t.Errorf("geminiResponseMode mismatch: got %v, want %v", responseMode, tc.expectedResponseMode)
				}
			}
		})
	}
}

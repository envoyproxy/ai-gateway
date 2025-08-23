// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func TestOpenAIChatCompletionContentPartUserUnionParamUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     []byte
		out    *ChatCompletionContentPartUserUnionParam
		expErr string
	}{
		{
			name: "text",
			in: []byte(`{
"type": "text",
"text": "what do you see in this image"
}`),
			out: &ChatCompletionContentPartUserUnionParam{
				TextContent: &ChatCompletionContentPartTextParam{
					Type: string(ChatCompletionContentPartTextTypeText),
					Text: "what do you see in this image",
				},
			},
		},
		{
			name: "image url",
			in: []byte(`{
"type": "image_url",
"image_url": {"url": "https://example.com/image.jpg"}
}`),
			out: &ChatCompletionContentPartUserUnionParam{
				ImageContent: &ChatCompletionContentPartImageParam{
					Type: ChatCompletionContentPartImageTypeImageURL,
					ImageURL: ChatCompletionContentPartImageImageURLParam{
						URL: "https://example.com/image.jpg",
					},
				},
			},
		},
		{
			name: "input audio",
			in: []byte(`{
"type": "input_audio",
"input_audio": {"data": "somebinarydata"}
}`),
			out: &ChatCompletionContentPartUserUnionParam{
				InputAudioContent: &ChatCompletionContentPartInputAudioParam{
					Type: ChatCompletionContentPartInputAudioTypeInputAudio,
					InputAudio: ChatCompletionContentPartInputAudioInputAudioParam{
						Data: "somebinarydata",
					},
				},
			},
		},
		{
			name:   "type not exist",
			in:     []byte(`{}`),
			expErr: "chat content does not have type",
		},
		{
			name: "unknown type",
			in: []byte(`{
"type": "unknown"
}`),
			expErr: "unknown ChatCompletionContentPartUnionParam type: unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var contentPart ChatCompletionContentPartUserUnionParam
			err := json.Unmarshal(tc.in, &contentPart)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}
			require.NoError(t, err)
			if !cmp.Equal(&contentPart, tc.out) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&contentPart, tc.out))
			}
		})
	}
}

func TestOpenAIChatCompletionMessageUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     []byte
		out    *ChatCompletionRequest
		expErr string
	}{
		{
			name: "basic test",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": "you are a helpful assistant"},
                         {"role": "developer", "content": "you are a helpful dev assistant"},
                         {"role": "user", "content": "what do you see in this image"},
                         {"role": "tool", "content": "some tool", "tool_call_id": "123"},
			                   {"role": "assistant", "content": "you are a helpful assistant"}
                    ]}
`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: StringOrArray{
								Value: "you are a helpful assistant",
							},
						},
						Type: ChatMessageRoleSystem,
					},
					{
						Value: ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: StringOrArray{
								Value: "you are a helpful dev assistant",
							},
						},
						Type: ChatMessageRoleDeveloper,
					},
					{
						Value: ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: "what do you see in this image",
							},
						},
						Type: ChatMessageRoleUser,
					},
					{
						Value: ChatCompletionToolMessageParam{
							Role:       ChatMessageRoleTool,
							ToolCallID: "123",
							Content:    StringOrArray{Value: "some tool"},
						},
						Type: ChatMessageRoleTool,
					},
					{
						Value: ChatCompletionAssistantMessageParam{
							Role:    ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: "you are a helpful assistant"},
						},
						Type: ChatMessageRoleAssistant,
					},
				},
			},
		},
		{
			name: "assistant message string",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "assistant", "content": "you are a helpful assistant"},
			                   {"role": "assistant", "content": [{"text": "you are a helpful assistant content", "type": "text"}]}
                    ]}
`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionAssistantMessageParam{
							Role:    ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: "you are a helpful assistant"},
						},
						Type: ChatMessageRoleAssistant,
					},
					{
						Value: ChatCompletionAssistantMessageParam{
							Role: ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: []ChatCompletionAssistantMessageParamContent{
								{Text: ptr.To("you are a helpful assistant content"), Type: "text"},
							}},
						},
						Type: ChatMessageRoleAssistant,
					},
				},
			},
		},
		{
			name: "content with array",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": [{"text": "you are a helpful assistant", "type": "text"}]},
                         {"role": "developer", "content": [{"text": "you are a helpful dev assistant", "type": "text"}]},
                         {"role": "user", "content": [{"text": "what do you see in this image", "type": "text"}]}]}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: StringOrArray{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful assistant",
										Type: "text",
									},
								},
							},
						},
						Type: ChatMessageRoleSystem,
					},
					{
						Value: ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: StringOrArray{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful dev assistant",
										Type: "text",
									},
								},
							},
						},
						Type: ChatMessageRoleDeveloper,
					},
					{
						Value: ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: []ChatCompletionContentPartUserUnionParam{
									{
										TextContent: &ChatCompletionContentPartTextParam{Text: "what do you see in this image", Type: "text"},
									},
								},
							},
						},
						Type: ChatMessageRoleUser,
					},
				},
			},
		},
		{
			name:   "no role",
			in:     []byte(`{"model": "gpu-o4","messages": [{}]}`),
			expErr: "chat message does not have role",
		},
		{
			name: "unknown role",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [{"role": "some-funky", "content": [{"text": "what do you see in this image", "type": "text"}]}]}`),
			expErr: "unknown ChatCompletionMessageParam type: some-funky",
		},
		{
			name: "response_format",
			in:   []byte(`{ "model": "azure.gpt-4o", "messages": [ { "role": "user", "content": "Tell me a story" } ], "response_format": { "type": "json_schema", "json_schema": { "name": "math_response", "schema": { "type": "object", "properties": { "step": "test_step" }, "required": [ "steps"], "additionalProperties": false }, "strict": true } } }`),
			out: &ChatCompletionRequest{
				Model: "azure.gpt-4o",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: "Tell me a story",
							},
						},
						Type: ChatMessageRoleUser,
					},
				},
				ResponseFormat: &ChatCompletionResponseFormat{
					Type: "json_schema",
					JSONSchema: &ChatCompletionResponseFormatJSONSchema{
						Name:   "math_response",
						Strict: true,
						Schema: map[string]any{
							"additionalProperties": false,
							"type":                 "object",
							"properties": map[string]any{
								"step": "test_step",
							},
							"required": []any{"steps"},
						},
					},
				},
			},
		},
		{
			name: "test fields",
			in: []byte(`{
				"model": "gpu-o4",
				"messages": [{"role": "user", "content": "hello"}],
				"max_completion_tokens": 1024,
				"parallel_tool_calls": true,
				"stop": ["\n", "stop"],
				"service_tier": "flex"
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "hello"},
						},
						Type: ChatMessageRoleUser,
					},
				},
				MaxCompletionTokens: ptr.To[int64](1024),
				ParallelToolCalls:   ptr.To(true),
				Stop:                []any{"\n", "stop"},
				ServiceTier:         ptr.To("flex"),
			},
		},
		{
			name: "stop as string",
			in: []byte(`{
				"model": "gpu-o4",
				"messages": [{"role": "user", "content": "hello"}],
				"stop": "stop"
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "hello"},
						},
						Type: ChatMessageRoleUser,
					},
				},
				Stop: "stop",
			},
		},
		{
			name: "web search options",
			in: []byte(`{
				"model": "gpt-4o-mini-search-preview",
				"messages": [{"role": "user", "content": "What's the latest news?"}],
				"web_search_options": {"search_context_size": "low"}
			}`),
			out: &ChatCompletionRequest{
				Model: "gpt-4o-mini-search-preview",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "What's the latest news?"},
						},
						Type: ChatMessageRoleUser,
					},
				},
				WebSearchOptions: &WebSearchOptions{
					SearchContextSize: WebSearchContextSizeLow,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chatCompletion ChatCompletionRequest
			err := json.Unmarshal(tc.in, &chatCompletion)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}
			require.NoError(t, err)
			if !cmp.Equal(&chatCompletion, tc.out) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&chatCompletion, tc.out))
			}
		})
	}
}

func TestModelListMarshal(t *testing.T) {
	var (
		model = Model{
			ID:      "gpt-3.5-turbo",
			Object:  "model",
			OwnedBy: "tetrate",
			Created: JSONUNIXTime(time.Date(2025, 0o1, 0o1, 0, 0, 0, 0, time.UTC)),
		}
		list = ModelList{Object: "list", Data: []Model{model}}
		raw  = `{"object":"list","data":[{"id":"gpt-3.5-turbo","object":"model","owned_by":"tetrate","created":1735689600}]}`
	)

	b, err := json.Marshal(list)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(b))

	var out ModelList
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	require.Len(t, out.Data, 1)
	require.Equal(t, "list", out.Object)
	require.Equal(t, model.ID, out.Data[0].ID)
	require.Equal(t, model.Object, out.Data[0].Object)
	require.Equal(t, model.OwnedBy, out.Data[0].OwnedBy)
	// Unmarshalling initializes other fields in time.Time we're not interested with. Just compare the actual time.
	require.Equal(t, time.Time(model.Created).Unix(), time.Time(out.Data[0].Created).Unix())
}

func TestChatCompletionMessageParamUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    ChatCompletionMessageParamUnion
		expected string
	}{
		{
			name: "user message",
			input: ChatCompletionMessageParamUnion{
				Type: ChatMessageRoleUser,
				Value: ChatCompletionUserMessageParam{
					Role: ChatMessageRoleUser,
					Content: StringOrUserRoleContentUnion{
						Value: "Hello!",
					},
				},
			},
			expected: `{"content":"Hello!","role":"user"}`,
		},
		{
			name: "system message",
			input: ChatCompletionMessageParamUnion{
				Type: ChatMessageRoleSystem,
				Value: ChatCompletionSystemMessageParam{
					Role: ChatMessageRoleSystem,
					Content: StringOrArray{
						Value: "You are a helpful assistant",
					},
				},
			},
			expected: `{"content":"You are a helpful assistant","role":"system"}`,
		},
		{
			name: "assistant message",
			input: ChatCompletionMessageParamUnion{
				Type: ChatMessageRoleAssistant,
				Value: ChatCompletionAssistantMessageParam{
					Role: ChatMessageRoleAssistant,
					Content: StringOrAssistantRoleContentUnion{
						Value: "I can help you with that",
					},
				},
			},
			expected: `{"role":"assistant","content":"I can help you with that"}`,
		},
		{
			name: "tool message",
			input: ChatCompletionMessageParamUnion{
				Type: ChatMessageRoleTool,
				Value: ChatCompletionToolMessageParam{
					Role:       ChatMessageRoleTool,
					ToolCallID: "123",
					Content:    StringOrArray{Value: "tool result"},
				},
			},
			expected: `{"content":"tool result","role":"tool","tool_call_id":"123"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestStringOrArrayMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    StringOrArray
		expected string
	}{
		{
			name:     "string value",
			input:    StringOrArray{Value: "hello world"},
			expected: `"hello world"`,
		},
		{
			name:     "string array",
			input:    StringOrArray{Value: []string{"hello", "world"}},
			expected: `["hello","world"]`,
		},
		{
			name:     "int array", // for token embeddings.
			input:    StringOrArray{Value: []int64{1, 2}},
			expected: `[1,2]`,
		},
		{
			name: "text param array",
			input: StringOrArray{Value: []ChatCompletionContentPartTextParam{
				{Text: "hello", Type: "text"},
				{Text: "world", Type: "text"},
			}},
			expected: `[{"text":"hello","type":"text"},{"text":"world","type":"text"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestStringOrUserRoleContentUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    StringOrUserRoleContentUnion
		expected string
	}{
		{
			name:     "string value",
			input:    StringOrUserRoleContentUnion{Value: "What is the weather?"},
			expected: `"What is the weather?"`,
		},
		{
			name: "content array",
			input: StringOrUserRoleContentUnion{
				Value: []ChatCompletionContentPartUserUnionParam{
					{
						TextContent: &ChatCompletionContentPartTextParam{
							Type: "text",
							Text: "What's in this image?",
						},
					},
				},
			},
			expected: `[{"text":"What's in this image?","type":"text"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestStringOrAssistantRoleContentUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    StringOrAssistantRoleContentUnion
		expected string
	}{
		{
			name:     "string value",
			input:    StringOrAssistantRoleContentUnion{Value: "I can help with that"},
			expected: `"I can help with that"`,
		},
		{
			name: "content object",
			input: StringOrAssistantRoleContentUnion{
				Value: ChatCompletionAssistantMessageParamContent{
					Text: ptr.To("Here is the answer"),
					Type: ChatCompletionAssistantMessageParamContentTypeText,
				},
			},
			expected: `{"type":"text","text":"Here is the answer"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestChatCompletionContentPartUserUnionParamMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    ChatCompletionContentPartUserUnionParam
		expected string
	}{
		{
			name: "text content",
			input: ChatCompletionContentPartUserUnionParam{
				TextContent: &ChatCompletionContentPartTextParam{
					Type: "text",
					Text: "Hello world",
				},
			},
			expected: `{"text":"Hello world","type":"text"}`,
		},
		{
			name: "image content",
			input: ChatCompletionContentPartUserUnionParam{
				ImageContent: &ChatCompletionContentPartImageParam{
					Type: ChatCompletionContentPartImageTypeImageURL,
					ImageURL: ChatCompletionContentPartImageImageURLParam{
						URL: "https://example.com/image.jpg",
					},
				},
			},
			expected: `{"image_url":{"url":"https://example.com/image.jpg"},"type":"image_url"}`,
		},
		{
			name: "audio content",
			input: ChatCompletionContentPartUserUnionParam{
				InputAudioContent: &ChatCompletionContentPartInputAudioParam{
					Type: ChatCompletionContentPartInputAudioTypeInputAudio,
					InputAudio: ChatCompletionContentPartInputAudioInputAudioParam{
						Data: "audio-data",
					},
				},
			},
			expected: `{"input_audio":{"data":"audio-data","format":""},"type":"input_audio"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := json.Marshal(tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	// Test that we can marshal and unmarshal a complete chat completion request
	req := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatCompletionMessageParamUnion{
			{
				Type: ChatMessageRoleSystem,
				Value: ChatCompletionSystemMessageParam{
					Role:    ChatMessageRoleSystem,
					Content: StringOrArray{Value: "You are helpful"},
				},
			},
			{
				Type: ChatMessageRoleUser,
				Value: ChatCompletionUserMessageParam{
					Role: ChatMessageRoleUser,
					Content: StringOrUserRoleContentUnion{
						Value: []ChatCompletionContentPartUserUnionParam{
							{
								TextContent: &ChatCompletionContentPartTextParam{
									Type: "text",
									Text: "What's in this image?",
								},
							},
							{
								ImageContent: &ChatCompletionContentPartImageParam{
									Type: ChatCompletionContentPartImageTypeImageURL,
									ImageURL: ChatCompletionContentPartImageImageURLParam{
										URL: "https://example.com/image.jpg",
									},
								},
							},
						},
					},
				},
			},
		},
		Temperature: ptr.To(0.7),
		MaxTokens:   ptr.To[int64](100),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(req)
	require.NoError(t, err)

	// Unmarshal back
	var decoded ChatCompletionRequest
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	// Verify the structure
	require.Equal(t, req.Model, decoded.Model)
	require.Len(t, req.Messages, len(decoded.Messages))
	require.Equal(t, *req.Temperature, *decoded.Temperature)
	require.Equal(t, *req.MaxTokens, *decoded.MaxTokens)
}

func TestChatCompletionResponse(t *testing.T) {
	testCases := []struct {
		name     string
		response ChatCompletionResponse
		expected string
	}{
		{
			name: "basic response with new fields",
			response: ChatCompletionResponse{
				ID:                "chatcmpl-test123",
				Created:           JSONUNIXTime(time.Unix(1735689600, 0)),
				Model:             "gpt-4.1-nano",
				ServiceTier:       "default",
				SystemFingerprint: "",
				Object:            "chat.completion",
				Choices: []ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: ChatCompletionChoicesFinishReasonStop,
						Message: ChatCompletionResponseChoiceMessage{
							Role:    "assistant",
							Content: ptr.To("Hello!"),
						},
					},
				},
				Usage: ChatCompletionResponseUsage{
					CompletionTokens: 1,
					PromptTokens:     5,
					TotalTokens:      6,
				},
			},
			expected: `{
				"id": "chatcmpl-test123",
				"object": "chat.completion",
				"created": 1735689600,
				"model": "gpt-4.1-nano",
				"service_tier": "default",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Hello!"
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 5,
					"completion_tokens": 1,
					"total_tokens": 6
				}
			}`,
		},
		{
			name: "response with web search annotations",
			response: ChatCompletionResponse{
				ID:      "chatcmpl-bf3e7207-9819-40a2-9225-87e8666fe23d",
				Created: JSONUNIXTime(time.Unix(1755135425, 0)),
				Model:   "gpt-4o-mini-search-preview-2025-03-11",
				Object:  "chat.completion",
				Choices: []ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: ChatCompletionChoicesFinishReasonStop,
						Message: ChatCompletionResponseChoiceMessage{
							Role:    "assistant",
							Content: ptr.To("Check out httpbin.org"),
							Annotations: ptr.To([]Annotation{
								{
									Type: "url_citation",
									URLCitation: &URLCitation{
										EndIndex:   21,
										StartIndex: 10,
										Title:      "httpbin.org",
										URL:        "https://httpbin.org/?utm_source=openai",
									},
								},
							}),
						},
					},
				},
				Usage: ChatCompletionResponseUsage{
					CompletionTokens: 192,
					PromptTokens:     14,
					TotalTokens:      206,
				},
			},
			expected: `{
				"id": "chatcmpl-bf3e7207-9819-40a2-9225-87e8666fe23d",
				"object": "chat.completion",
				"created": 1755135425,
				"model": "gpt-4o-mini-search-preview-2025-03-11",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Check out httpbin.org",
						"annotations": [{
							"type": "url_citation",
							"url_citation": {
								"end_index": 21,
								"start_index": 10,
								"title": "httpbin.org",
								"url": "https://httpbin.org/?utm_source=openai"
							}
						}]
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 14,
					"completion_tokens": 192,
					"total_tokens": 206
				}
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal to JSON
			jsonData, err := json.Marshal(tc.response)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			// Unmarshal back and verify round-trip
			var decoded ChatCompletionResponse
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.response.ID, decoded.ID)
			require.Equal(t, tc.response.Model, decoded.Model)
			require.Equal(t, time.Time(tc.response.Created).Unix(), time.Time(decoded.Created).Unix())
		})
	}
}

func TestChatCompletionRequest(t *testing.T) {
	testCases := []struct {
		name     string
		jsonStr  string
		expected *ChatCompletionRequest
	}{
		{
			name: "text and audio modalities",
			jsonStr: `{
				"model": "gpt-4o-audio-preview",
				"messages": [{"role": "user", "content": "Hello!"}],
				"modalities": ["text", "audio"]
			}`,
			expected: &ChatCompletionRequest{
				Model: "gpt-4o-audio-preview",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Type: ChatMessageRoleUser,
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Hello!"},
						},
					},
				},
				Modalities: []ChatCompletionModality{ChatCompletionModalityText, ChatCompletionModalityAudio},
			},
		},
		{
			name: "text only modality",
			jsonStr: `{
				"model": "gpt-4.1-nano",
				"messages": [{"role": "user", "content": "Hi"}],
				"modalities": ["text"]
			}`,
			expected: &ChatCompletionRequest{
				Model: "gpt-4.1-nano",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Type: ChatMessageRoleUser,
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Hi"},
						},
					},
				},
				Modalities: []ChatCompletionModality{ChatCompletionModalityText},
			},
		},
		{
			name: "audio output parameters",
			jsonStr: `{
				"model": "gpt-4o-audio-preview",
				"messages": [{"role": "user", "content": "Hello!"}],
				"modalities": ["audio"],
				"audio": {
					"voice": "alloy",
					"format": "wav"
				}
			}`,
			expected: &ChatCompletionRequest{
				Model: "gpt-4o-audio-preview",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Type: ChatMessageRoleUser,
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Hello!"},
						},
					},
				},
				Modalities: []ChatCompletionModality{ChatCompletionModalityAudio},
				Audio: &ChatCompletionAudioParam{
					Voice:  ChatCompletionAudioVoiceAlloy,
					Format: ChatCompletionAudioFormatWav,
				},
			},
		},
		{
			name: "prediction with string content",
			jsonStr: `{
				"model": "gpt-4.1-nano",
				"messages": [{"role": "user", "content": "Complete this: Hello"}],
				"prediction": {
					"type": "content",
					"content": "Hello world!"
				}
			}`,
			expected: &ChatCompletionRequest{
				Model: "gpt-4.1-nano",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Type: ChatMessageRoleUser,
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Complete this: Hello"},
						},
					},
				},
				PredictionContent: &PredictionContent{
					Type:    PredictionContentTypeContent,
					Content: StringOrArray{Value: "Hello world!"},
				},
			},
		},
		{
			name: "web search options",
			jsonStr: `{
				"model": "gpt-4o-mini-search-preview",
				"messages": [{"role": "user", "content": "What's the latest news?"}],
				"web_search_options": {"search_context_size": "low"}
			}`,
			expected: &ChatCompletionRequest{
				Model: "gpt-4o-mini-search-preview",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Type: ChatMessageRoleUser,
						Value: ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "What's the latest news?"},
						},
					},
				},
				WebSearchOptions: &WebSearchOptions{
					SearchContextSize: WebSearchContextSizeLow,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var req ChatCompletionRequest
			err := json.Unmarshal([]byte(tc.jsonStr), &req)
			require.NoError(t, err)
			require.Equal(t, *tc.expected, req)

			// Marshal back and verify it round-trips
			marshaled, err := json.Marshal(req)
			require.NoError(t, err)
			var req2 ChatCompletionRequest
			err = json.Unmarshal(marshaled, &req2)
			require.NoError(t, err)
			require.Equal(t, req, req2)
		})
	}
}

func TestChatCompletionAudioFormats(t *testing.T) {
	formats := []ChatCompletionAudioFormat{
		ChatCompletionAudioFormatWav,
		ChatCompletionAudioFormatAAC,
		ChatCompletionAudioFormatMP3,
		ChatCompletionAudioFormatFlac,
		ChatCompletionAudioFormatOpus,
		ChatCompletionAudioFormatPCM16,
	}

	for _, format := range formats {
		audio := ChatCompletionAudioParam{
			Voice:  ChatCompletionAudioVoiceNova,
			Format: format,
		}
		data, err := json.Marshal(audio)
		require.NoError(t, err)
		require.Contains(t, string(data), string(format))
	}
}

func TestPredictionContentType(t *testing.T) {
	// Verify the constant value matches OpenAPI spec.
	require.Equal(t, PredictionContentTypeContent, PredictionContentType("content"))
}

func TestUnmarshalJSON_Unmarshal(t *testing.T) {
	jsonStr := `{"value": 3.14}`
	var data struct {
		Time JSONUNIXTime `json:"value"`
	}
	err := json.Unmarshal([]byte(jsonStr), &data)
	require.NoError(t, err)
	require.Equal(t, int64(3), time.Time(data.Time).Unix())

	jsonStr = `{"value": 2}`
	err = json.Unmarshal([]byte(jsonStr), &data)
	require.NoError(t, err)
	require.Equal(t, int64(2), time.Time(data.Time).Unix())
}

func TestChatCompletionResponseChunkChoice(t *testing.T) {
	testCases := []struct {
		name     string
		choice   ChatCompletionResponseChunkChoice
		expected string
	}{
		{
			name: "streaming chunk with content",
			choice: ChatCompletionResponseChunkChoice{
				Index: 0,
				Delta: &ChatCompletionResponseChunkChoiceDelta{
					Content: ptr.To("Hello"),
					Role:    "assistant",
				},
			},
			expected: `{"index":0,"delta":{"content":"Hello","role":"assistant"}}`,
		},
		{
			name: "streaming chunk with empty content",
			choice: ChatCompletionResponseChunkChoice{
				Index: 0,
				Delta: &ChatCompletionResponseChunkChoiceDelta{
					Content: ptr.To(""),
					Role:    "assistant",
				},
			},
			expected: `{"index":0,"delta":{"content":"","role":"assistant"}}`,
		},
		{
			name: "streaming chunk with tool calls",
			choice: ChatCompletionResponseChunkChoice{
				Index: 0,
				Delta: &ChatCompletionResponseChunkChoiceDelta{
					Role: "assistant",
					ToolCalls: []ChatCompletionMessageToolCallParam{
						{
							ID:   ptr.To("tooluse_QklrEHKjRu6Oc4BQUfy7ZQ"),
							Type: "function",
							Function: ChatCompletionMessageToolCallFunctionParam{
								Name:      "cosine",
								Arguments: "",
							},
						},
					},
				},
			},
			expected: `{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"tooluse_QklrEHKjRu6Oc4BQUfy7ZQ","function":{"arguments":"","name":"cosine"},"type":"function"}]}}`,
		},
		{
			name: "streaming chunk with annotations",
			choice: ChatCompletionResponseChunkChoice{
				Index: 0,
				Delta: &ChatCompletionResponseChunkChoiceDelta{
					Annotations: ptr.To([]Annotation{
						{
							Type: "url_citation",
							URLCitation: &URLCitation{
								EndIndex:   215,
								StartIndex: 160,
								Title:      "httpbin.org",
								URL:        "https://httpbin.org/?utm_source=openai",
							},
						},
					}),
				},
				FinishReason: "stop",
			},
			expected: `{"index":0,"delta":{"annotations":[{"type":"url_citation","url_citation":{"end_index":215,"start_index":160,"title":"httpbin.org","url":"https://httpbin.org/?utm_source=openai"}}]},"finish_reason":"stop"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.choice)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))
		})
	}
}

func TestChatCompletionResponseChunk(t *testing.T) {
	testCases := []struct {
		name     string
		chunk    ChatCompletionResponseChunk
		expected string
	}{
		{
			name: "chunk with obfuscation",
			chunk: ChatCompletionResponseChunk{
				ID:                "chatcmpl-123",
				Object:            "chat.completion.chunk",
				Created:           JSONUNIXTime(time.Unix(1755137933, 0)),
				Model:             "gpt-5-nano",
				ServiceTier:       "default",
				SystemFingerprint: "fp_123",
				Choices: []ChatCompletionResponseChunkChoice{
					{
						Index: 0,
						Delta: &ChatCompletionResponseChunkChoiceDelta{
							Content: ptr.To("Hello"),
						},
					},
				},
				Obfuscation: "yBUv8b1dlI5ORP",
			},
			expected: `{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1755137933,"model":"gpt-5-nano","service_tier":"default","system_fingerprint":"fp_123","choices":[{"index":0,"delta":{"content":"Hello"}}],"obfuscation":"yBUv8b1dlI5ORP"}`,
		},
		{
			name: "chunk without obfuscation",
			chunk: ChatCompletionResponseChunk{
				ID:      "chatcmpl-456",
				Object:  "chat.completion.chunk",
				Created: JSONUNIXTime(time.Unix(1755137934, 0)),
				Model:   "gpt-5-nano",
				Choices: []ChatCompletionResponseChunkChoice{
					{
						Index: 0,
						Delta: &ChatCompletionResponseChunkChoiceDelta{
							Content: ptr.To("World"),
						},
					},
				},
			},
			expected: `{"id":"chatcmpl-456","object":"chat.completion.chunk","created":1755137934,"model":"gpt-5-nano","choices":[{"index":0,"delta":{"content":"World"}}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.chunk)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))
		})
	}
}

func TestURLCitation(t *testing.T) {
	testCases := []struct {
		name     string
		citation URLCitation
		expected string
	}{
		{
			name: "url citation with all fields",
			citation: URLCitation{
				EndIndex:   215,
				StartIndex: 160,
				Title:      "httpbin.org",
				URL:        "https://httpbin.org/?utm_source=openai",
			},
			expected: `{"end_index":215,"start_index":160,"title":"httpbin.org","url":"https://httpbin.org/?utm_source=openai"}`,
		},
		{
			name: "url citation minimal",
			citation: URLCitation{
				EndIndex:   10,
				StartIndex: 0,
				URL:        "https://example.com",
				Title:      "Example",
			},
			expected: `{"end_index":10,"start_index":0,"title":"Example","url":"https://example.com"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.citation)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			var decoded URLCitation
			err = json.Unmarshal([]byte(tc.expected), &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.citation, decoded)
		})
	}
}

func TestAnnotation(t *testing.T) {
	testCases := []struct {
		name       string
		annotation Annotation
		expected   string
	}{
		{
			name: "annotation with url citation",
			annotation: Annotation{
				Type: "url_citation",
				URLCitation: &URLCitation{
					EndIndex:   215,
					StartIndex: 160,
					Title:      "httpbin.org",
					URL:        "https://httpbin.org/?utm_source=openai",
				},
			},
			expected: `{"type":"url_citation","url_citation":{"end_index":215,"start_index":160,"title":"httpbin.org","url":"https://httpbin.org/?utm_source=openai"}}`,
		},
		{
			name: "annotation type only",
			annotation: Annotation{
				Type: "url_citation",
			},
			expected: `{"type":"url_citation"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.annotation)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			var decoded Annotation
			err = json.Unmarshal([]byte(tc.expected), &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.annotation, decoded)
		})
	}
}

func TestEmbeddingUnionUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    any
		wantErr bool
	}{
		{
			name:  "unmarshal array of floats",
			input: `[1.0, 2.0, 3.0]`,
			want:  []float64{1.0, 2.0, 3.0},
		},
		{
			name:  "unmarshal string",
			input: `"base64response"`,
			want:  "base64response",
		},
		{
			name:    "unmarshal int should error",
			input:   `123`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var eu EmbeddingUnion
			err := json.Unmarshal([]byte(tt.input), &eu)
			if (err != nil) != tt.wantErr {
				t.Errorf("EmbeddingUnion Unmarshal Error. error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Use reflect.DeepEqual to compare
				if !cmp.Equal(eu.Value, tt.want) {
					t.Errorf("EmbeddingUnion Unmarshal Error. got = %v, want %v", eu.Value, tt.want)
				}
			}
		})
	}
}

func TestChatCompletionResponseChoiceMessageAudio(t *testing.T) {
	testCases := []struct {
		name     string
		audio    ChatCompletionResponseChoiceMessageAudio
		expected string
	}{
		{
			name: "audio with all fields",
			audio: ChatCompletionResponseChoiceMessageAudio{
				Data:       "base64audiodata",
				ExpiresAt:  1735689600,
				ID:         "audio-123",
				Transcript: "Hello, world!",
			},
			expected: `{
				"data": "base64audiodata",
				"expires_at": 1735689600,
				"id": "audio-123",
				"transcript": "Hello, world!"
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.audio)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			var decoded ChatCompletionResponseChoiceMessageAudio
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.audio, decoded)
		})
	}
}

func TestCompletionTokensDetails(t *testing.T) {
	testCases := []struct {
		name     string
		details  CompletionTokensDetails
		expected string
	}{
		{
			name: "with text tokens",
			details: CompletionTokensDetails{
				TextTokens:               5,
				AcceptedPredictionTokens: 10,
				AudioTokens:              256,
				ReasoningTokens:          832,
				RejectedPredictionTokens: 2,
			},
			expected: `{
				"text_tokens": 5,
				"accepted_prediction_tokens": 10,
				"audio_tokens": 256,
				"reasoning_tokens": 832,
				"rejected_prediction_tokens": 2
			}`,
		},
		{
			name: "with zero text tokens omitted",
			details: CompletionTokensDetails{
				TextTokens:      0,
				AudioTokens:     256,
				ReasoningTokens: 832,
			},
			expected: `{
				"audio_tokens": 256,
				"reasoning_tokens": 832
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.details)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			var decoded CompletionTokensDetails
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.details, decoded)
		})
	}
}

func TestPromptTokensDetails(t *testing.T) {
	testCases := []struct {
		name     string
		details  PromptTokensDetails
		expected string
	}{
		{
			name: "with text tokens",
			details: PromptTokensDetails{
				TextTokens:   15,
				AudioTokens:  8,
				CachedTokens: 384,
			},
			expected: `{
				"text_tokens": 15,
				"audio_tokens": 8,
				"cached_tokens": 384
			}`,
		},
		{
			name: "with zero text tokens omitted",
			details: PromptTokensDetails{
				TextTokens:   0,
				AudioTokens:  8,
				CachedTokens: 384,
			},
			expected: `{
				"audio_tokens": 8,
				"cached_tokens": 384
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tc.details)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			var decoded PromptTokensDetails
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.details, decoded)
		})
	}
}

func TestChatCompletionResponseUsage(t *testing.T) {
	testCases := []struct {
		name     string
		usage    ChatCompletionResponseUsage
		expected string
	}{
		{
			name: "with zero values omitted",
			usage: ChatCompletionResponseUsage{
				CompletionTokens: 9,
				PromptTokens:     19,
				TotalTokens:      28,
				CompletionTokensDetails: &CompletionTokensDetails{
					AcceptedPredictionTokens: 0,
					AudioTokens:              0,
					ReasoningTokens:          0,
					RejectedPredictionTokens: 0,
				},
				PromptTokensDetails: &PromptTokensDetails{
					AudioTokens:  0,
					CachedTokens: 0,
				},
			},
			expected: `{"completion_tokens":9,"prompt_tokens":19,"total_tokens":28,"completion_tokens_details":{},"prompt_tokens_details":{}}`,
		},
		{
			name: "with non-zero values",
			usage: ChatCompletionResponseUsage{
				CompletionTokens: 11,
				PromptTokens:     37,
				TotalTokens:      48,
				CompletionTokensDetails: &CompletionTokensDetails{
					AcceptedPredictionTokens: 0,
					AudioTokens:              256,
					ReasoningTokens:          832,
					RejectedPredictionTokens: 0,
				},
				PromptTokensDetails: &PromptTokensDetails{
					AudioTokens:  8,
					CachedTokens: 384,
				},
			},
			expected: `{
				"completion_tokens": 11,
				"prompt_tokens": 37,
				"total_tokens": 48,
				"completion_tokens_details": {
					"audio_tokens": 256,
					"reasoning_tokens": 832
				},
				"prompt_tokens_details": {
					"audio_tokens": 8,
					"cached_tokens": 384
				}
			}`,
		},
		{
			name: "with text tokens",
			usage: ChatCompletionResponseUsage{
				CompletionTokens: 11,
				PromptTokens:     37,
				TotalTokens:      48,
				CompletionTokensDetails: &CompletionTokensDetails{
					TextTokens:               5,
					AcceptedPredictionTokens: 0,
					AudioTokens:              256,
					ReasoningTokens:          832,
					RejectedPredictionTokens: 0,
				},
				PromptTokensDetails: &PromptTokensDetails{
					TextTokens:   15,
					AudioTokens:  8,
					CachedTokens: 384,
				},
			},
			expected: `{
				"completion_tokens": 11,
				"prompt_tokens": 37,
				"total_tokens": 48,
				"completion_tokens_details": {
					"text_tokens": 5,
					"audio_tokens": 256,
					"reasoning_tokens": 832
				},
				"prompt_tokens_details": {
					"text_tokens": 15,
					"audio_tokens": 8,
					"cached_tokens": 384
				}
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal to JSON
			jsonData, err := json.Marshal(tc.usage)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			// Unmarshal and verify
			var decoded ChatCompletionResponseUsage
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.usage, decoded)
		})
	}
}

func TestWebSearchOptions(t *testing.T) {
	testCases := []struct {
		name     string
		options  WebSearchOptions
		expected string
	}{
		{
			name: "search context size low",
			options: WebSearchOptions{
				SearchContextSize: WebSearchContextSizeLow,
			},
			expected: `{"search_context_size":"low"}`,
		},
		{
			name: "with user location",
			options: WebSearchOptions{
				SearchContextSize: WebSearchContextSizeMedium,
				UserLocation: &WebSearchUserLocation{
					Type: "approximate",
					Approximate: WebSearchLocation{
						City:    "San Francisco",
						Region:  "California",
						Country: "USA",
					},
				},
			},
			expected: `{"user_location":{"type":"approximate","approximate":{"city":"San Francisco","region":"California","country":"USA"}},"search_context_size":"medium"}`,
		},
		{
			name:     "empty options",
			options:  WebSearchOptions{},
			expected: `{}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal to JSON
			jsonData, err := json.Marshal(tc.options)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(jsonData))

			// Unmarshal and verify
			var decoded WebSearchOptions
			err = json.Unmarshal(jsonData, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.options, decoded)
		})
	}
}

// This tests ensures to use a pointer to the slice since otherwise for "annotations" field to maintain
// the same results after round trip.
func TestChatCompletionResponseChoiceMessage_annotations_round_trip(t *testing.T) {
	orig := []byte(`{"annotations": []}`)
	var msg ChatCompletionResponseChoiceMessage
	err := json.Unmarshal(orig, &msg)
	require.NoError(t, err)
	require.NotNil(t, msg.Annotations)
	marshaled, err := json.Marshal(msg)
	require.NoError(t, err)
	require.JSONEq(t, `{"annotations":[]}`, string(marshaled))

	var msg2 ChatCompletionResponseChoiceMessage
	err = json.Unmarshal([]byte(`{}`), &msg2)
	require.NoError(t, err)
	require.Nil(t, msg2.Annotations)
	marshaled, err = json.Marshal(msg2)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(marshaled))
}

// This tests ensures to use a pointer to the slice since otherwise for "annotations" field to maintain
// the same results after round trip.
func TestChatCompletionResponseChunkChoiceDelta_annotations_round_trip(t *testing.T) {
	orig := []byte(`{"annotations": []}`)
	var msg ChatCompletionResponseChunkChoiceDelta
	err := json.Unmarshal(orig, &msg)
	require.NoError(t, err)
	require.NotNil(t, msg.Annotations)
	marshaled, err := json.Marshal(msg)
	require.NoError(t, err)
	require.JSONEq(t, `{"annotations":[]}`, string(marshaled))

	var msg2 ChatCompletionResponseChunkChoiceDelta
	err = json.Unmarshal([]byte(`{}`), &msg2)
	require.NoError(t, err)
	require.Nil(t, msg2.Annotations)
	marshaled, err = json.Marshal(msg2)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(marshaled))
}

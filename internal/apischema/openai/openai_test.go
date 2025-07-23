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
						Schema: map[string]interface{}{
							"additionalProperties": false,
							"type":                 "object",
							"properties": map[string]interface{}{
								"step": "test_step",
							},
							"required": []interface{}{"steps"},
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
				"stop": ["\n", "stop"]
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
				Stop:                []interface{}{"\n", "stop"},
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

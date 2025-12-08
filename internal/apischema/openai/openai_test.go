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
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/packages/param"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
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
				OfText: &ChatCompletionContentPartTextParam{
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
				OfImageURL: &ChatCompletionContentPartImageParam{
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
				OfInputAudio: &ChatCompletionContentPartInputAudioParam{
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

func TestOpenAIChatCompletionResponseFormatUnionUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     []byte
		out    *ChatCompletionResponseFormatUnion
		expErr string
	}{
		{
			name: "text",
			in:   []byte(`{"type": "text"}`),
			out: &ChatCompletionResponseFormatUnion{
				OfText: &ChatCompletionResponseFormatTextParam{
					Type: ChatCompletionResponseFormatTypeText,
				},
			},
		},
		{
			name: "json schema",
			in:   []byte(`{"json_schema": { "name": "math_response", "schema": { "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }, "strict": true }, "type":"json_schema"}`),
			out: &ChatCompletionResponseFormatUnion{
				OfJSONSchema: &ChatCompletionResponseFormatJSONSchema{
					Type: "json_schema",
					JSONSchema: ChatCompletionResponseFormatJSONSchemaJSONSchema{
						Name:   "math_response",
						Strict: true,
						Schema: json.RawMessage(`{ "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }`),
					},
				},
			},
		},
		{
			name: "json object",
			in:   []byte(`{"type": "json_object"}`),
			out: &ChatCompletionResponseFormatUnion{
				OfJSONObject: &ChatCompletionResponseFormatJSONObjectParam{
					Type: "json_object",
				},
			},
		},
		{
			name:   "type not exist",
			in:     []byte(`{}`),
			expErr: "response format does not have type",
		},
		{
			name: "unknown type",
			in: []byte(`{
"type": "unknown"
}`),
			expErr: "unsupported ChatCompletionResponseFormatType",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var contentPart ChatCompletionResponseFormatUnion
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
						OfSystem: &ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: ContentUnion{
								Value: "you are a helpful assistant",
							},
						},
					},
					{
						OfDeveloper: &ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: ContentUnion{
								Value: "you are a helpful dev assistant",
							},
						},
					},
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: "what do you see in this image",
							},
						},
					},
					{
						OfTool: &ChatCompletionToolMessageParam{
							Role:       ChatMessageRoleTool,
							ToolCallID: "123",
							Content:    ContentUnion{Value: "some tool"},
						},
					},
					{
						OfAssistant: &ChatCompletionAssistantMessageParam{
							Role:    ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: "you are a helpful assistant"},
						},
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
						OfAssistant: &ChatCompletionAssistantMessageParam{
							Role:    ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: "you are a helpful assistant"},
						},
					},
					{
						OfAssistant: &ChatCompletionAssistantMessageParam{
							Role: ChatMessageRoleAssistant,
							Content: StringOrAssistantRoleContentUnion{Value: []ChatCompletionAssistantMessageParamContent{
								{Text: ptr.To("you are a helpful assistant content"), Type: "text"},
							}},
						},
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
						OfSystem: &ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: ContentUnion{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful assistant",
										Type: "text",
									},
								},
							},
						},
					},
					{
						OfDeveloper: &ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: ContentUnion{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful dev assistant",
										Type: "text",
									},
								},
							},
						},
					},
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: []ChatCompletionContentPartUserUnionParam{
									{
										OfText: &ChatCompletionContentPartTextParam{Text: "what do you see in this image", Type: "text"},
									},
								},
							},
						},
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
			in:   []byte(`{ "model": "azure.gpt-4o", "messages": [ { "role": "user", "content": "Tell me a story" } ], "response_format": { "type": "json_schema", "json_schema": { "name": "math_response", "schema": { "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }, "strict": true } } }`),
			out: &ChatCompletionRequest{
				Model: "azure.gpt-4o",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: "Tell me a story",
							},
						},
					},
				},
				ResponseFormat: &ChatCompletionResponseFormatUnion{
					OfJSONSchema: &ChatCompletionResponseFormatJSONSchema{
						Type: "json_schema",
						JSONSchema: ChatCompletionResponseFormatJSONSchemaJSONSchema{
							Name:   "math_response",
							Strict: true,
							Schema: json.RawMessage(`{ "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }`),
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
				"service_tier": "flex",
                "verbosity": "low",
                "reasoning_effort": "low"
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "hello"},
						},
					},
				},
				MaxCompletionTokens: ptr.To[int64](1024),
				ParallelToolCalls:   ptr.To(true),
				Stop: openai.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"\n", "stop"},
				},
				ServiceTier:     openai.ChatCompletionNewParamsServiceTierFlex,
				Verbosity:       openai.ChatCompletionNewParamsVerbosityLow,
				ReasoningEffort: openai.ReasoningEffortLow,
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
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "hello"},
						},
					},
				},
				Stop: openai.ChatCompletionNewParamsStopUnion{
					OfString: openai.Opt[string]("stop"),
				},
			},
		},
		{
			name: "stop as array",
			in: []byte(`{
				"model": "gpu-o4",
				"messages": [{"role": "user", "content": "hello"}],
				"stop": ["</s>", "__end_tag__", "<|eot_id|>", "[answer_end]"]
			}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "hello"},
						},
					},
				},
				Stop: openai.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"</s>", "__end_tag__", "<|eot_id|>", "[answer_end]"},
				},
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
						OfUser: &ChatCompletionUserMessageParam{
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chatCompletion ChatCompletionRequest
			err := json.Unmarshal(tc.in, &chatCompletion)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}
			require.NoError(t, err)
			if !cmp.Equal(&chatCompletion, tc.out,
				cmpopts.IgnoreUnexported(openai.ChatCompletionNewParamsStopUnion{}, param.Opt[string]{})) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&chatCompletion, tc.out,
					cmpopts.IgnoreUnexported(openai.ChatCompletionNewParamsStopUnion{}, param.Opt[string]{})))
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
				OfUser: &ChatCompletionUserMessageParam{
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
				OfSystem: &ChatCompletionSystemMessageParam{
					Role: ChatMessageRoleSystem,
					Content: ContentUnion{
						Value: "You are a helpful assistant",
					},
				},
			},
			expected: `{"content":"You are a helpful assistant","role":"system"}`,
		},
		{
			name: "assistant message",
			input: ChatCompletionMessageParamUnion{
				OfAssistant: &ChatCompletionAssistantMessageParam{
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
				OfTool: &ChatCompletionToolMessageParam{
					Role:       ChatMessageRoleTool,
					ToolCallID: "123",
					Content:    ContentUnion{Value: "tool result"},
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

func TestContentUnionUnmarshal(t *testing.T) {
	for _, tc := range contentUnionBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			var p ContentUnion
			err := p.UnmarshalJSON(tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, p.Value)
		})
	}
	// Test cases that cover ContentUnion.UnmarshalJSON lines
	errorCases := []struct {
		name        string
		data        []byte
		expectedErr string
	}{
		{
			name:        "truncated data triggers skipLeadingWhitespace error",
			data:        []byte{},
			expectedErr: "truncated content data",
		},
		{
			name:        "invalid array unmarshal",
			data:        []byte(`["not a valid ChatCompletionContentPartTextParam"]`),
			expectedErr: "cannot unmarshal content as []ChatCompletionContentPartTextParam",
		},
		{
			name:        "invalid type (number)",
			data:        []byte(`123`),
			expectedErr: "invalid content type (must be string or array of ChatCompletionContentPartTextParam)",
		},
		{
			name:        "invalid type (object)",
			data:        []byte(`{"key": "value"}`),
			expectedErr: "invalid content type (must be string or array of ChatCompletionContentPartTextParam)",
		},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			var p ContentUnion
			err := p.UnmarshalJSON(tc.data)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestContentUnionMarshal(t *testing.T) {
	for _, tc := range contentUnionBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.expected)
			require.NoError(t, err)
			require.JSONEq(t, string(tc.data), string(data))
		})
	}
}

func TestEmbeddingRequestInputUnmarshal(t *testing.T) {
	for _, tc := range embeddingRequestInputBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			var p EmbeddingRequestInput
			err := p.UnmarshalJSON(tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, p.Value)
		})
	}

	// Test error cases for EmbeddingRequestInput
	errorCases := []struct {
		name        string
		data        []byte
		expectedErr string
	}{
		{
			name:        "truncated data",
			data:        []byte{},
			expectedErr: "truncated input data",
		},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			var p EmbeddingRequestInput
			err := p.UnmarshalJSON(tc.data)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestEmbeddingRequestInputMarshal(t *testing.T) {
	for _, tc := range embeddingRequestInputBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.expected)
			require.NoError(t, err)
			require.JSONEq(t, string(tc.data), string(data))
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
						OfText: &ChatCompletionContentPartTextParam{
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

func TestChatCompletionResponseFormatUnionMarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    ChatCompletionResponseFormatUnion
		expected string
	}{
		{
			name: "text",
			input: ChatCompletionResponseFormatUnion{
				OfText: &ChatCompletionResponseFormatTextParam{
					Type: "text",
				},
			},
			expected: `{"type":"text"}`,
		},
		{
			name: "json schema",
			input: ChatCompletionResponseFormatUnion{
				OfJSONSchema: &ChatCompletionResponseFormatJSONSchema{
					Type: "json_schema",
					JSONSchema: ChatCompletionResponseFormatJSONSchemaJSONSchema{
						Name:   "math_response",
						Strict: true,
						Schema: json.RawMessage(`{ "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }`),
					},
				},
			},

			expected: `{"json_schema": { "name": "math_response", "schema": { "type": "object", "properties": { "step": {"type": "string"} }, "required": [ "steps"], "additionalProperties": false }, "strict": true }, "type":"json_schema"}`,
		},
		{
			name: "json object",
			input: ChatCompletionResponseFormatUnion{
				OfJSONObject: &ChatCompletionResponseFormatJSONObjectParam{
					Type: "json_object",
				},
			},
			expected: `{"type":"json_object"}`,
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
				OfText: &ChatCompletionContentPartTextParam{
					Type: "text",
					Text: "Hello world",
				},
			},
			expected: `{"text":"Hello world","type":"text"}`,
		},
		{
			name: "image content",
			input: ChatCompletionContentPartUserUnionParam{
				OfImageURL: &ChatCompletionContentPartImageParam{
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
				OfInputAudio: &ChatCompletionContentPartInputAudioParam{
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
				OfSystem: &ChatCompletionSystemMessageParam{
					Role:    ChatMessageRoleSystem,
					Content: ContentUnion{Value: "You are helpful"},
				},
			},
			{
				OfUser: &ChatCompletionUserMessageParam{
					Role: ChatMessageRoleUser,
					Content: StringOrUserRoleContentUnion{
						Value: []ChatCompletionContentPartUserUnionParam{
							{
								OfText: &ChatCompletionContentPartTextParam{
									Type: "text",
									Text: "What's in this image?",
								},
							},
							{
								OfImageURL: &ChatCompletionContentPartImageParam{
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
				Usage: Usage{
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
				Usage: Usage{
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
		{
			name: "response with safety settings",
			response: ChatCompletionResponse{
				ID:      "chatcmpl-safety-test",
				Created: JSONUNIXTime(time.Unix(1755135425, 0)),
				Model:   "gpt-4.1-nano",
				Object:  "chat.completion",
				Choices: []ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: ChatCompletionChoicesFinishReasonStop,
						Message: ChatCompletionResponseChoiceMessage{
							Role:    "assistant",
							Content: ptr.To("This is a safe response"),
							SafetyRatings: []*genai.SafetyRating{
								{
									Category:    genai.HarmCategoryHarassment,
									Probability: genai.HarmProbabilityLow,
								},
								{
									Category:    genai.HarmCategorySexuallyExplicit,
									Probability: genai.HarmProbabilityNegligible,
								},
							},
						},
					},
				},
				Usage: Usage{
					CompletionTokens: 5,
					PromptTokens:     3,
					TotalTokens:      8,
				},
			},
			expected: `{
				"id": "chatcmpl-safety-test",
				"object": "chat.completion",
				"created": 1755135425,
				"model": "gpt-4.1-nano",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "This is a safe response",
						"safety_ratings": [
							{
								"category": "HARM_CATEGORY_HARASSMENT",
								"probability": "LOW"
							},
							{
								"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT",
								"probability": "NEGLIGIBLE"
							}
						]
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 3,
					"completion_tokens": 5,
					"total_tokens": 8
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
						OfUser: &ChatCompletionUserMessageParam{
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
						OfUser: &ChatCompletionUserMessageParam{
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
						OfUser: &ChatCompletionUserMessageParam{
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
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Complete this: Hello"},
						},
					},
				},
				PredictionContent: &PredictionContent{
					Type:    PredictionContentTypeContent,
					Content: ContentUnion{Value: "Hello world!"},
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
						OfUser: &ChatCompletionUserMessageParam{
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
		{
			name: "enterprise search tool",
			jsonStr: `{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Hello with enterprise search!"
					}
				],
				"tools": [
					{
						"type": "enterprise_search"
					}
				]
			}`,
			expected: &ChatCompletionRequest{
				Model: "gemini-1.5-pro",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Hello with enterprise search!"},
						},
					},
				},
				Tools: []Tool{
					{
						Type: ToolTypeEnterpriseWebSearch,
					},
				},
			},
		},
		{
			name: "mixed function and enterprise search tools",
			jsonStr: `{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Mixed tools test"
					}
				],
				"tools": [
					{
						"type": "function",
						"function": {
							"name": "get_weather",
							"description": "Get current weather"
						}
					},
					{
						"type": "enterprise_search"
					}
				]
			}`,
			expected: &ChatCompletionRequest{
				Model: "gemini-1.5-pro",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Mixed tools test"},
						},
					},
				},
				Tools: []Tool{
					{
						Type: ToolTypeFunction,
						Function: &FunctionDefinition{
							Name:        "get_weather",
							Description: "Get current weather",
						},
					},
					{
						Type: ToolTypeEnterpriseWebSearch,
					},
				},
			},
		},
		{
			name: "enterprise search with vendor fields",
			jsonStr: `{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Combined enterprise search and safety settings"
					}
				],
				"tools": [
					{
						"type": "enterprise_search"
					}
				],
				"safetySettings": [
					{
						"category": "HARM_CATEGORY_HARASSMENT",
						"threshold": "BLOCK_ONLY_HIGH"
					}
				]
			}`,
			expected: &ChatCompletionRequest{
				Model: "gemini-1.5-pro",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Combined enterprise search and safety settings"},
						},
					},
				},
				Tools: []Tool{
					{
						Type: ToolTypeEnterpriseWebSearch,
					},
				},
				GCPVertexAIVendorFields: &GCPVertexAIVendorFields{
					SafetySettings: []*genai.SafetySetting{
						{
							Category:  genai.HarmCategoryHarassment,
							Threshold: genai.HarmBlockThresholdBlockOnlyHigh,
						},
					},
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
			name: "streaming chunk with truncated content",
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
					ToolCalls: []ChatCompletionChunkChoiceDeltaToolCall{
						{
							ID:   ptr.To("tooluse_QklrEHKjRu6Oc4BQUfy7ZQ"),
							Type: "function",
							Function: ChatCompletionMessageToolCallFunctionParam{
								Name:      "cosine",
								Arguments: "",
							},
							Index: 0,
						},
					},
				},
			},
			expected: `{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"tooluse_QklrEHKjRu6Oc4BQUfy7ZQ","function":{"arguments":"","name":"cosine"},"type":"function", "index": 0}]}}`,
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
		usage    Usage
		expected string
	}{
		{
			name: "with zero values omitted",
			usage: Usage{
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
			usage: Usage{
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
			usage: Usage{
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
			var decoded Usage
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
			name:     "truncated options",
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

func TestStringOrAssistantRoleContentUnionUnmarshal(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected StringOrAssistantRoleContentUnion
		expErr   string
	}{
		{
			name:  "string value",
			input: `"hello"`,
			expected: StringOrAssistantRoleContentUnion{
				Value: "hello",
			},
		},
		{
			name:  "array of content objects",
			input: `[{"type": "text", "text": "hello from array"}]`,
			expected: StringOrAssistantRoleContentUnion{
				Value: []ChatCompletionAssistantMessageParamContent{
					{
						Type: ChatCompletionAssistantMessageParamContentTypeText,
						Text: ptr.To("hello from array"),
					},
				},
			},
		},
		{
			name:  "single content object",
			input: `{"type": "text", "text": "hello from single object"}`,
			expected: StringOrAssistantRoleContentUnion{
				Value: ChatCompletionAssistantMessageParamContent{
					Type: ChatCompletionAssistantMessageParamContentTypeText,
					Text: ptr.To("hello from single object"),
				},
			},
		},
		{
			name:   "invalid json",
			input:  `12345`,
			expErr: "cannot unmarshal JSON data as string or assistant content parts",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var result StringOrAssistantRoleContentUnion
			err := json.Unmarshal([]byte(tc.input), &result)

			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				return
			}

			require.NoError(t, err)
			if !cmp.Equal(tc.expected, result) {
				t.Errorf("Unmarshal diff(got, expected) = %s\n", cmp.Diff(result, tc.expected))
			}
		})
	}
}

func TestPromptUnionUnmarshal(t *testing.T) {
	for _, tc := range promptUnionBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			var p PromptUnion
			err := p.UnmarshalJSON(tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, p.Value)
		})
	}
	// just one error to avoid replicating tests in unmarshalJSONNestedUnion
	t.Run("error", func(t *testing.T) {
		var p PromptUnion
		err := p.UnmarshalJSON([]byte{})
		require.EqualError(t, err, "truncated prompt data")
	})
}

func TestPromptUnionMarshal(t *testing.T) {
	for _, tc := range promptUnionBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.expected)
			require.NoError(t, err)
			require.JSONEq(t, string(tc.data), string(data))
		})
	}
}

func TestCompletionRequest(t *testing.T) {
	testCases := []struct {
		name     string
		req      CompletionRequest
		expected string
	}{
		{
			name: "basic request",
			req: CompletionRequest{
				Model:       ModelGPT5Nano,
				Prompt:      PromptUnion{Value: "test"},
				MaxTokens:   ptr.To(10),
				Temperature: ptr.To(0.7),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "test",
				"max_tokens": 10,
				"temperature": 0.7
			}`,
		},
		{
			name: "zero temperature is valid and serialized",
			req: CompletionRequest{
				Model:       ModelGPT5Nano,
				Prompt:      PromptUnion{Value: "test"},
				MaxTokens:   ptr.To(10),
				Temperature: ptr.To(0.0),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "test",
				"max_tokens": 10,
				"temperature": 0
			}`,
		},
		{
			name: "nil temperature omitted",
			req: CompletionRequest{
				Model:     ModelGPT5Nano,
				Prompt:    PromptUnion{Value: "test"},
				MaxTokens: ptr.To(10),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "test",
				"max_tokens": 10
			}`,
		},
		{
			name: "zero frequency penalty is valid",
			req: CompletionRequest{
				Model:            ModelGPT5Nano,
				Prompt:           PromptUnion{Value: "test"},
				FrequencyPenalty: ptr.To(0.0),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "test",
				"frequency_penalty": 0
			}`,
		},
		{
			name: "with batch prompts",
			req: CompletionRequest{
				Model:       ModelGPT5Nano,
				Prompt:      PromptUnion{Value: []string{"prompt1", "prompt2"}},
				MaxTokens:   ptr.To(5),
				Temperature: ptr.To(1.0),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": ["prompt1", "prompt2"],
				"max_tokens": 5,
				"temperature": 1.0
			}`,
		},
		{
			name: "with token array",
			req: CompletionRequest{
				Model:     ModelGPT5Nano,
				Prompt:    PromptUnion{Value: []int{1212, 318}},
				MaxTokens: ptr.To(5),
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": [1212, 318],
				"max_tokens": 5
			}`,
		},
		{
			name: "with stream",
			req: CompletionRequest{
				Model:  ModelGPT5Nano,
				Prompt: PromptUnion{Value: "test"},
				Stream: true,
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "test",
				"stream": true
			}`,
		},
		{
			name: "with suffix",
			req: CompletionRequest{
				Model:  ModelGPT5Nano,
				Prompt: PromptUnion{Value: "Once upon a time"},
				Suffix: " and they lived happily ever after.",
			},
			expected: `{
				"model": "gpt-5-nano",
				"prompt": "Once upon a time",
				"suffix": " and they lived happily ever after."
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.req)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(data))

			var decoded CompletionRequest
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.req.Model, decoded.Model)
			if tc.req.Temperature != nil {
				require.NotNil(t, decoded.Temperature)
				require.Equal(t, *tc.req.Temperature, *decoded.Temperature)
			}
		})
	}
}

func TestCompletionResponse(t *testing.T) {
	testCases := []struct {
		name     string
		resp     CompletionResponse
		expected string
	}{
		{
			name: "basic response",
			resp: CompletionResponse{
				ID:      "cmpl-123",
				Object:  "text_completion",
				Created: JSONUNIXTime(time.Unix(1589478378, 0)),
				Model:   ModelGPT5Nano,
				Choices: []CompletionChoice{
					{
						Text:         "\n\nThis is indeed a test",
						Index:        ptr.To(0),
						FinishReason: "length",
					},
				},
				Usage: &Usage{
					PromptTokens:     5,
					CompletionTokens: 7,
					TotalTokens:      12,
				},
			},
			expected: `{
				"id": "cmpl-123",
				"object": "text_completion",
				"created": 1589478378,
				"model": "gpt-5-nano",
				"choices": [{
					"text": "\n\nThis is indeed a test",
					"index": 0,
					"finish_reason": "length"
				}],
				"usage": {
					"prompt_tokens": 5,
					"completion_tokens": 7,
					"total_tokens": 12
				}
			}`,
		},
		{
			name: "with system fingerprint",
			resp: CompletionResponse{
				ID:                "cmpl-456",
				Object:            "text_completion",
				Created:           JSONUNIXTime(time.Unix(1589478378, 0)),
				Model:             ModelGPT5Nano,
				SystemFingerprint: "fp_44709d6fcb",
				Choices: []CompletionChoice{
					{
						Text:         "Response text",
						FinishReason: "stop",
					},
				},
			},
			expected: `{
				"id": "cmpl-456",
				"object": "text_completion",
				"created": 1589478378,
				"model": "gpt-5-nano",
				"system_fingerprint": "fp_44709d6fcb",
				"choices": [{
					"text": "Response text",
					"finish_reason": "stop"
				}]
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.resp)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(data))

			var decoded CompletionResponse
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.resp.ID, decoded.ID)
			require.Equal(t, tc.resp.Model, decoded.Model)
		})
	}
}

func TestCompletionLogprobs(t *testing.T) {
	testCases := []struct {
		name     string
		logprobs CompletionLogprobs
		expected string
	}{
		{
			name: "with logprobs",
			logprobs: CompletionLogprobs{
				Tokens:        []string{"\n", "\n", "This"},
				TokenLogprobs: []float64{-0.1, -0.2, -0.3},
				TopLogprobs: []map[string]float64{
					{"\n": -0.1, " ": -2.3},
					{"\n": -0.2, " ": -2.1},
				},
				TextOffset: []int{0, 1, 2},
			},
			expected: `{
				"tokens": ["\n", "\n", "This"],
				"token_logprobs": [-0.1, -0.2, -0.3],
				"top_logprobs": [
					{"\n": -0.1, " ": -2.3},
					{"\n": -0.2, " ": -2.1}
				],
				"text_offset": [0, 1, 2]
			}`,
		},
		{
			name:     "truncated logprobs omitted",
			logprobs: CompletionLogprobs{},
			expected: `{}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.logprobs)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(data))

			var decoded CompletionLogprobs
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.logprobs, decoded)
		})
	}
}

func TestUsage(t *testing.T) {
	testCases := []struct {
		name     string
		usage    Usage
		expected string
	}{
		{
			name: "with all fields",
			usage: Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
			expected: `{
				"prompt_tokens": 10,
				"completion_tokens": 20,
				"total_tokens": 30
			}`,
		},
		{
			name: "with zero values omitted",
			usage: Usage{
				PromptTokens: 5,
				TotalTokens:  5,
			},
			expected: `{
				"prompt_tokens": 5,
				"total_tokens": 5
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.usage)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, string(data))

			var decoded Usage
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			require.Equal(t, tc.usage, decoded)
		})
	}
}

func TestChatCompletionNamedToolChoice_MarshalUnmarshal(t *testing.T) {
	original := ChatCompletionNamedToolChoice{
		Type: ToolTypeFunction,
		Function: ChatCompletionNamedToolChoiceFunction{
			Name: "my_func",
		},
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var unmarshaled ChatCompletionNamedToolChoice
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	require.Equal(t, original, unmarshaled)
	require.Equal(t, "my_func", unmarshaled.Function.Name)
}

func TestChatCompletionToolChoiceUnion_MarshalUnmarshal(t *testing.T) {
	// Test with string value
	unionStr := ChatCompletionToolChoiceUnion{Value: "auto"}
	dataStr, err := json.Marshal(unionStr)
	require.NoError(t, err)

	var unmarshaledStr ChatCompletionToolChoiceUnion
	err = json.Unmarshal(dataStr, &unmarshaledStr)
	require.NoError(t, err)
	require.Equal(t, "auto", unmarshaledStr.Value)

	// Test with ChatCompletionNamedToolChoice value
	unionObj := ChatCompletionToolChoiceUnion{Value: ChatCompletionNamedToolChoice{
		Type:     ToolTypeFunction,
		Function: ChatCompletionNamedToolChoiceFunction{Name: "my_func"},
	}}
	dataObj, err := json.Marshal(unionObj)
	require.NoError(t, err)

	var unmarshaledObj ChatCompletionToolChoiceUnion
	err = json.Unmarshal(dataObj, &unmarshaledObj)
	require.NoError(t, err)

	// Type assertion for struct value
	namedChoice, ok := unmarshaledObj.Value.(ChatCompletionNamedToolChoice)
	require.True(t, ok)
	require.Equal(t, unionObj.Value, namedChoice)
	require.Equal(t, "my_func", namedChoice.Function.Name)
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
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

// TestResponseModel_GCPVertexAIStreaming tests that GCP Vertex AI streaming returns the request model
// GCP Vertex AI uses deterministic model mapping without virtualization
func TestResponseModel_GCPVertexAIStreaming(t *testing.T) {
	modelName := "gemini-1.5-pro-002"
	translator := NewChatCompletionOpenAIToGCPVertexAITranslator(modelName).(*openAIToGCPVertexAITranslatorV1ChatCompletion)

	// Initialize translator with streaming request
	req := &openai.ChatCompletionRequest{
		Model:  "gemini-1.5-pro",
		Stream: true,
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
	require.True(t, translator.stream)

	// Vertex AI streaming response in JSONL format
	streamResponse := `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}
`

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(streamResponse)), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel) // Returns the request model since no virtualization
	require.Equal(t, uint32(10), tokenUsage.InputTokens)
	require.Equal(t, uint32(5), tokenUsage.OutputTokens)
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	wantBdy := []byte(`{
    "contents": [
        {
            "parts": [
                {
                    "text": "Tell me about AI Gateways"
                }
            ],
            "role": "user"
        }
    ],
    "tools": null,
    "generation_config": {
        "maxOutputTokens": 100,
        "stopSequences": ["stop1", "stop2"],
        "temperature": 0.1
    },
    "system_instruction": {
        "parts": [
            {
                "text": "You are a helpful assistant"
            }
        ]
    }
}
`)

	wantBdyWithTools := []byte(`{
    "contents": [
        {
            "parts": [
                {
                    "text": "What's the weather in San Francisco?"
                }
            ],
            "role": "user"
        }
    ],
    "tools": [
        {
            "functionDeclarations": [
                {
                    "name": "get_weather",
                    "description": "Get the current weather in a given location",
                    "parametersJsonSchema": {
                        "type": "object",
                        "properties": {
                            "location": {
                                "type": "string",
                                "description": "The city and state, e.g. San Francisco, CA"
                            },
                            "unit": {
                                "type": "string",
                                "enum": ["celsius", "fahrenheit"]
                            }
                        },
                        "required": ["location"]
                    }
                }
            ]
        }
    ],
    "generation_config": {},
    "system_instruction": {
        "parts": [
            {
                "text": "You are a helpful assistant"
            }
        ]
    }
}
`)

	wantBdyWithVendorFields := []byte(`{
    "contents": [
        {
            "parts": [
                {
                    "text": "Test with standard fields"
                }
            ],
            "role": "user"
        }
    ],
    "tools": [
        {
            "functionDeclarations": [
                {
                    "name": "test_function",
                    "description": "A test function",
                    "parametersJsonSchema": {
                        "type": "object",
                        "properties": {
                            "param1": {
                                "type": "string"
                            }
                        }
                    }
                }
            ]
        }
    ],
    "generation_config": {
        "maxOutputTokens": 1024,
        "stopSequences": ["stop"],
        "temperature": 0.7,
          "thinkingConfig": {
            "includeThoughts": true,
            "thinkingBudget":  1000
        }
    }
}`)

	wantBdyWithSafetySettingFields := []byte(`{
    "contents": [
        {
            "parts": [
                {
                    "text": "Test with safety setting"
                }
            ],
            "role": "user"
        }
    ],
    "tools": [
        {
            "functionDeclarations": [
                {
                    "name": "test_function",
                    "description": "A test function",
                    "parametersJsonSchema": {
                        "type": "object",
                        "properties": {
                            "param1": {
                                "type": "string"
                            }
                        }
                    }
                }
            ]
        }
    ],
    "generation_config": {
        "maxOutputTokens": 1024,
        "temperature": 0.7
    },
    "safetySettings": [{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_ONLY_HIGH"}]
}`)

	wantBdyWithGuidedChoice := []byte(`{
  "contents": [
    {
      "parts": [
        {
          "text": "Test with guided choice"
        }
      ],
      "role": "user"
    }
  ],
  "tools": [
    {
      "functionDeclarations": [
        {
          "name": "test_function",
          "description": "A test function",
          "parametersJsonSchema": {
            "type": "object",
            "properties": {
              "param1": {
                "type": "string"
              }
            }
          }
        }
      ]
    }
  ],
  "generation_config": {
    "maxOutputTokens": 1024,
    "temperature": 0.7,
    "responseMimeType": "text/x.enum",
    "responseSchema": {
      "enum": [
        "Positive",
        "Negative"
      ],
      "type": "STRING"
    }
  }
}`)

	wantBdyWithGuidedRegex := []byte(`{
  "contents": [
    {
      "parts": [
        {
          "text": "Test with guided regex"
        }
      ],
      "role": "user"
    }
  ],
  "tools": [
    {
      "functionDeclarations": [
        {
          "name": "test_function",
          "description": "A test function",
          "parametersJsonSchema": {
            "type": "object",
            "properties": {
              "param1": {
                "type": "string"
              }
            }
          }
        }
      ]
    }
  ],
  "generation_config": {
    "maxOutputTokens": 1024,
    "temperature": 0.7,
    "responseMimeType": "application/json",
    "responseSchema": {
      "pattern": "\\w+@\\w+\\.com\\n",
      "type": "STRING"
    }
  }
}`)

	tests := []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		input             openai.ChatCompletionRequest
		onRetry           bool
		wantError         bool
		wantHeaderMut     *extprocv3.HeaderMutation
		wantBody          *extprocv3.BodyMutation
	}{
		{
			name: "basic request",
			input: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gemini-pro",
				Temperature: ptr.To(0.1),
				MaxTokens:   ptr.To(int64(100)),
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"stop1", "stop2"},
				},
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			// Since these are stub implementations, we expect nil mutations.
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("258"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdy,
				},
			},
		},
		{
			name: "basic request with streaming",
			input: openai.ChatCompletionRequest{
				Stream:      true,
				Model:       "gemini-pro",
				Temperature: ptr.To(0.1),
				MaxTokens:   ptr.To(int64(100)),
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"stop1", "stop2"},
				},
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			// Since these are stub implementations, we expect nil mutations.
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-pro:streamGenerateContent?alt=sse"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("258"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdy,
				},
			},
		},
		{
			name:              "model name override",
			modelNameOverride: "gemini-flash",
			input: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gemini-pro",
				Temperature: ptr.To(0.1),
				MaxTokens:   ptr.To(int64(100)),
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfStringArray: []string{"stop1", "stop2"},
				},
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			// Since these are stub implementations, we expect nil mutations.
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-flash:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("258"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdy,
				},
			},
		},
		{
			name: "request with tools",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gemini-pro",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ContentUnion{
								Value: "You are a helpful assistant",
							},
							Role: openai.ChatMessageRoleSystem,
						},
					},
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "What's the weather in San Francisco?",
							},
							Role: openai.ChatMessageRoleUser,
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the current weather in a given location",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{
										"type":        "string",
										"description": "The city and state, e.g. San Francisco, CA",
									},
									"unit": map[string]any{
										"type": "string",
										"enum": []string{"celsius", "fahrenheit"},
									},
								},
								"required": []string{"location"},
							},
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("528"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdyWithTools,
				},
			},
		},
		{
			name: "Request with gcp thinking fields",
			input: openai.ChatCompletionRequest{
				Model:       "gemini-1.5-pro",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1024)),
				Stop: openaigo.ChatCompletionNewParamsStopUnion{
					OfString: openaigo.Opt[string]("stop"),
				},
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Test with standard fields"},
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "test_function",
							Description: "A test function",
							Parameters: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"param1": map[string]any{
										"type": "string",
									},
								},
							},
						},
					},
				},
				GCPVertexAIVendorFields: &openai.GCPVertexAIVendorFields{
					GenerationConfig: &openai.GCPVertexAIGenerationConfig{
						ThinkingConfig: &genai.GenerationConfigThinkingConfig{
							IncludeThoughts: true,
							ThinkingBudget:  ptr.To(int32(1000)),
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-1.5-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("406"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdyWithVendorFields,
				},
			},
		},
		{
			name: "Request with gcp safety setting fields",
			input: openai.ChatCompletionRequest{
				Model:       "gemini-1.5-pro",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Test with safety setting"},
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "test_function",
							Description: "A test function",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"param1": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
				GCPVertexAIVendorFields: &openai.GCPVertexAIVendorFields{
					SafetySettings: []*genai.SafetySetting{
						{
							Category:  "HARM_CATEGORY_HARASSMENT",
							Threshold: "BLOCK_ONLY_HIGH",
						},
					},
				},
			},
			onRetry:   false,
			wantError: false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-1.5-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("405"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdyWithSafetySettingFields,
				},
			},
		},
		{
			name: "Request with guided choice fields",
			input: openai.ChatCompletionRequest{
				Model:       "gemini-1.5-pro",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Test with guided choice"},
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "test_function",
							Description: "A test function",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"param1": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
				GuidedChoice: []string{"Positive", "Negative"},
			},
			onRetry:   false,
			wantError: false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-1.5-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("414"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdyWithGuidedChoice,
				},
			},
		},
		{
			name: "Request with guided regex fields",
			input: openai.ChatCompletionRequest{
				Model:       "gemini-1.5-pro",
				Temperature: ptr.To(0.7),
				MaxTokens:   ptr.To(int64(1024)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Role:    openai.ChatMessageRoleUser,
							Content: openai.StringOrUserRoleContentUnion{Value: "Test with guided regex"},
						},
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "test_function",
							Description: "A test function",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"param1": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
				GuidedRegex: "\\w+@\\w+\\.com\\n",
			},
			onRetry:   false,
			wantError: false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-1.5-pro:generateContent"),
						},
					},
					{
						Header: &corev3.HeaderValue{
							Key:      "Content-Length",
							RawValue: []byte("418"),
						},
					},
				},
			},
			wantBody: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: wantBdyWithGuidedRegex,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToGCPVertexAITranslator(tc.modelNameOverride)
			headerMut, bodyMut, err := translator.RequestBody(nil, &tc.input, tc.onRetry)
			if tc.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut, cmpopts.IgnoreUnexported(extprocv3.HeaderMutation{}, corev3.HeaderValueOption{}, corev3.HeaderValue{})); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantBody, bodyMut, bodyMutTransformer(t)); diff != "" {
				t.Errorf("BodyMutation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	tests := []struct {
		name          string
		modelName     string
		headers       map[string]string
		wantError     bool
		wantHeaderMut *extprocv3.HeaderMutation
	}{
		{
			name:      "basic headers",
			modelName: "gemini-pro",
			headers: map[string]string{
				"content-type": "application/json",
			},
			wantError:     false,
			wantHeaderMut: nil,
		},
		// TODO: Add more test cases when implementation is ready.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewChatCompletionOpenAIToGCPVertexAITranslator(tc.modelName)
			headerMut, err := translator.ResponseHeaders(tc.headers)
			if tc.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut, cmpopts.IgnoreUnexported(extprocv3.HeaderMutation{}, corev3.HeaderValueOption{}, corev3.HeaderValue{})); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	tests := []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		respHeaders       map[string]string
		body              string
		stream            bool
		endOfStream       bool
		wantError         bool
		wantHeaderMut     *extprocv3.HeaderMutation
		wantBodyMut       *extprocv3.BodyMutation
		wantTokenUsage    LLMTokenUsage
	}{
		{
			name: "successful response",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body: `{
				"candidates": [
					{
						"content": {
							"parts": [
								{
									"text": "AI Gateways act as intermediaries between clients and LLM services."
								}
							]
						},
						"finishReason": "STOP",
						"safetyRatings": []
					}
				],
				"promptFeedback": {
					"safetyRatings": []
				},
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 15,
					"totalTokenCount": 25,
                    "cachedContentTokenCount": 10,
                    "thoughtsTokenCount": 10
				}
			}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("353")},
				}},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{
    "choices": [
        {
            "finish_reason": "stop",
            "index": 0,
            "message": {
                "content": "AI Gateways act as intermediaries between clients and LLM services.",
                "role": "assistant"
            }
        }
    ],
    "object": "chat.completion",
    "usage": {
        "completion_tokens": 15,
        "completion_tokens_details": {
            "reasoning_tokens": 10
        },
        "prompt_tokens": 10,
        "prompt_tokens_details": {
            "cached_tokens": 10
        },
        "total_tokens": 25
    }
}`),
				},
			},
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  10,
				OutputTokens: 15,
				TotalTokens:  25,
			},
		},
		{
			name: "response with safety ratings",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body: `{
				"candidates": [
					{
						"content": {
							"parts": [
								{
									"text": "This is a safe response from the AI assistant."
								}
							]
						},
						"finishReason": "STOP",
						"safetyRatings": [
							{
								"category": "HARM_CATEGORY_HARASSMENT",
								"probability": "LOW"
							},
							{
								"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT",
								"probability": "NEGLIGIBLE"
							},
							{
								"category": "HARM_CATEGORY_DANGEROUS_CONTENT",
								"probability": "MEDIUM"
							}
						]
					}
				],
				"promptFeedback": {
					"safetyRatings": []
				},
				"usageMetadata": {
					"promptTokenCount": 8,
					"candidatesTokenCount": 12,
					"totalTokenCount": 20
				}
			}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("515")},
				}},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{
    "choices": [
        {
            "finish_reason": "stop",
            "index": 0,
            "message": {
                "content": "This is a safe response from the AI assistant.",
                "role": "assistant",
                "safety_ratings": [
                    {
                        "category": "HARM_CATEGORY_HARASSMENT",
                        "probability": "LOW"
                    },
                    {
                        "category": "HARM_CATEGORY_SEXUALLY_EXPLICIT",
                        "probability": "NEGLIGIBLE"
                    },
                    {
                        "category": "HARM_CATEGORY_DANGEROUS_CONTENT",
                        "probability": "MEDIUM"
                    }
                ]
            }
        }
    ],
    "object": "chat.completion",
    "usage": {
        "completion_tokens": 12,
        "completion_tokens_details": {},
        "prompt_tokens": 8,
        "prompt_tokens_details": {},
        "total_tokens": 20
    }
}`),
				},
			},
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  8,
				OutputTokens: 12,
				TotalTokens:  20,
			},
		},
		{
			name: "empty response",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body:        `{}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("28")},
					},
				},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{"object":"chat.completion"}`),
				},
			},
			wantTokenUsage: LLMTokenUsage{},
		},
		{
			name: "single stream chunk response",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body: `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}

`,
			stream:        true,
			endOfStream:   true,
			wantError:     false,
			wantHeaderMut: nil,
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`data: {"choices":[{"index":0,"delta":{"content":"Hello","role":"assistant"}}],"object":"chat.completion.chunk","usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8,"completion_tokens_details":{},"prompt_tokens_details":{}}}

data: [DONE]
`),
				},
			},
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  5,
				OutputTokens: 3,
				TotalTokens:  8,
			},
		},
		{
			name: "response with model version field",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body: `{
				"modelVersion": "gemini-1.5-pro-002",
				"candidates": [
					{
						"content": {
							"parts": [
								{
									"text": "Response with model version set."
								}
							]
						},
						"finishReason": "STOP",
						"safetyRatings": []
					}
				],
				"promptFeedback": {
					"safetyRatings": []
				},
				"usageMetadata": {
					"promptTokenCount": 6,
					"candidatesTokenCount": 8,
					"totalTokenCount": 14
				}
			}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("306")},
				}},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{
    "choices": [
        {
            "finish_reason": "stop",
            "index": 0,
            "message": {
                "content": "Response with model version set.",
                "role": "assistant"
            }
        }
    ],
    "model": "gemini-1.5-pro-002",
    "object": "chat.completion",
    "usage": {
        "completion_tokens": 8,
        "completion_tokens_details": {},
        "prompt_tokens": 6,
        "prompt_tokens_details": {},
        "total_tokens": 14
    }
}`),
				},
			},
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  6,
				OutputTokens: 8,
				TotalTokens:  14,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := bytes.NewReader([]byte(tc.body))
			translator := openAIToGCPVertexAITranslatorV1ChatCompletion{
				modelNameOverride: tc.modelNameOverride,
				stream:            tc.stream,
			}
			headerMut, bodyMut, tokenUsage, _, err := translator.ResponseBody(tc.respHeaders, reader, tc.endOfStream, nil)
			if tc.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut, cmpopts.IgnoreUnexported(extprocv3.HeaderMutation{}, corev3.HeaderValueOption{}, corev3.HeaderValue{})); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantBodyMut, bodyMut, bodyMutTransformer(t)); diff != "" {
				t.Errorf("BodyMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantTokenUsage, tokenUsage); diff != "" {
				t.Errorf("TokenUsage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_StreamingResponseHeaders(t *testing.T) {
	eventStreamHeaderMutation := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:   "content-type",
					Value: "text/event-stream",
				},
			},
		},
	}

	tests := []struct {
		name            string
		stream          bool
		headers         map[string]string
		wantMutation    *extprocv3.HeaderMutation
		wantContentType string
	}{
		{
			name:         "non-streaming response",
			stream:       false,
			headers:      map[string]string{"content-type": "application/json"},
			wantMutation: nil,
		},
		{
			name:            "streaming response with application/json",
			stream:          true,
			headers:         map[string]string{"content-type": "application/json"},
			wantMutation:    eventStreamHeaderMutation,
			wantContentType: "text/event-stream",
		},
		{
			name:            "streaming response with text/event-stream",
			stream:          true,
			headers:         map[string]string{"content-type": "text/event-stream"},
			wantMutation:    eventStreamHeaderMutation,
			wantContentType: "text/event-stream",
		},
		{
			name:         "streaming response with other content-type",
			stream:       true,
			headers:      map[string]string{"content-type": "text/plain"},
			wantMutation: eventStreamHeaderMutation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := &openAIToGCPVertexAITranslatorV1ChatCompletion{
				stream: tt.stream,
			}

			headerMut, err := translator.ResponseHeaders(tt.headers)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.wantMutation, headerMut, cmpopts.IgnoreUnexported(extprocv3.HeaderMutation{}, corev3.HeaderValueOption{}, corev3.HeaderValue{})); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_StreamingResponseBody(t *testing.T) {
	// Test basic streaming response conversion.
	translator := &openAIToGCPVertexAITranslatorV1ChatCompletion{
		stream: true,
	}

	// Mock GCP streaming response.
	gcpChunk := `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"STOP"}]}`

	headerMut, bodyMut, tokenUsage, _, err := translator.handleStreamingResponse(
		bytes.NewReader([]byte(gcpChunk)),
		false,
		nil,
	)

	require.Nil(t, headerMut)
	require.NoError(t, err)
	require.NotNil(t, bodyMut)
	require.NotNil(t, bodyMut.Mutation)

	// Check that the response is in SSE format.
	body := bodyMut.Mutation.(*extprocv3.BodyMutation_Body).Body
	bodyStr := string(body)
	require.Contains(t, bodyStr, "data: ")
	require.Contains(t, bodyStr, "chat.completion.chunk")
	require.Equal(t, LLMTokenUsage{}, tokenUsage) // No usage in this test chunk.
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_StreamingEndOfStream(t *testing.T) {
	translator := &openAIToGCPVertexAITranslatorV1ChatCompletion{
		stream: true,
	}

	// Test end of stream marker.
	_, bodyMut, _, _, err := translator.handleStreamingResponse(
		bytes.NewReader([]byte("")),
		true,
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, bodyMut)
	require.NotNil(t, bodyMut.Mutation)

	// Check that [DONE] marker is present.
	body := bodyMut.Mutation.(*extprocv3.BodyMutation_Body).Body
	bodyStr := string(body)
	require.Contains(t, bodyStr, "data: [DONE]")
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_parseGCPStreamingChunks(t *testing.T) {
	tests := []struct {
		name         string
		bufferedBody []byte
		input        string
		wantChunks   []genai.GenerateContentResponse
		wantBuffered []byte
	}{
		{
			name:         "single complete chunk",
			bufferedBody: nil,
			input: `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}

`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "Hello"},
								},
							},
						},
					},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     5,
						CandidatesTokenCount: 3,
						TotalTokenCount:      8,
					},
				},
			},
			wantBuffered: []byte(""),
		},
		{
			name:         "multiple complete chunks",
			bufferedBody: nil,
			input: `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":" world"}]}}]}

`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "Hello"},
								},
							},
						},
					},
				},
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: " world"},
								},
							},
						},
					},
				},
			},
			wantBuffered: []byte(""),
		},
		{
			name:         "incomplete chunk at end",
			bufferedBody: nil,
			input: `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}

data: {"candidates":[{"content":{"parts":`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "Hello"},
								},
							},
						},
					},
				},
			},
			wantBuffered: []byte(`{"candidates":[{"content":{"parts":`),
		},
		{
			name:         "buffered data with new complete chunk",
			bufferedBody: []byte(`{"candidates":[{"content":{"parts":`),
			input: `[{"text":"buffered"}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":"new"}]}}]}

`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "buffered"},
								},
							},
						},
					},
				},
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "new"},
								},
							},
						},
					},
				},
			},
			wantBuffered: []byte(""),
		},
		{
			name:         "invalid JSON chunk in middle - ignored",
			bufferedBody: nil,
			input: `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}

data: invalid-json

data: {"candidates":[{"content":{"parts":[{"text":"world"}]}}]}

`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "Hello"},
								},
							},
						},
					},
				},
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "world"},
								},
							},
						},
					},
				},
			},
			wantBuffered: []byte(""),
		},
		{
			name:         "empty input",
			bufferedBody: nil,
			input:        "",
			wantChunks:   nil,
			wantBuffered: []byte(""),
		},
		{
			name:         "chunk without data prefix",
			bufferedBody: nil,
			input: `{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}

`,
			wantChunks: []genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{Text: "Hello"},
								},
							},
						},
					},
				},
			},
			wantBuffered: []byte(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := &openAIToGCPVertexAITranslatorV1ChatCompletion{
				bufferedBody: tc.bufferedBody,
			}

			chunks, err := translator.parseGCPStreamingChunks(strings.NewReader(tc.input))

			require.NoError(t, err)

			// Compare chunks using cmp with options to handle pointer fields.
			if diff := cmp.Diff(tc.wantChunks, chunks,
				cmpopts.IgnoreUnexported(genai.GenerateContentResponse{}),
				cmpopts.IgnoreUnexported(genai.Candidate{}),
				cmpopts.IgnoreUnexported(genai.Content{}),
				cmpopts.IgnoreUnexported(genai.Part{}),
				cmpopts.IgnoreUnexported(genai.UsageMetadata{}),
			); diff != "" {
				t.Errorf("chunks mismatch (-want +got):\n%s", diff)
			}

			// Check buffered body.
			if diff := cmp.Diff(tc.wantBuffered, translator.bufferedBody); diff != "" {
				t.Errorf("buffered body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1ChatCompletion_ResponseError(t *testing.T) {
	tests := []struct {
		name           string
		headers        map[string]string
		body           string
		expectedErrMsg string
		wantError      openai.Error
		description    string
	}{
		{
			name: "JSON error response with complete GCP error structure",
			headers: map[string]string{
				statusHeaderName: "400",
			},
			body: `{
  "error": {
    "code": 400,
    "message": "Invalid JSON payload received. Unknown name \"fake\": Cannot find field.",
    "status": "INVALID_ARGUMENT",
    "details": [
      {
        "@type": "type.googleapis.com/google.rpc.BadRequest",
        "fieldViolations": [
          {
            "description": "Invalid JSON payload received. Unknown name \"fake\": Cannot find field."
          }
        ]
      }
    ]
  }
}`,
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type: "INVALID_ARGUMENT",
					Message: `Error: Invalid JSON payload received. Unknown name "fake": Cannot find field.
Details: [
      {
        "@type": "type.googleapis.com/google.rpc.BadRequest",
        "fieldViolations": [
          {
            "description": "Invalid JSON payload received. Unknown name \"fake\": Cannot find field."
          }
        ]
      }
    ]`,
					Code: ptr.To("400"),
				},
			},
		},
		{
			name: "Plain text error response",
			headers: map[string]string{
				statusHeaderName: "503",
			},
			body: "Service temporarily unavailable",
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    gcpVertexAIBackendError,
					Message: "Service temporarily unavailable",
					Code:    ptr.To("503"),
				},
			},
		},
		{
			name: "Invalid JSON in error response",
			headers: map[string]string{
				statusHeaderName: "400",
			},
			body: `{"error": invalid json}`,
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    gcpVertexAIBackendError,
					Message: `{"error": invalid json}`,
					Code:    ptr.To("400"),
				},
			},
		},
		{
			name: "Empty body handling",
			headers: map[string]string{
				statusHeaderName: "500",
			},
			body:        "", // Empty body to simulate no content.
			description: "Should handle empty body gracefully.",
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    gcpVertexAIBackendError,
					Message: "",
					Code:    ptr.To("500"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := &openAIToGCPVertexAITranslatorV1ChatCompletion{}

			body := strings.NewReader(tt.body)

			headerMutation, bodyMutation, err := translator.ResponseError(tt.headers, body)

			if tt.expectedErrMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrMsg)
				return
			}

			require.NoError(t, err)
			if tt.description != "" {
				require.NoError(t, err, tt.description)
			}
			require.NotNil(t, bodyMutation)
			require.NotNil(t, headerMutation)

			// Verify that the body mutation contains a valid OpenAI error response.
			var openaiError openai.Error
			bodyBytes := bodyMutation.GetBody()
			err = json.Unmarshal(bodyBytes, &openaiError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.wantError, openaiError); diff != "" {
				t.Errorf("OpenAI error mismatch (-want +got):\n%s", diff)
			}

			// Verify header mutation contains content-length header.
			foundContentLength := slices.ContainsFunc(
				headerMutation.SetHeaders,
				func(header *corev3.HeaderValueOption) bool { return header.Header.Key == httpHeaderKeyContentLength },
			)
			assert.True(t, foundContentLength, "content-length header should be set")
		})
	}
}

func bodyMutTransformer(_ *testing.T) cmp.Option {
	return cmp.Transformer("BodyMutationsToBodyBytes", func(bm *extprocv3.BodyMutation) map[string]any {
		if bm == nil {
			return nil
		}

		var bdy map[string]any
		if body, ok := bm.Mutation.(*extprocv3.BodyMutation_Body); ok {
			if err := json.Unmarshal(body.Body, &bdy); err != nil {
				// The response body may not be valid JSON for streaming requests.
				return map[string]any{
					"BodyMutation": string(body.Body),
				}
			}
			return bdy
		}
		return nil
	})
}

// TestResponseModel_GCPVertexAI tests that GCP Vertex AI returns the request model (no response field)
func TestResponseModel_GCPVertexAI(t *testing.T) {
	modelName := "gemini-1.5-pro-002"
	translator := NewChatCompletionOpenAIToGCPVertexAITranslator(modelName)

	// Initialize translator with the model
	req := &openai.ChatCompletionRequest{
		Model: "gemini-1.5-pro",
	}
	reqBody, _ := json.Marshal(req)
	_, _, err := translator.RequestBody(reqBody, req, false)
	require.NoError(t, err)

	// Vertex AI response doesn't have model field
	vertexResponse := `{
		"candidates": [{
			"content": {
				"parts": [{"text": "Hello"}],
				"role": "model"
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"candidatesTokenCount": 5,
			"totalTokenCount": 15
		}
	}`

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(vertexResponse)), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel) // Returns the request model
	require.Equal(t, uint32(10), tokenUsage.InputTokens)
	require.Equal(t, uint32(5), tokenUsage.OutputTokens)
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

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

	tests := []struct {
		name              string
		modelNameOverride string
		input             openai.ChatCompletionRequest
		onRetry           bool
		wantError         bool
		wantHeaderMut     *extprocv3.HeaderMutation
		wantBody          *extprocv3.BodyMutation
	}{
		{
			name: "basic request",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gemini-pro",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: "You are a helpful assistant",
							},
						},
						Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
						},
						Type: openai.ChatMessageRoleUser,
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
							RawValue: []byte("185"),
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
				Stream: true,
				Model:  "gemini-pro",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: "You are a helpful assistant",
							},
						},
						Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
						},
						Type: openai.ChatMessageRoleUser,
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
							RawValue: []byte("185"),
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
				Stream: false,
				Model:  "gemini-pro",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: "You are a helpful assistant",
							},
						},
						Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Tell me about AI Gateways",
							},
						},
						Type: openai.ChatMessageRoleUser,
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
							RawValue: []byte("185"),
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
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: "You are a helpful assistant",
							},
						},
						Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "What's the weather in San Francisco?",
							},
						},
						Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: openai.ToolTypeFunction,
						Function: &openai.FunctionDefinition{
							Name:        "get_weather",
							Description: "Get the current weather in a given location",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "The city and state, e.g. San Francisco, CA",
									},
									"unit": map[string]interface{}{
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
		modelNameOverride string
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
					"totalTokenCount": 25
				}
			}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("270")},
				}},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{
    "choices": [
        {
            "finish_reason": "stop",
            "index": 0,
            "logprobs": {},
            "message": {
                "content": "AI Gateways act as intermediaries between clients and LLM services.",
                "role": "assistant"
            }
        }
    ],
    "object": "chat.completion",
    "usage": {
        "completion_tokens": 15,
        "prompt_tokens": 10,
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
						Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("39")},
					},
				},
			},
			wantBodyMut: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{"object":"chat.completion","usage":{}}`),
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
					Body: []byte(`data: {"choices":[{"delta":{"content":"Hello","role":"assistant"}}],"object":"chat.completion.chunk","usage":{"completion_tokens":3,"prompt_tokens":5,"total_tokens":8}}

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := bytes.NewReader([]byte(tc.body))
			translator := openAIToGCPVertexAITranslatorV1ChatCompletion{
				modelNameOverride: tc.modelNameOverride,
				stream:            tc.stream,
			}
			headerMut, bodyMut, tokenUsage, err := translator.ResponseBody(tc.respHeaders, reader, tc.endOfStream)
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

	headerMut, bodyMut, tokenUsage, err := translator.handleStreamingResponse(
		map[string]string{},
		bytes.NewReader([]byte(gcpChunk)),
		false,
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
	_, bodyMut, _, err := translator.handleStreamingResponse(
		map[string]string{},
		bytes.NewReader([]byte("")),
		true,
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

func bodyMutTransformer(_ *testing.T) cmp.Option {
	return cmp.Transformer("BodyMutationsToBodyBytes", func(bm *extprocv3.BodyMutation) map[string]interface{} {
		if bm == nil {
			return nil
		}

		var bdy map[string]interface{}
		if body, ok := bm.Mutation.(*extprocv3.BodyMutation_Body); ok {
			if err := json.Unmarshal(body.Body, &bdy); err != nil {
				// The response body may not be valid JSON for streaming requests.
				return map[string]interface{}{
					"BodyMutation": string(body.Body),
				}
			}
			return bdy
		}
		return nil
	})
}

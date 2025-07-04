// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	tests := []struct {
		name          string
		input         openai.ChatCompletionRequest
		onRetry       bool
		wantError     bool
		wantHeaderMut *extprocv3.HeaderMutation
		wantBody      *extprocv3.BodyMutation
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
	}

	translator := NewChatCompletionOpenAIToGCPVertexAITranslator()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
		headers       map[string]string
		wantError     bool
		wantHeaderMut *extprocv3.HeaderMutation
	}{
		{
			name: "basic headers",
			headers: map[string]string{
				"content-type": "application/json",
			},
			wantError:     false,
			wantHeaderMut: nil,
		},
		// TODO: Add more test cases when implementation is ready.
	}

	translator := NewChatCompletionOpenAIToGCPVertexAITranslator()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
		name           string
		respHeaders    map[string]string
		body           string
		endOfStream    bool
		wantError      bool
		wantHeaderMut  *extprocv3.HeaderMutation
		wantBodyMut    *extprocv3.BodyMutation
		wantTokenUsage LLMTokenUsage
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
				"usage": {
					"promptTokens": 10,
					"candidatesTokens": 15,
					"totalTokens": 25
				}
			}`,
			endOfStream: true,
			wantError:   false,
			wantHeaderMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{{
					Header: &corev3.HeaderValue{Key: "Content-Length", RawValue: []byte("211")},
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
    "usage": {}
}`),
				},
			},
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name: "empty response",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body:           `{}`,
			endOfStream:    true,
			wantError:      false,
			wantHeaderMut:  nil,
			wantBodyMut:    nil,
			wantTokenUsage: LLMTokenUsage{},
		},
	}

	translator := NewChatCompletionOpenAIToGCPVertexAITranslator()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := bytes.NewReader([]byte(tc.body))
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

func bodyMutTransformer(t *testing.T) cmp.Option {
	return cmp.Transformer("BodyMutationsToBodyBytes", func(bm *extprocv3.BodyMutation) map[string]interface{} {
		if bm == nil {
			return nil
		}

		var bdy map[string]interface{}
		if body, ok := bm.Mutation.(*extprocv3.BodyMutation_Body); ok {
			if err := json.Unmarshal(body.Body, &bdy); err != nil {
				t.Errorf("error unmarshaling body: %v", err)
				return nil
			}
			return bdy
		}
		return nil
	})
}

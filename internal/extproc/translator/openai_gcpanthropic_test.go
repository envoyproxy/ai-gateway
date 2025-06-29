// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	defaultHeaderMut := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      ":path",
					RawValue: []byte("publishers/anthropic/models/claude-3:generateContent"),
				},
			},
		},
	}

	tests := []struct {
		name          string
		raw           []byte
		input         *openai.ChatCompletionRequest
		onRetry       bool
		wantError     bool
		wantHeaderMut *extprocv3.HeaderMutation
		wantBodyMut   *extprocv3.BodyMutation
	}{
		{
			name: "basic request",
			input: &openai.ChatCompletionRequest{
				Stream: false,
				Model:  "claude-3",
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
			wantError:     false,
			wantHeaderMut: defaultHeaderMut,
			wantBodyMut:   nil,
		},
		{
			name: "streaming request",
			input: &openai.ChatCompletionRequest{
				Stream: true,
				Model:  "claude-3",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "Explain streaming responses",
							},
						},
						Type: openai.ChatMessageRoleUser,
					},
				},
			},
			wantError:     false,
			wantHeaderMut: defaultHeaderMut,
			wantBodyMut:   nil,
		},
		{
			name:          "retry request",
			input:         &openai.ChatCompletionRequest{Model: "claude-3"},
			onRetry:       true,
			wantError:     false,
			wantHeaderMut: defaultHeaderMut,
			wantBodyMut:   nil,
		},
	}

	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			headerMut, bodyMut, err := translator.RequestBody(tc.raw, tc.input, tc.onRetry)

			if tc.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut, cmpopts.IgnoreUnexported(extprocv3.HeaderMutation{}, corev3.HeaderValueOption{}, corev3.HeaderValue{})); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantBodyMut, bodyMut); diff != "" {
				t.Errorf("BodyMutation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	tests := []struct {
		name          string
		headers       map[string]string
		wantError     bool
		wantHeaderMut *extprocv3.HeaderMutation
	}{
		{
			name:          "empty headers",
			headers:       map[string]string{},
			wantError:     false,
			wantHeaderMut: nil,
		},
		{
			name: "with content-type",
			headers: map[string]string{
				"content-type": "application/json",
			},
			wantError:     false,
			wantHeaderMut: nil,
		},
		{
			name: "with status",
			headers: map[string]string{
				":status": "200",
			},
			wantError:     false,
			wantHeaderMut: nil,
		},
	}

	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			headerMut, err := translator.ResponseHeaders(tc.headers)

			if tc.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToGCPAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
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
				"id": "resp-1234567890",
				"model": "claude-3-opus-20240229",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "AI Gateways act as intermediaries between clients and LLM services."
					}
				],
				"usage": {
					"input_tokens": 10,
					"output_tokens": 15
				}
			}`,
			endOfStream:   true,
			wantError:     false,
			wantHeaderMut: nil,
			wantBodyMut:   nil,
			wantTokenUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name: "streaming chunk",
			respHeaders: map[string]string{
				"content-type": "application/json",
			},
			body: `{
				"type": "content_block_delta",
				"index": 0,
				"delta": {
					"type": "text_delta",
					"text": "AI"
				}
			}`,
			endOfStream:    false,
			wantError:      false,
			wantHeaderMut:  nil,
			wantBodyMut:    nil,
			wantTokenUsage: LLMTokenUsage{},
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

	translator := NewChatCompletionOpenAIToGCPAnthropicTranslator()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := bytes.NewReader([]byte(tc.body))

			headerMut, bodyMut, tokenUsage, err := translator.ResponseBody(tc.respHeaders, reader, tc.endOfStream)

			if tc.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantHeaderMut, headerMut); diff != "" {
				t.Errorf("HeaderMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantBodyMut, bodyMut); diff != "" {
				t.Errorf("BodyMutation mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantTokenUsage, tokenUsage); diff != "" {
				t.Errorf("TokenUsage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

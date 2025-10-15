// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strconv"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestAnthropicToAnthropic_RequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		original          []byte
		body              anthropicschema.MessagesRequest
		forceBodyMutation bool
		modelNameOverride string

		expRequestModel internalapi.RequestModel
		expBodyMutation *extprocv3.BodyMutation
	}{
		{
			name:              "no mutation",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{"stream": false, "model": "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "",
			expRequestModel:   "claude-2",
			expBodyMutation:   nil,
		},
		{
			name:              "model override",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}], "stream": true}`),
			body:              anthropicschema.MessagesRequest{"stream": true, "model": "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "claude-100.1",
			expRequestModel:   "claude-100.1",
			expBodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{"model":"claude-100.1","messages":[{"role":"user","content":"Hello!"}], "stream": true}`),
				},
			},
		},
		{
			name:              "force mutation",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{"stream": false, "model": "claude-2"},
			forceBodyMutation: true,
			modelNameOverride: "",
			expRequestModel:   "claude-2",
			expBodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewAnthropicToAnthropicTranslator("", tc.modelNameOverride)
			require.NotNil(t, translator)

			headerMutation, bodyMutation, err := translator.RequestBody(tc.original, &tc.body, tc.forceBodyMutation)
			require.NoError(t, err)
			expHeaderMutation := &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("/v1/messages"),
						},
					},
				},
			}
			if bodyMutation != nil {
				expHeaderMutation.SetHeaders = append(expHeaderMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
					Key:      "content-length",
					RawValue: []byte(strconv.Itoa(len(bodyMutation.GetBody()))),
				}})
			}
			require.Equal(t, expHeaderMutation, headerMutation)
			require.Equal(t, tc.expBodyMutation, bodyMutation)

			require.Equal(t, tc.expRequestModel, translator.(*anthropicToAnthropicTranslator).requestModel)
			require.Equal(t, tc.body.GetStream(), translator.(*anthropicToAnthropicTranslator).stream)
		})
	}
}

func TestAnthropicToAnthropic_ResponseHeaders(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)

	headerMutation, err := translator.ResponseHeaders(nil)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestAnthropicToAnthropic_ResponseBody_non_streaming(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)
	const responseBody = `{"model":"claude-sonnet-4-5-20250929","id":"msg_01J5gW6Sffiem6avXSAooZZw","type":"message","role":"assistant","content":[{"type":"text","text":"Hi! 👋 How can I help you today?"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":9,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"service_tier":"standard"}}`

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(responseBody), true)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{
		InputTokens:  9,
		OutputTokens: 16,
		TotalTokens:  25,
	}, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)
}

func TestAnthropicToAnthropic_ResponseBody_streaming(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)
	translator.(*anthropicToAnthropicTranslator).stream = true

	// We split the response into two parts to simulate streaming where each part can end in the
	// middle of an event.
	const responseHead = `event: message_start
data: {"type":"message_start","message":{"model":"claude-sonnet-4-5-20250929","id":"msg_01BfvfMsg2gBzwsk6PZRLtDg","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":9,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard"}}    }

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}      }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}           }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"typ`
	const responseTail = `
e":"text_delta","text":"! 👋 How"}      }

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" can I help you today?"}   }

event: content_block_stop
data: {"type":"content_block_stop","index":0             }

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":9,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":16}               }

event: message_stop
data: {"type":"message_stop"       }`

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(responseHead), false)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{}, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)

	headerMutation, bodyMutation, tokenUsage, responseModel, err = translator.ResponseBody(nil, strings.NewReader(responseTail), false)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{
		InputTokens:  9,
		OutputTokens: 16,
		TotalTokens:  25,
	}, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)
}

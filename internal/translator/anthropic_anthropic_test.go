// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestAnthropicToAnthropic_RequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		original          []byte
		body              anthropicschema.MessagesRequest
		forceBodyMutation bool
		modelNameOverride string
		prefix            string

		expRequestModel internalapi.RequestModel
		expNewBody      []byte
		expPath         string
	}{
		{
			name:              "no mutation",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Stream: false, Model: "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "",
			prefix:            "v1",
			expRequestModel:   "claude-2",
			expNewBody:        nil,
			expPath:           "/v1/messages",
		},
		{
			name:              "model override",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}], Stream: true}`),
			body:              anthropicschema.MessagesRequest{Stream: true, Model: "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "claude-100.1",
			prefix:            "v1",
			expRequestModel:   "claude-100.1",
			expNewBody:        []byte(`{"model":"claude-100.1","messages":[{"role":"user","content":"Hello!"}], Stream: true}`),
			expPath:           "/v1/messages",
		},
		{
			name:              "force mutation",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Stream: false, Model: "claude-2"},
			forceBodyMutation: true,
			modelNameOverride: "",
			prefix:            "v1",
			expRequestModel:   "claude-2",
			expNewBody:        []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			expPath:           "/v1/messages",
		},
		{
			name:              "empty prefix yields /messages",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Stream: false, Model: "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "",
			prefix:            "",
			expRequestModel:   "claude-2",
			expNewBody:        nil,
			expPath:           "/messages",
		},
		{
			name:              "custom prefix",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Stream: false, Model: "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "",
			prefix:            "gateway/v1",
			expRequestModel:   "claude-2",
			expNewBody:        nil,
			expPath:           "/gateway/v1/messages",
		},
		{
			name:              "custom prefix with leading slash",
			original:          []byte(`{"model":"claude-2","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Stream: false, Model: "claude-2"},
			forceBodyMutation: false,
			modelNameOverride: "",
			prefix:            "/custom",
			expRequestModel:   "claude-2",
			expNewBody:        nil,
			expPath:           "/custom/messages",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewAnthropicToAnthropicTranslator(tc.prefix, tc.modelNameOverride)
			require.NotNil(t, translator)

			headerMutation, bodyMutation, err := translator.RequestBody(tc.original, &tc.body, tc.forceBodyMutation)
			require.NoError(t, err)
			expHeaders := []internalapi.Header{
				{pathHeaderName, tc.expPath},
			}
			if bodyMutation != nil {
				expHeaders = append(expHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(bodyMutation))})
			}
			require.Equal(t, expHeaders, headerMutation)
			require.Equal(t, tc.expNewBody, bodyMutation)

			require.Equal(t, tc.expRequestModel, translator.(*anthropicToAnthropicTranslator).requestModel)
			require.Equal(t, tc.body.Stream, translator.(*anthropicToAnthropicTranslator).stream)
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(responseBody), true, nil)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	expected := tokenUsageFrom(9, 0, 0, 16, 25, -1)
	require.Equal(t, expected, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)
}

func TestAnthropicToAnthropic_ResponseBody_streaming(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)
	translator.(*anthropicToAnthropicTranslator).stream = true

	// We split the response into two parts to simulate streaming where each part can end in the
	// middle of an event.
	const responseHead = `event: message_start
data: {"type":"message_start","message":{"model":"claude-sonnet-4-5-20250929","id":"msg_01BfvfMsg2gBzwsk6PZRLtDg","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":9,"cache_creation_input_tokens":0,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":0,"service_tier":"standard"}}    }

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}      }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}           }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"! 👋 How"}      }

`

	const responseTail = `
event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" can I help you today?"}   }

event: content_block_stop
data: {"type":"content_block_stop","index":0             }

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":16}               }

event: message_stop
data: {"type":"message_stop"       }`

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(responseHead), false, nil)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	expected := tokenUsageFrom(10, 1, 0, 0, 10, -1)
	require.Equal(t, expected, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)

	headerMutation, bodyMutation, tokenUsage, responseModel, err = translator.ResponseBody(nil, strings.NewReader(responseTail), false, nil)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	expected = tokenUsageFrom(10, 1, 0, 16, 26, -1)
	require.Equal(t, expected, tokenUsage)
	require.Equal(t, "claude-sonnet-4-5-20250929", responseModel)
}

// Reproducer for the Sonnet "establish-cache" cache_creation under-count bug.
//
// In production we observed Anthropic Sonnet streaming responses where
// `message_start.usage.cache_creation_input_tokens` was 0 (cache write not yet
// finalized at message_start) and the actual final cumulative count appeared
// only in `message_delta.usage.cache_creation_input_tokens`. The translator
// previously read only OutputTokens from message_delta, silently dropping the
// final cache_creation value and emitting cache_creation=0 to the histogram.
//
// This test simulates that exact pattern and asserts the translator captures
// the cumulative cache_creation_input_tokens reported in message_delta.
func TestAnthropicToAnthropic_ResponseBody_streaming_establishCache(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)
	translator.(*anthropicToAnthropicTranslator).stream = true

	// message_start: input_tokens=3 (regular), cache_creation=0 (cache write
	// not yet finalized), cache_read=0, output=1. This is the "establish-cache"
	// pattern observed for short Sonnet requests where the cache is being
	// written DURING processing.
	const responseHead = `event: message_start
data: {"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_01EstablishCacheBugRepro","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}    }

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}      }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}           }

`

	// message_delta: cumulative final usage. cache_creation_input_tokens=59234
	// is the FINAL count after the cache was fully written. Per Anthropic
	// docs, this is cumulative — the translator MUST absorb it.
	const responseTail = `
event: content_block_stop
data: {"type":"content_block_stop","index":0             }

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":3,"cache_creation_input_tokens":59234,"cache_read_input_tokens":0,"output_tokens":151}               }

event: message_stop
data: {"type":"message_stop"       }`

	// First batch: only message_start parsed. cache_creation=0 (correctly).
	_, _, tokenUsage, _, err := translator.ResponseBody(nil, strings.NewReader(responseHead), false, nil)
	require.NoError(t, err)
	expectedAfterHead := tokenUsageFrom(3, 0, 0, 1, 4, -1)
	require.Equal(t, expectedAfterHead, tokenUsage,
		"after message_start: input=3 (regular), cache_create=0 (not yet written), output=1")

	// Second batch: message_delta parsed with cumulative cache_creation=59234
	// and output=151. The translator MUST update cache_creation_input_tokens
	// from message_delta — this is what was previously broken and silently
	// emitted as 0.
	_, _, tokenUsage, _, err = translator.ResponseBody(nil, strings.NewReader(responseTail), true, nil)
	require.NoError(t, err)
	// Expected: input gross = 3 (regular) + 0 (cache_read) + 59234 (cache_create) = 59237
	//           cache_read = 0
	//           cache_create = 59234
	//           output = 151
	//           total = 59237 + 151 = 59388
	expectedFinal := tokenUsageFrom(59237, 0, 59234, 151, 59388, -1)
	require.Equal(t, expectedFinal, tokenUsage,
		"after message_delta: cumulative cache_creation_input_tokens MUST be captured")
}

// Validates that when message_delta omits the cache fields (only updates
// output_tokens, the most common case), the translator does NOT reset the
// cache values that were correctly set from message_start.
func TestAnthropicToAnthropic_ResponseBody_streaming_messageDelta_preservesMessageStartCache(t *testing.T) {
	translator := NewAnthropicToAnthropicTranslator("", "")
	require.NotNil(t, translator)
	translator.(*anthropicToAnthropicTranslator).stream = true

	// message_start with full cache values (cache hit + small cache write).
	const responseHead = `event: message_start
data: {"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_01CacheHit","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"cache_creation_input_tokens":1000,"cache_read_input_tokens":50000,"output_tokens":1}}    }

`

	// message_delta only carries the cumulative output_tokens. Cache fields
	// are 0 (omitted) — translator must preserve the message_start values.
	const responseTail = `
event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":250}               }

event: message_stop
data: {"type":"message_stop"       }`

	_, _, tokenUsage, _, err := translator.ResponseBody(nil, strings.NewReader(responseHead), false, nil)
	require.NoError(t, err)
	expectedAfterHead := tokenUsageFrom(51005, 50000, 1000, 1, 51006, -1)
	require.Equal(t, expectedAfterHead, tokenUsage,
		"after message_start: input gross = 5+50000+1000 = 51005")

	_, _, tokenUsage, _, err = translator.ResponseBody(nil, strings.NewReader(responseTail), true, nil)
	require.NoError(t, err)
	// Cache fields preserved from message_start; output updated from message_delta.
	expectedFinal := tokenUsageFrom(51005, 50000, 1000, 250, 51255, -1)
	require.Equal(t, expectedFinal, tokenUsage,
		"after message_delta with output-only usage: cache fields MUST be preserved (not reset to 0)")
}

func TestAnthropicToAnthropic_ResponseError(t *testing.T) {
	t.Run("json error", func(t *testing.T) {
		translator := NewAnthropicToAnthropicTranslator("", "")
		require.NotNil(t, translator)
		hdrs, body, err := translator.ResponseError(map[string]string{
			"content-type": "application/json",
		}, strings.NewReader(`{"error":{"code":"invalid_request_error","message":"The model 'claude-unknown' does not exist."}}`))
		require.Nil(t, hdrs)
		require.Nil(t, body)
		require.NoError(t, err)
	})
	for _, tc := range []struct {
		statusCode int
		expType    string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{429, "rate_limit_error"},
		{500, "internal_server_error"},
		{503, "service_unavailable_error"},
	} {
		t.Run("non-json error "+strconv.Itoa(tc.statusCode), func(t *testing.T) {
			translator := NewAnthropicToAnthropicTranslator("", "")
			require.NotNil(t, translator)
			hdrs, body, err := translator.ResponseError(map[string]string{
				"content-type": "text/plain",
				":status":      strconv.Itoa(tc.statusCode),
			}, strings.NewReader("Some error occurred"))
			require.NoError(t, err)
			require.Len(t, hdrs, 2)
			require.Equal(t, "application/json", hdrs[0].Value())
			require.Equal(t, strconv.Itoa(len(body)), hdrs[1].Value())
			var resp anthropicschema.ErrorResponse
			err = json.Unmarshal(body, &resp)
			require.NoError(t, err)
			require.Equal(t, "error", resp.Type)
			require.Equal(t, tc.expType, resp.Error.Type)
			require.Equal(t, "Some error occurred", resp.Error.Message)
		})
	}
}

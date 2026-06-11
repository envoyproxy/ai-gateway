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

func TestCountTokensToAnthropic_RequestBody(t *testing.T) {
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
			name:            "no mutation",
			original:        []byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"Hello!"}]}`),
			body:            anthropicschema.MessagesRequest{Model: "claude-opus-4-1"},
			prefix:          "v1",
			expRequestModel: "claude-opus-4-1",
			expNewBody:      nil,
			expPath:         "/v1/messages/count_tokens",
		},
		{
			name:              "model override",
			original:          []byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Model: "claude-opus-4-1"},
			modelNameOverride: "claude-sonnet-4-5",
			prefix:            "v1",
			expRequestModel:   "claude-sonnet-4-5",
			expNewBody:        []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"Hello!"}]}`),
			expPath:           "/v1/messages/count_tokens",
		},
		{
			name:              "force mutation",
			original:          []byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"Hello!"}]}`),
			body:              anthropicschema.MessagesRequest{Model: "claude-opus-4-1"},
			forceBodyMutation: true,
			prefix:            "v1",
			expRequestModel:   "claude-opus-4-1",
			expNewBody:        []byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"Hello!"}]}`),
			expPath:           "/v1/messages/count_tokens",
		},
		{
			name:            "custom prefix",
			original:        []byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"Hello!"}]}`),
			body:            anthropicschema.MessagesRequest{Model: "claude-opus-4-1"},
			prefix:          "gateway/v1",
			expRequestModel: "claude-opus-4-1",
			expNewBody:      nil,
			expPath:         "/gateway/v1/messages/count_tokens",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewCountTokensToAnthropicTranslator(tc.prefix, tc.modelNameOverride)
			require.NotNil(t, translator)

			headerMutation, bodyMutation, err := translator.RequestBody(tc.original, &tc.body, tc.forceBodyMutation)
			require.NoError(t, err)
			expHeaders := []internalapi.Header{{pathHeaderName, tc.expPath}}
			if bodyMutation != nil {
				expHeaders = append(expHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(bodyMutation))})
			}
			require.Equal(t, expHeaders, headerMutation)
			require.Equal(t, tc.expNewBody, bodyMutation)
			require.Equal(t, tc.expRequestModel, translator.(*countTokensToAnthropicTranslator).requestModel)

			if tc.modelNameOverride != "" {
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(bodyMutation, &parsed))
				require.Equal(t, tc.modelNameOverride, parsed["model"])
			}
		})
	}
}

func TestCountTokensToAnthropic_ResponseBody(t *testing.T) {
	translator := NewCountTokensToAnthropicTranslator("v1", "")
	require.NotNil(t, translator)
	_, _, err := translator.RequestBody(
		[]byte(`{"model":"claude-opus-4-1","messages":[{"role":"user","content":"hello"}]}`),
		&anthropicschema.MessagesRequest{Model: "claude-opus-4-1"},
		false,
	)
	require.NoError(t, err)

	_, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(`{"input_tokens": 42}`), false, nil)
	require.NoError(t, err)
	require.Nil(t, bodyMutation)
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(42), inputTokens)
	require.Equal(t, "claude-opus-4-1", responseModel)
}

func TestCountTokensToAnthropic_ResponseBodyNilBody(t *testing.T) {
	translator := NewCountTokensToAnthropicTranslator("v1", "")
	require.NotNil(t, translator)

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, nil, false, nil)
	require.ErrorContains(t, err, "response body is nil")
	_, ok := tokenUsage.InputTokens()
	require.False(t, ok)
	require.Empty(t, responseModel)
}

func TestCountTokensToAnthropic_ResponseBodyNegativeInputTokens(t *testing.T) {
	translator := NewCountTokensToAnthropicTranslator("v1", "")
	require.NotNil(t, translator)

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, strings.NewReader(`{"input_tokens": -1}`), false, nil)
	require.ErrorContains(t, err, "invalid negative input_tokens: -1")
	_, ok := tokenUsage.InputTokens()
	require.False(t, ok)
	require.Empty(t, responseModel)
}

func TestCountTokensToAnthropic_ResponseError(t *testing.T) {
	translator := NewCountTokensToAnthropicTranslator("v1", "")
	require.NotNil(t, translator)

	hdrs, body, err := translator.ResponseError(nil, nil)
	require.NoError(t, err)
	require.Nil(t, hdrs)
	require.Nil(t, body)
}

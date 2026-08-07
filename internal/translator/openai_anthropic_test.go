// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestOpenAIToAnthropicTranslator_RequestBody(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model:     "claude-requested",
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
			},
		}},
	}

	tr := NewChatCompletionOpenAIToAnthropicTranslator("gateway/v1", "claude-override")
	headers, body, err := tr.RequestBody(nil, req, false)
	require.NoError(t, err)
	require.Contains(t, headers, internalapi.Header{pathHeaderName, "/gateway/v1/messages"})
	require.Contains(t, headers, internalapi.Header{anthropicVersionHeaderName, anthropicDefaultVersion})
	require.Equal(t, "claude-override", gjson.GetBytes(body, "model").String())
	require.False(t, gjson.GetBytes(body, anthropicVersionKey).Exists())

	req.Stream = true
	_, body, err = tr.RequestBody(nil, req, false)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream").Bool())
}

func TestOpenAIToAnthropicTranslator_UsesResponseModel(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model:     "claude-requested",
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Role:    openai.ChatMessageRoleUser,
				Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
			},
		}},
	}

	tr := NewChatCompletionOpenAIToAnthropicTranslator("v1", "")
	_, _, err := tr.RequestBody(nil, req, false)
	require.NoError(t, err)

	response := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-executed","content":[{"type":"text","text":"Hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	_, body, _, model, err := tr.ResponseBody(nil, bytes.NewBufferString(response), true, nil)
	require.NoError(t, err)
	require.Equal(t, "claude-executed", model)
	require.Equal(t, "claude-executed", gjson.GetBytes(body, "model").String())
}

func TestOpenAIToAnthropicTranslator_StreamUsesResponseModel(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model:     "claude-requested",
		Stream:    true,
		MaxTokens: ptr.To(int64(100)),
	}
	tr := NewChatCompletionOpenAIToAnthropicTranslator("v1", "")
	_, _, err := tr.RequestBody(nil, req, false)
	require.NoError(t, err)

	event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-executed\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"
	_, _, _, model, err := tr.ResponseBody(nil, bytes.NewBufferString(event), false, nil)
	require.NoError(t, err)
	require.Equal(t, "claude-executed", model)
}

func TestOpenAIToAnthropicTranslator_RawError(t *testing.T) {
	tr := NewChatCompletionOpenAIToAnthropicTranslator("v1", "")
	_, body, err := tr.ResponseError(map[string]string{statusHeaderName: "503"}, bytes.NewBufferString("unavailable"))
	require.NoError(t, err)
	require.Equal(t, anthropicBackendError, gjson.GetBytes(body, "error.type").String())
}

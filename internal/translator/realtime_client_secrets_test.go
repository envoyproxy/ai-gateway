// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestRealtimeClientSecretsOpenAI_PassThrough(t *testing.T) {
	translator := NewRealtimeClientSecretsOpenAITranslator()

	req := &openai.RealtimeClientSecretRequest{
		ExpiresAfter: &openai.RealtimeClientSecretExpiresAfter{
			Anchor:  "created_at",
			Seconds: 600,
		},
		Session: &openai.RealtimeClientSecretSession{
			Type:         "realtime",
			Model:        "gpt-realtime",
			Instructions: "You are a friendly assistant.",
		},
	}

	headerMutation, bodyMutation, err := translator.RequestBody(req)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)

	respBody := []byte(`{"value":"test_secret","expires_at":1234567890}`)
	headerMutation, bodyMutation, err = translator.ResponseBody(respBody)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

func TestRealtimeClientSecretsGCP_Translation(t *testing.T) {
	translator := NewRealtimeClientSecretsGCPTranslator(nil)

	req := &openai.RealtimeClientSecretRequest{
		ExpiresAfter: &openai.RealtimeClientSecretExpiresAfter{
			Anchor:  "created_at",
			Seconds: 600,
		},
		Session: &openai.RealtimeClientSecretSession{
			Type:         "realtime",
			Model:        "gpt-realtime",
			Instructions: "You are a friendly assistant.",
		},
	}

	headerMutation, bodyMutation, err := translator.RequestBody(req)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	require.Len(t, headerMutation.SetHeaders, 2)
	require.Equal(t, ":path", headerMutation.SetHeaders[0].Header.Key)
	require.Equal(t, "/v1alpha/auth_tokens", string(headerMutation.SetHeaders[0].Header.RawValue))

	var gcpReq gcp.CreateAuthTokenRequest
	err = json.Unmarshal(bodyMutation.GetBody(), &gcpReq)
	require.NoError(t, err)
	require.Equal(t, 1, gcpReq.Uses)
	require.NotEmpty(t, gcpReq.ExpireTime)
}

func TestRealtimeClientSecretsGCP_ResponseTranslation(t *testing.T) {
	translator := NewRealtimeClientSecretsGCPTranslator(nil)

	gcpResp := gcp.CreateAuthTokenResponse{
		Name:       "auth_tokens/test_gcp_token",
		ExpireTime: "2025-01-01T00:00:00Z",
	}
	gcpRespBody, err := json.Marshal(gcpResp)
	require.NoError(t, err)

	headerMutation, bodyMutation, err := translator.ResponseBody(gcpRespBody)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	var openAIResp openai.RealtimeClientSecretResponse
	err = json.Unmarshal(bodyMutation.GetBody(), &openAIResp)
	require.NoError(t, err)
	require.Equal(t, "test_gcp_token", openAIResp.Value)
	require.Equal(t, int64(1735689600), openAIResp.ExpiresAt)
}

func TestRealtimeClientSecretsGCP_DefaultExpiry(t *testing.T) {
	translator := NewRealtimeClientSecretsGCPTranslator(nil)

	req := &openai.RealtimeClientSecretRequest{
		Session: &openai.RealtimeClientSecretSession{
			Type:  "realtime",
			Model: "gpt-realtime",
		},
	}

	headerMutation, bodyMutation, err := translator.RequestBody(req)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	var gcpReq gcp.CreateAuthTokenRequest
	err = json.Unmarshal(bodyMutation.GetBody(), &gcpReq)
	require.NoError(t, err)
	require.NotEmpty(t, gcpReq.ExpireTime)
}

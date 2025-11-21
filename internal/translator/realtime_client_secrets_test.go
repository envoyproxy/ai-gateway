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

func TestRealtimeClientSecretsGemini_Translation(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true, nil)

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

	var geminiReq gcp.CreateAuthTokenRequest
	err = json.Unmarshal(bodyMutation.GetBody(), &geminiReq)
	require.NoError(t, err)
	require.Equal(t, 1, geminiReq.Uses)
	require.NotEmpty(t, geminiReq.ExpireTime)
}

func TestRealtimeClientSecretsGemini_ResponseTranslation(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true, nil)

	geminiResp := gcp.CreateAuthTokenResponse{
		Name:       "auth_tokens/test_gemini_token",
		ExpireTime: "2025-01-01T00:00:00Z",
	}
	geminiRespBody, err := json.Marshal(geminiResp)
	require.NoError(t, err)

	headerMutation, bodyMutation, err := translator.ResponseBody(geminiRespBody)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	var openAIResp openai.RealtimeClientSecretResponse
	err = json.Unmarshal(bodyMutation.GetBody(), &openAIResp)
	require.NoError(t, err)
	require.Equal(t, "test_gemini_token", openAIResp.Value)
	require.Equal(t, int64(1735689600), openAIResp.ExpiresAt)
}

func TestRealtimeClientSecretsGemini_DefaultExpiry(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true, nil)

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

	var geminiReq gcp.CreateAuthTokenRequest
	err = json.Unmarshal(bodyMutation.GetBody(), &geminiReq)
	require.NoError(t, err)
	require.NotEmpty(t, geminiReq.ExpireTime)
}


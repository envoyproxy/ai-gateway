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

	respBody := []byte(`{"client_secret":"test_secret","expires_at":1234567890}`)
	headerMutation, bodyMutation, err = translator.ResponseBody(respBody)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

func TestRealtimeClientSecretsGemini_Translation(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true)

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
	require.Equal(t, "/v1alpha/authTokens:create", string(headerMutation.SetHeaders[0].Header.RawValue))

	var geminiReq gcp.CreateAuthTokenRequest
	err = json.Unmarshal(bodyMutation.GetBody(), &geminiReq)
	require.NoError(t, err)
	require.NotNil(t, geminiReq.Config)
	require.Equal(t, 1, geminiReq.Config.Uses)
	require.NotEmpty(t, geminiReq.Config.ExpireTime)
	require.NotEmpty(t, geminiReq.Config.NewSessionExpireTime)
	require.NotNil(t, geminiReq.HTTPOptions)
	require.Equal(t, "v1alpha", geminiReq.HTTPOptions.APIVersion)
}

func TestRealtimeClientSecretsGemini_ResponseTranslation(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true)

	geminiResp := gcp.CreateAuthTokenResponse{
		Token:      "test_gemini_token",
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
	require.Equal(t, "test_gemini_token", openAIResp.ClientSecret)
	require.Equal(t, int64(1735689600), openAIResp.ExpiresAt)
}

func TestRealtimeClientSecretsGemini_DefaultExpiry(t *testing.T) {
	translator := NewRealtimeClientSecretsGeminiTranslator(true)

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
	require.NotNil(t, geminiReq.Config)
	require.NotEmpty(t, geminiReq.Config.ExpireTime)
}


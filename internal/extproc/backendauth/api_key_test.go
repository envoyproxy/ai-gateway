package backendauth

import (
	"os"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func TestNewAPIKeyHandler(t *testing.T) {
	apiKeyFile := t.TempDir() + "/test"

	f, err := os.Create(apiKeyFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	_, err = f.WriteString("test")
	require.NoError(t, err)
	require.NoError(t, f.Sync())

	auth := filterconfig.APIKeyAuth{Filename: apiKeyFile}
	handler, err := newAPIKeyHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestApiKeyHandler_Do(t *testing.T) {
	apiKeyFile := t.TempDir() + "/test"

	f, err := os.Create(apiKeyFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	_, err = f.WriteString("test")
	require.NoError(t, err)
	require.NoError(t, f.Sync())

	auth := filterconfig.APIKeyAuth{Filename: apiKeyFile}
	handler, err := newAPIKeyHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	secret, err := os.ReadFile(auth.Filename)
	require.NoError(t, err)
	require.Equal(t, "test", string(secret))

	requestHeaders := map[string]string{":method": "POST"}
	headerMut := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:   ":path",
				Value: "/model/some-random-model/converse",
			}},
		},
	}
	bodyMut := &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: []byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`),
		},
	}
	err = handler.Do(requestHeaders, headerMut, bodyMut)
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer test", bearerToken)

	require.Len(t, headerMut.SetHeaders, 2)
	require.Equal(t, "Authorization", headerMut.SetHeaders[1].Header.Key)
	require.Equal(t, []byte("Bearer test"), headerMut.SetHeaders[1].Header.GetRawValue())
}

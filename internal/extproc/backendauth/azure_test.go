// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"os"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewAzureHandler_MissingConfigFile(t *testing.T) {
	handler, err := newAzureHandler(&filterapi.AzureAuth{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read azure access token file")
	require.Nil(t, handler)
}

func TestNewAzureHandler(t *testing.T) {
	azureTokenFile := t.TempDir() + "/azureAccessToken"
	file, err := os.Create(azureTokenFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()

	_, err = file.WriteString(" some-access-token \n")
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	auth := filterapi.AzureAuth{Filename: azureTokenFile}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	require.Equal(t, "some-access-token", handler.(*azureHandler).azureAccessToken)
}

func TestNewAzureHandler_Do(t *testing.T) {
	azureTokenFile := t.TempDir() + "/azureAccessToken"
	file, err := os.Create(azureTokenFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()

	_, err = file.WriteString("some-access-token")
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	auth := filterapi.AzureAuth{Filename: azureTokenFile}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	secret, err := os.ReadFile(auth.Filename)
	require.NoError(t, err)
	require.Equal(t, "some-access-token", string(secret))

	requestHeaders := map[string]string{":method": "POST"}
	headerMut := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:   ":path",
					Value: "/model/some-random-model/chat/completion",
				},
			},
		},
	}
	bodyMut := &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: []byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`),
		},
	}

	err = handler.Do(t.Context(), requestHeaders, headerMut, bodyMut)
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer some-access-token", bearerToken)

	require.Len(t, headerMut.SetHeaders, 2)
	require.Equal(t, "Authorization", headerMut.SetHeaders[1].Header.Key)
	require.Equal(t, []byte("Bearer some-access-token"), headerMut.SetHeaders[1].Header.GetRawValue())
}

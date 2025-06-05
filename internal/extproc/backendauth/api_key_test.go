// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewAPIKeyHandler(t *testing.T) {
	auth := filterapi.APIKeyAuth{Key: "test \n"}
	handler, err := newAPIKeyHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)
	// apiKey should be trimmed.
	require.Equal(t, "test", handler.(*apiKeyHandler).apiKey)
}

func TestApiKeyHandler_Do(t *testing.T) {
	auth := filterapi.APIKeyAuth{Key: "test"}
	handler, err := newAPIKeyHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

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
	err = handler.Do(t.Context(), requestHeaders, headerMut, bodyMut)
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer test", bearerToken)

	require.Len(t, headerMut.SetHeaders, 2)
	require.Equal(t, "Authorization", headerMut.SetHeaders[1].Header.Key)
	require.Equal(t, []byte("Bearer test"), headerMut.SetHeaders[1].Header.GetRawValue())
}

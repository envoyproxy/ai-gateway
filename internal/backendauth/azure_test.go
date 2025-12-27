// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestNewAzureHandler(t *testing.T) {
	auth := filterapi.AzureAuth{AccessToken: " some-access-token \n"}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	require.Equal(t, "some-access-token", handler.(*azureHandler).azureAccessToken)
}

func TestNewAzureHandler_Do(t *testing.T) {
	auth := filterapi.AzureAuth{AccessToken: "some-access-token"}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	requestHeaders := map[string]string{":method": "POST", ":path": "/model/some-random-model/chat/completion"}
	headers, err := handler.Do(t.Context(), requestHeaders, []byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`))
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer some-access-token", bearerToken)

	require.Len(t, headers, 1)
	require.Equal(t, "Authorization", headers[0][0])
	require.Equal(t, "Bearer some-access-token", headers[0][1])
}

// investigate how to ruun test foor workload identity cases.
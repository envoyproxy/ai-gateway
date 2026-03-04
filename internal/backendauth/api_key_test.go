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

	requestHeaders := map[string]string{":method": "POST", ":path": "/model/some-random-model/converse"}
	hdrs, err := handler.Do(t.Context(), requestHeaders, nil)
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer test", bearerToken)

	require.Len(t, hdrs, 1)
	require.Equal(t, "Authorization", hdrs[0][0])
	require.Equal(t, "Bearer test", hdrs[0][1])
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

func TestNewAzureTokenProvider(t *testing.T) {
	mockProvider := NewMockTokenProvider("mock-token", time.Now().Add(1*time.Hour), nil)
	_, err := NewAzureTokenProvider(t.Context(), "tenantID", "clientID", mockProvider, policy.TokenRequestOptions{})
	require.NoError(t, err)
}

func TestAzureTokenProvider_GetToken(t *testing.T) {
	t.Run("missing azure scope", func(t *testing.T) {
		mockProvider := NewMockTokenProvider("mock-token", time.Now().Add(1*time.Hour), nil)
		provider, err := NewAzureTokenProvider(t.Context(), "tenantID", "clientID", mockProvider, policy.TokenRequestOptions{})
		require.NoError(t, err)

		tokenExpiry, err := provider.GetToken(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "ClientAssertionCredential.GetToken() requires at least one scope")
		require.Empty(t, tokenExpiry.Token)
		require.True(t, tokenExpiry.ExpiresAt.IsZero())
	})

	t.Run("invalid azure credential info", func(t *testing.T) {
		scopes := []string{"some-azure-scope"}
		mockProvider := NewMockTokenProvider("mock-token", time.Now().Add(1*time.Hour), nil)
		provider, err := NewAzureTokenProvider(t.Context(), "invalidTenantID", "invalidClientID", mockProvider, policy.TokenRequestOptions{Scopes: scopes})
		require.NoError(t, err)

		_, err = provider.GetToken(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "Tenant 'invalidtenantid' not found. Check to make sure you have the correct tenant ID and are signing into the correct cloud.")
	})

	t.Run("azure proxy url", func(t *testing.T) {
		// Set environment variable for the test.
		mockProxyURL := "http://localhost:8888"
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", mockProxyURL)

		opts := GetClientAssertionCredentialOptions()

		require.NotNil(t, opts)
		require.NotNil(t, opts.ClientOptions.Transport)

		// Assert that the transport has a proxy set.
		transport, ok := opts.ClientOptions.Transport.(*http.Client)
		require.True(t, ok)
		require.NotNil(t, transport.Transport)

		// Check the proxy URL (optional, deeper inspection).
		innerTransport, ok := transport.Transport.(*http.Transport)
		require.True(t, ok)
		require.NotNil(t, innerTransport.Proxy)

		req, _ := http.NewRequest("GET", "http://example.com", nil)
		proxyFunc := innerTransport.Proxy
		proxyURL, err := proxyFunc(req)
		require.NoError(t, err)
		require.Equal(t, "http://localhost:8888", proxyURL.String())
	})
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

func TestNewAzureManagedIdentityTokenProvider(t *testing.T) {
	t.Run("system-assigned managed identity", func(t *testing.T) {
		provider, err := NewAzureManagedIdentityTokenProvider(context.Background(), "", policy.TokenRequestOptions{})
		require.NoError(t, err)
		require.NotNil(t, provider)
	})

	t.Run("user-assigned managed identity", func(t *testing.T) {
		provider, err := NewAzureManagedIdentityTokenProvider(context.Background(), "client-id", policy.TokenRequestOptions{})
		require.NoError(t, err)
		require.NotNil(t, provider)
	})
}

func TestNewAzureManagedIdentityTokenProvider_GetToken(t *testing.T) {
	t.Run("missing azure scope", func(t *testing.T) {
		provider, err := NewAzureManagedIdentityTokenProvider(context.Background(), "", policy.TokenRequestOptions{})
		require.NoError(t, err)

		tokenExpiry, err := provider.GetToken(context.Background())
		require.Error(t, err)
		require.Empty(t, tokenExpiry.Token)
		require.True(t, tokenExpiry.ExpiresAt.IsZero())
	})

	// Note: Testing GetToken with actual Azure endpoints is skipped in unit tests
	// as it would require a real Azure environment with managed identity configured.
	// Integration tests should cover the full authentication flow.

	t.Run("azure proxy url", func(t *testing.T) {
		// Set environment variable for the test.
		mockProxyURL := "http://localhost:8888"
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", mockProxyURL)

		opts := GetDefaultAzureCredentialOptions()

		require.NotNil(t, opts)
		require.NotNil(t, opts.Transport)

		// Assert that the transport has a proxy set.
		transport, ok := opts.Transport.(*http.Client)
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

	t.Run("no proxy url set", func(t *testing.T) {
		// Ensure no proxy URL is set.
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", "")

		opts := GetDefaultAzureCredentialOptions()
		require.Nil(t, opts)
	})

	t.Run("invalid proxy url", func(t *testing.T) {
		// Set invalid proxy URL.
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", "://invalid-url")

		opts := GetDefaultAzureCredentialOptions()
		require.Nil(t, opts) // Should return nil when URL parsing fails.
	})
}

func TestGetDefaultAzureCredentialOptions(t *testing.T) {
	t.Run("no proxy configured", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", "")
		opts := GetDefaultAzureCredentialOptions()
		require.Nil(t, opts)
	})

	t.Run("valid proxy configured", func(t *testing.T) {
		proxyURL := "http://proxy.example.com:8080"
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", proxyURL)

		opts := GetDefaultAzureCredentialOptions()
		require.NotNil(t, opts)
		require.NotNil(t, opts.Transport)
	})

	t.Run("invalid proxy url", func(t *testing.T) {
		t.Setenv("AI_GATEWAY_AZURE_PROXY_URL", "://invalid-url")

		opts := GetDefaultAzureCredentialOptions()
		require.Nil(t, opts)
	})
}

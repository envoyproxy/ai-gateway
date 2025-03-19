// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

func TestNewAzureTokenProvider(t *testing.T) {
	_, err := NewAzureTokenProvider("tenantID", "clientID", "", policy.TokenRequestOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "secret can't be empty string")
}

func TestAzureTokenProvider_GetToken(t *testing.T) {
	t.Run("missing azure scope", func(t *testing.T) {
		provider, err := NewAzureTokenProvider("tenantID", "clientID", "clientSecret", policy.TokenRequestOptions{})
		require.NoError(t, err)

		tokenExpiry, err := provider.GetToken(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "ClientSecretCredential.GetToken() requires at least one scope")
		require.Empty(t, tokenExpiry.Token)
		require.True(t, tokenExpiry.ExpiresAt.IsZero())
	})

	t.Run("invalid azure credential info", func(t *testing.T) {
		scopes := []string{"some-azure-scope"}
		provider, err := NewAzureTokenProvider("invalidTenantID", "invalidClientID", "invalidClientSecret", policy.TokenRequestOptions{Scopes: scopes})
		require.NoError(t, err)

		_, err = provider.GetToken(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "Tenant 'invalidtenantid' not found. Check to make sure you have the correct tenant ID and are signing into the correct cloud.")
	})
}

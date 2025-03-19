// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// azureTokenProvider is a provider implements TokenProvider interface for Azure access tokens.
type azureTokenProvider struct {
	credential  *azidentity.ClientSecretCredential
	tokenOption policy.TokenRequestOptions
}

// NewAzureTokenProvider creates a new TokenProvider with the given tenant ID, client ID, client secret, and token request options.
func NewAzureTokenProvider(tenantID, clientID, clientSecret string, tokenOption policy.TokenRequestOptions) (TokenProvider, error) {
	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, err
	}
	return &azureTokenProvider{credential: credential, tokenOption: tokenOption}, nil
}

// GetToken implements TokenProvider.GetToken method to retrieve an Azure access token and its expiration time.
func (a *azureTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	azureToken, err := a.credential.GetToken(ctx, a.tokenOption)
	if err != nil {
		return TokenExpiry{}, err
	}
	return TokenExpiry{Token: azureToken.Token, ExpiresAt: azureToken.ExpiresOn}, nil
}

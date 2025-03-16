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

type AzureTokenProvider struct {
	credential  *azidentity.ClientSecretCredential
	tokenOption policy.TokenRequestOptions
}

func NewAzureTokenProvider(tenantID, clientID, clientSecret string, tokenOption policy.TokenRequestOptions) (*AzureTokenProvider, error) {
	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, err
	}
	return &AzureTokenProvider{credential: credential, tokenOption: tokenOption}, nil
}

func (a *AzureTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	azureToken, err := a.credential.GetToken(ctx, a.tokenOption)
	if err != nil {
		return TokenExpiry{}, err
	}
	return TokenExpiry{Token: azureToken.Token, ExpiresAt: azureToken.ExpiresOn}, nil
}

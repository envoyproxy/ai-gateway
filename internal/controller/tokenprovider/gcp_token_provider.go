// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"

	"golang.org/x/oauth2/google"
)

// gcpTokenProvider is a provider implements TokenProvider interface for GCP access tokens.
type gcpTokenProvider struct {
	credentials *google.Credentials
}

// NewGCPTokenProvider creates a new TokenProvider with GCP service account key JSON string.
func NewGCPTokenProvider(ctx context.Context, gcpCredentialLiteral []byte) (TokenProvider, error) {
	credential, err := google.CredentialsFromJSON(ctx, gcpCredentialLiteral, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	return &gcpTokenProvider{credentials: credential}, nil
}

// GetToken implements TokenProvider.GetToken method to retrieve an GCP access token and its expiration time.
func (a *gcpTokenProvider) GetToken(_ context.Context) (TokenExpiry, error) {
	token, err := a.credentials.TokenSource.Token()
	if err != nil {
		return TokenExpiry{}, err
	}
	return TokenExpiry{
		Token:     token.AccessToken,
		ExpiresAt: token.Expiry,
	}, nil
}

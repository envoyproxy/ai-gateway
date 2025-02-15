// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"
	"fmt"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClientCredentialsTokenProvider implements the standard OAuth2 client credentials flow.
type ClientCredentialsTokenProvider struct {
	client client.Client
}

// NewClientCredentialsProvider creates a new client credentials provider.
func NewClientCredentialsProvider(cl client.Client) *ClientCredentialsTokenProvider {
	return &ClientCredentialsTokenProvider{
		client: cl,
	}
}

// FetchToken gets the client secret from the secret reference and fetches the token from the provider token URL.
//
// This implements [TokenProvider.FetchToken].
func (p *ClientCredentialsTokenProvider) FetchToken(ctx context.Context, oidc egv1a1.OIDC) (*oauth2.Token, error) {
	// client secret namespace is optional on egv1a1.OIDC, but it is required for AI Gateway for now.
	if oidc.ClientSecret.Namespace == nil {
		return nil, fmt.Errorf("oidc-client-secret namespace is nil")
	}

	clientSecret, err := getClientSecret(ctx, p.client, &corev1.SecretReference{
		Name:      string(oidc.ClientSecret.Name),
		Namespace: string(*oidc.ClientSecret.Namespace),
	})
	if err != nil {
		return nil, err
	}
	return p.getTokenWithClientCredentialConfig(ctx, oidc, clientSecret)
}

// getTokenWithClientCredentialFlow fetches the oauth2 token with client credential config.
func (p *ClientCredentialsTokenProvider) getTokenWithClientCredentialConfig(ctx context.Context, oidc egv1a1.OIDC, clientSecret string) (*oauth2.Token, error) {
	oauth2Config := clientcredentials.Config{
		ClientSecret: clientSecret,
		ClientID:     oidc.ClientID,
		Scopes:       oidc.Scopes,
		TokenURL:     *oidc.Provider.TokenEndpoint,
	}
	token, err := oauth2Config.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("fail to get oauth2 token %w", err)
	}
	// Handle expiration.
	if token.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return token, nil
}

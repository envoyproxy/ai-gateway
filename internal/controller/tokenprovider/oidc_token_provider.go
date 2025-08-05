// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// oidcTokenProvider is a provider implements TokenProvider interface for OIDC tokens.
type oidcTokenProvider struct {
	oidcConfig *egv1a1.OIDC
	client     client.Client
}

// NewOidcTokenProvider creates a new TokenProvider with the given OIDC configuration.
func NewOidcTokenProvider(ctx context.Context, client client.Client, oidcConfig *egv1a1.OIDC) (TokenProvider, error) {
	if oidcConfig == nil {
		return nil, fmt.Errorf("provided oidc config is nil")
	}

	issuerURL := oidcConfig.Provider.Issuer
	oidcProvider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create oidc config: %q, %w", issuerURL, err)
	}
	var config oidc.ProviderConfig
	if err = oidcProvider.Claims(&config); err != nil {
		return nil, fmt.Errorf("failed to decode oidc config claims: %q, %w", issuerURL, err)
	}
	// Unmarshal supported scopes.
	var claims struct {
		SupportedScopes []string `json:"scopes_supported"`
	}
	if err = oidcProvider.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to get scopes_supported field in claim: %w", err)
	}
	// Validate required fields.
	if config.IssuerURL == "" {
		return nil, fmt.Errorf("issuer is required in oidc provider config")
	}
	if config.TokenURL == "" {
		return nil, fmt.Errorf("token_endpoint is required in oidc provider config")
	}

	// Use discovered token endpoint if not explicitly provided.
	if oidcConfig.Provider.TokenEndpoint == nil {
		oidcConfig.Provider.TokenEndpoint = &config.TokenURL
	}
	// Add discovered scopes if available.
	if len(claims.SupportedScopes) > 0 {
		requestedScopes := make(map[string]bool, len(oidcConfig.Scopes))
		for _, scope := range oidcConfig.Scopes {
			requestedScopes[scope] = true
		}

		// Add supported scopes that aren't already requested.
		for _, scope := range claims.SupportedScopes {
			if !requestedScopes[scope] {
				oidcConfig.Scopes = append(oidcConfig.Scopes, scope)
			}
		}
	}
	// Now OidcTokenProvider has all fields configured and is ready for caller to use by calling GetToken(ctx).
	return &oidcTokenProvider{oidcConfig, client}, nil
}

// GetToken implements TokenProvider.GetToken method to retrieve an OIDC token and its expiration time.
func (o *oidcTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	if o.oidcConfig.ClientSecret.Namespace == nil {
		return TokenExpiry{}, fmt.Errorf("oidc client secret namespace is nil")
	}
	clientSecret, err := GetClientSecret(ctx, o.client, &corev1.SecretReference{
		Name:      string(o.oidcConfig.ClientSecret.Name),
		Namespace: string(*o.oidcConfig.ClientSecret.Namespace),
	})
	if err != nil {
		return TokenExpiry{}, err
	}
	oauth2Config := clientcredentials.Config{
		ClientSecret: clientSecret,
		ClientID:     *o.oidcConfig.ClientID,
		Scopes:       o.oidcConfig.Scopes,
	}

	if o.oidcConfig.Provider.TokenEndpoint != nil {
		oauth2Config.TokenURL = *o.oidcConfig.Provider.TokenEndpoint
	}

	// Underlying token call will apply http client timeout.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: time.Minute})

	token, err := oauth2Config.Token(ctx)
	if err != nil {
		return TokenExpiry{}, fmt.Errorf("failed to get oauth2 token: %w", err)
	}
	if token.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return TokenExpiry{Token: token.AccessToken, ExpiresAt: token.Expiry}, nil
}

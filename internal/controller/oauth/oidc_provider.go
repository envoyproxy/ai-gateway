// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
)

// OIDCProvider extends ClientCredentialsTokenProvider with OIDC support.
type OIDCProvider struct {
	tokenProvider  *ClientCredentialsTokenProvider
	oidcCredential egv1a1.OIDC
}

// NewOIDCProvider creates a new OIDC-aware provider.
func NewOIDCProvider(tokenProvider *ClientCredentialsTokenProvider, oidcCredentials egv1a1.OIDC) *OIDCProvider {
	return &OIDCProvider{
		tokenProvider:  tokenProvider,
		oidcCredential: oidcCredentials,
	}
}

// getOIDCProviderConfig retrieves or creates OIDC config for the given issuer URL.
func (p *OIDCProvider) getOIDCProviderConfig(ctx context.Context, issuerURL string) (*oidc.ProviderConfig, []string, error) {
	// Check context before proceeding in case context is cancelled because of timeout.
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context error before discovery: %w", err)
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create go-oidc provider %q: %w", issuerURL, err)
	}

	var config oidc.ProviderConfig
	if err = provider.Claims(&config); err != nil {
		return nil, nil, fmt.Errorf("failed to decode provider config claims %q: %w", issuerURL, err)
	}

	// Unmarshall supported scopes.
	var claims struct {
		SupportedScopes []string `json:"scopes_supported"`
	}
	if err = provider.Claims(&claims); err != nil {
		return nil, nil, fmt.Errorf("failed to decode provider scope supported claims: %w", err)
	}

	// Validate required fields.
	if config.IssuerURL == "" {
		return nil, nil, fmt.Errorf("issuer is required in OIDC provider config")
	}
	if config.TokenURL == "" {
		return nil, nil, fmt.Errorf("token_endpoint is required in OIDC provider config")
	}

	return &config, claims.SupportedScopes, nil
}

// FetchToken retrieves and validates tokens using the client credentials flow with OIDC support.
//
// This implements [TokenProvider.FetchToken].
func (p *OIDCProvider) FetchToken(ctx context.Context) (*oauth2.Token, error) {
	// If issuer URL is provided, fetch OIDC metadata.
	if issuerURL := p.oidcCredential.Provider.Issuer; issuerURL != "" {
		config, supportedScopes, err := p.getOIDCProviderConfig(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to get OIDC config: %w", err)
		}

		// Use discovered token endpoint if not explicitly provided.
		if p.oidcCredential.Provider.TokenEndpoint == nil {
			p.oidcCredential.Provider.TokenEndpoint = &config.TokenURL
		}

		// Add discovered scopes if available.
		if len(supportedScopes) > 0 {
			requestedScopes := make(map[string]bool)
			for _, scope := range p.oidcCredential.Scopes {
				requestedScopes[scope] = true
			}

			// Add supported scopes that aren't already requested.
			for _, scope := range supportedScopes {
				if !requestedScopes[scope] {
					p.oidcCredential.Scopes = append(p.oidcCredential.Scopes, scope)
				}
			}
		}
	}

	// Get base token response.
	token, err := p.tokenProvider.FetchToken(ctx, p.oidcCredential)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return token, nil
}

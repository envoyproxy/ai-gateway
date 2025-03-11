// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

/*
// OIDCProvider extends ClientCredentialsTokenProvider with OIDC support.
type OIDCProvider struct {
	tokenProvider TokenProvider
	// oidcConfig will be in sync with clientCredentialsProvider OIDC.
	oidcConfig *egv1a1.OIDC
}

// NewOIDCProvider creates a new OIDC-aware provider.
func NewOIDCProvider(client client.Client, oidcConfig egv1a1.OIDC) *OIDCProvider {
	return &OIDCProvider{
		tokenProvider: newClientCredentialsProvider(client, &oidcConfig),
		oidcConfig:    &oidcConfig,
	}
}

// getOIDCProviderConfig retrieves or creates OIDC config for the given issuer URL.
func (p *OIDCProvider) getOIDCProviderConfig(ctx context.Context, issuerURL string) (*oidc.ProviderConfig, []string, error) {
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

// GetToken retrieves and validates tokens using the client credentials flow with OIDC support.
//
// This implements [TokenProvider.GetToken].
func (p *OIDCProvider) GetToken(ctx context.Context) (*oauth2.Token, error) {
	// If issuer URL is provided, fetch OIDC metadata.
	if issuerURL := p.oidcConfig.Provider.Issuer; issuerURL != "" {
		config, supportedScopes, err := p.getOIDCProviderConfig(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to get OIDC config: %w", err)
		}

		// Use discovered token endpoint if not explicitly provided.
		if p.oidcConfig.Provider.TokenEndpoint == nil {
			p.oidcConfig.Provider.TokenEndpoint = &config.TokenURL
		}

		// Add discovered scopes if available.
		if len(supportedScopes) > 0 {
			requestedScopes := make(map[string]bool, len(p.oidcConfig.Scopes))
			for _, scope := range p.oidcConfig.Scopes {
				requestedScopes[scope] = true
			}

			// Add supported scopes that aren't already requested.
			for _, scope := range supportedScopes {
				if !requestedScopes[scope] {
					p.oidcConfig.Scopes = append(p.oidcConfig.Scopes, scope)
				}
			}
		}
	}

	// Get token response from the provider.
	token, err := p.tokenProvider.GetToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return token, nil
}

*/

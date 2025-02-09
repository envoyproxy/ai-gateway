package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
)

// OIDCProvider extends ClientCredentialsProvider with OIDC support
type OIDCProvider struct {
	tokenProvider  TokenProvider
	httpClient     *http.Client
	oidcCredential *egv1a1.OIDC
}

// OIDCMetadata represents the OpenID Connect provider metadata
type OIDCMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SupportedScopes       []string `json:"scopes_supported"`
}

// NewOIDCProvider creates a new OIDC-aware provider
func NewOIDCProvider(tokenProvider TokenProvider, oidcCredentials *egv1a1.OIDC) *OIDCProvider {
	return &OIDCProvider{
		tokenProvider:  tokenProvider,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		oidcCredential: oidcCredentials,
	}
}

// getOIDCMetadata retrieves or creates OIDC metadata for the given issuer URL
func (p *OIDCProvider) getOIDCMetadata(ctx context.Context, issuerURL string) (*OIDCMetadata, error) {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before discovery: %w", err)
	}

	// Fetch OIDC configuration
	wellKnown := strings.TrimSuffix(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, "GET", wellKnown, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OIDC metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from discovery endpoint: %d", resp.StatusCode)
	}

	var metadata OIDCMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC metadata: %w", err)
	}

	// Validate required fields
	if metadata.Issuer == "" {
		return nil, fmt.Errorf("issuer is required in OIDC metadata")
	}
	if metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("token_endpoint is required in OIDC metadata")
	}

	return &metadata, nil
}

// FetchToken retrieves and validates tokens using the client credentials flow with OIDC support
func (p *OIDCProvider) FetchToken(ctx context.Context) (*oauth2.Token, error) {
	// If issuer URL is provided, fetch OIDC metadata
	if issuerURL := p.oidcCredential.Provider.Issuer; issuerURL != "" {
		metadata, err := p.getOIDCMetadata(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to get OIDC metadata: %w", err)
		}

		// Use discovered token endpoint if not explicitly provided
		if p.oidcCredential.Provider.TokenEndpoint == nil {
			p.oidcCredential.Provider.TokenEndpoint = &metadata.TokenEndpoint
		}

		// Add discovered scopes if available
		if len(metadata.SupportedScopes) > 0 {
			requestedScopes := make(map[string]bool)
			for _, scope := range p.oidcCredential.Scopes {
				requestedScopes[scope] = true
			}

			// Add supported scopes that aren't already requested
			for _, scope := range metadata.SupportedScopes {
				if !requestedScopes[scope] {
					p.oidcCredential.Scopes = append(p.oidcCredential.Scopes, scope)
				}
			}
		}
	}

	// Get base token response
	token, err := p.tokenProvider.FetchToken(ctx, p.oidcCredential)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return token, nil
}

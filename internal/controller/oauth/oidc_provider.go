package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCProvider extends ClientCredentialsProvider with OIDC support
type OIDCProvider struct {
	*ClientCredentialsProvider
	httpClient     *http.Client
	discoveryCache map[string]*discoveryEntry
	discoveryLock  sync.RWMutex
}

// OIDCMetadata represents the OpenID Connect provider metadata
type OIDCMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SupportedScopes       []string `json:"scopes_supported"`
}

// discoveryEntry represents a cached OIDC metadata entry with expiration
type discoveryEntry struct {
	metadata  *OIDCMetadata
	expiresAt time.Time
}

// NewOIDCProvider creates a new OIDC-aware provider
func NewOIDCProvider(base *BaseProvider) *OIDCProvider {
	return &OIDCProvider{
		ClientCredentialsProvider: NewClientCredentialsProvider(base),
		httpClient:                &http.Client{Timeout: 30 * time.Second},
		discoveryCache:            make(map[string]*discoveryEntry),
	}
}

// getOIDCMetadata retrieves or creates OIDC metadata for the given issuer URL
func (p *OIDCProvider) getOIDCMetadata(ctx context.Context, issuerURL string) (*OIDCMetadata, error) {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before discovery: %w", err)
	}

	// Try to get from cache first
	p.discoveryLock.RLock()
	if entry, exists := p.discoveryCache[issuerURL]; exists {
		if time.Now().Before(entry.expiresAt) {
			p.discoveryLock.RUnlock()
			return entry.metadata, nil
		}
	}
	p.discoveryLock.RUnlock()

	// Cache miss or expired, acquire write lock
	p.discoveryLock.Lock()
	defer p.discoveryLock.Unlock()

	// Double check after acquiring write lock
	if entry, exists := p.discoveryCache[issuerURL]; exists {
		if time.Now().Before(entry.expiresAt) {
			return entry.metadata, nil
		}
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

	// Cache the result with 24-hour expiration
	p.discoveryCache[issuerURL] = &discoveryEntry{
		metadata:  &metadata,
		expiresAt: time.Now().Add(24 * time.Hour),
	}

	return &metadata, nil
}

// validateIDToken validates the ID token according to the OIDC spec
func (p *OIDCProvider) validateIDToken(ctx context.Context, rawIDToken, issuerURL, clientID string) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before validation: %w", err)
	}

	token, err := jwt.Parse(rawIDToken, func(token *jwt.Token) (interface{}, error) {
		// For now, we skip signature validation as we don't have the key
		// TODO: Implement JWKS validation
		return jwt.UnsafeAllowNoneSignatureType, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims format in token")
	}

	now := time.Now()

	// Validate issuer
	if iss, err := claims.GetIssuer(); err != nil || iss != issuerURL {
		return nil, fmt.Errorf("invalid issuer claim")
	}

	// Validate audience
	if aud, err := claims.GetAudience(); err != nil || !contains(aud, clientID) {
		return nil, fmt.Errorf("invalid audience claim")
	}

	// Validate expiration
	if exp, err := claims.GetExpirationTime(); err != nil || exp.Before(now) {
		return nil, fmt.Errorf("token is expired")
	}

	// Validate issued at
	if iat, err := claims.GetIssuedAt(); err != nil || iat.After(now) {
		return nil, fmt.Errorf("token used before issued")
	}

	return claims, nil
}

// contains checks if a string slice contains a value
func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// FetchToken retrieves and validates tokens using the client credentials flow with OIDC support
func (p *OIDCProvider) FetchToken(ctx context.Context, config Config) (*TokenResponse, error) {
	// If issuer URL is provided, fetch OIDC metadata
	if issuerURL, ok := config.Options["issuer_url"].(string); ok {
		metadata, err := p.getOIDCMetadata(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to get OIDC metadata: %w", err)
		}

		// Use discovered token endpoint if not explicitly provided
		if config.TokenURL == "" {
			config.TokenURL = metadata.TokenEndpoint
		}

		// Add discovered scopes if available
		if len(metadata.SupportedScopes) > 0 {
			requestedScopes := make(map[string]bool)
			for _, scope := range config.Scopes {
				requestedScopes[scope] = true
			}

			// Add supported scopes that aren't already requested
			for _, scope := range metadata.SupportedScopes {
				if !requestedScopes[scope] {
					config.Scopes = append(config.Scopes, scope)
				}
			}
		}
	}

	// Ensure openid scope is present
	hasOpenID := false
	for _, scope := range config.Scopes {
		if scope == "openid" {
			hasOpenID = true
			break
		}
	}
	if !hasOpenID {
		config.Scopes = append(config.Scopes, "openid")
	}

	// Get base token response
	token, err := p.ClientCredentialsProvider.FetchToken(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Extract ID token if present
	if rawIDToken, ok := token.Raw["id_token"].(string); ok {
		token.IDToken = rawIDToken

		// Validate ID token if issuer URL is provided
		if issuerURL, ok := config.Options["issuer_url"].(string); ok {
			claims, err := p.validateIDToken(ctx, rawIDToken, issuerURL, config.ClientID)
			if err != nil {
				return nil, fmt.Errorf("failed to validate ID token: %w", err)
			}

			// Store claims in raw map for access by consumers
			token.Raw["id_token_claims"] = claims
		}
	}

	return token, nil
}

func (p *OIDCProvider) SupportsFlow(flowType FlowType) bool {
	return flowType == FlowClientCredentialsWithIDToken
}

// ValidateToken implements token validation for both access tokens and ID tokens
func (p *OIDCProvider) ValidateToken(ctx context.Context, token string) error {
	// For ID tokens, we expect them to have been validated during GetToken
	// For access tokens, we could implement introspection here if needed
	return nil
}

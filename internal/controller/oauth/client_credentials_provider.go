package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// ClientCredentialsProvider implements the standard OAuth2 client credentials flow
type ClientCredentialsProvider struct {
	*BaseProvider
}

// NewClientCredentialsProvider creates a new client credentials provider
func NewClientCredentialsProvider(base *BaseProvider) *ClientCredentialsProvider {
	return &ClientCredentialsProvider{
		BaseProvider: base,
	}
}

func (p *ClientCredentialsProvider) FetchToken(ctx context.Context, oidc *egv1a1.OIDC) (*TokenResponse, error) {
	clientSecret, err := p.getClientSecret(ctx, &corev1.SecretReference{
		Name:      string(oidc.ClientSecret.Name),
		Namespace: string(*oidc.ClientSecret.Namespace),
	})
	if err != nil {
		return nil, err
	}

	// Prepare token request
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", oidc.ClientID)
	form.Set("client_secret", clientSecret)
	if len(oidc.Scopes) > 0 {
		form.Set("scope", strings.Join(oidc.Scopes, " "))
	}

	// Make request
	req, err := http.NewRequestWithContext(ctx, "POST", *oidc.Provider.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to TokenResponse
	token := &TokenResponse{
		Raw: raw,
	}

	// Extract standard fields
	if v, ok := raw["access_token"].(string); ok {
		token.AccessToken = v
	}
	if v, ok := raw["token_type"].(string); ok {
		token.TokenType = v
	}
	if v, ok := raw["scope"].(string); ok {
		token.Scope = v
	}

	// Handle expiration
	if v, ok := raw["expires_in"].(float64); ok {
		token.ExpiresAt = time.Now().Add(time.Duration(v) * time.Second)
	}

	return token, nil
}

func (p *ClientCredentialsProvider) SupportsFlow(flowType FlowType) bool {
	return flowType == FlowClientCredentials
}

func (p *ClientCredentialsProvider) ValidateToken(ctx context.Context, token string) error {
	// Implement token validation logic
	// This might involve introspection endpoint if available
	return nil
}

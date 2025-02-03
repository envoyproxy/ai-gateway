package oauth

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// FlowType represents different OAuth/OIDC flow types
type FlowType string

const (
	FlowClientCredentials            FlowType = "client_credentials"
	FlowClientCredentialsWithIDToken FlowType = "client_credentials_with_id_token"
)

// TokenResponse represents the common token response structure
type TokenResponse struct {
	AccessToken  string
	TokenType    string
	ExpiresAt    time.Time
	Scope        string
	IDToken      string // Optional OIDC field
	RefreshToken string // Optional refresh token
	Raw          map[string]interface{}
}

// Config holds the provider configuration
type Config struct {
	// TokenURL is the OAuth/OIDC token endpoint URL.
	// For OIDC, this can be omitted if issuer_url is provided in Options.
	TokenURL string

	// ClientID is the OAuth/OIDC client identifier
	ClientID string

	// SecretRef references the Kubernetes secret containing credentials
	SecretRef *corev1.SecretReference

	// Scopes are the OAuth/OIDC scopes to request.
	// For OIDC flows, 'openid' will be automatically added if not present.
	Scopes []string

	// FlowType specifies the OAuth/OIDC flow to use
	FlowType FlowType

	// Options contains additional provider-specific options.
	// For OIDC, supported options include:
	// - issuer_url: The OIDC issuer URL for discovery (string)
	// - client_secret: The client secret for token requests (string)
	// - discovery_cache_ttl: How long to cache discovery info (duration string, default "24h")
	Options map[string]interface{}
}

// Provider defines the interface for OAuth token providers
type Provider interface {
	FetchToken(ctx context.Context, config Config) (*TokenResponse, error)
	ValidateToken(ctx context.Context, token string) error
	SupportsFlow(flowType FlowType) bool
}

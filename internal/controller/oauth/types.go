package oauth

import (
	"context"
	"time"
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

// Provider defines the interface for OAuth token providers
type Provider interface {
	FetchToken(ctx context.Context) (*TokenResponse, error)
	ValidateToken(ctx context.Context, token string) error
	SupportsFlow(flowType FlowType) bool
}

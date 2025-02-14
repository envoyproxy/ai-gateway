package oauth

import (
	"context"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
)

// TokenProvider defines the interface for OAuth token providers.
type TokenProvider interface {
	// FetchToken will obtain oauth token using oidc credentials.
	FetchToken(ctx context.Context, oidc *egv1a1.OIDC) (*oauth2.Token, error)
}

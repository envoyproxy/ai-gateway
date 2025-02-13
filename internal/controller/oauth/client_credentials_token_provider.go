package oauth

import (
	"context"
	"fmt"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
)

// ClientCredentialsTokenProvider implements the standard OAuth2 client credentials flow
type ClientCredentialsTokenProvider struct {
	*BaseProvider
	TokenSource oauth2.TokenSource
}

// NewClientCredentialsProvider creates a new client credentials provider
func NewClientCredentialsProvider(base *BaseProvider) *ClientCredentialsTokenProvider {
	return &ClientCredentialsTokenProvider{
		BaseProvider: base,
	}
}

// FetchToken gets the client secret from the secret reference and fetches the token from the provider token URL.
func (p *ClientCredentialsTokenProvider) FetchToken(ctx context.Context, oidc *egv1a1.OIDC) (*oauth2.Token, error) {
	clientSecret, err := p.getClientSecret(ctx, &corev1.SecretReference{
		Name:      string(oidc.ClientSecret.Name),
		Namespace: string(*oidc.ClientSecret.Namespace),
	})
	if err != nil {
		return nil, err
	}
	return p.getTokenWithClientCredentialConfig(ctx, oidc, clientSecret)
}

// getTokenWithClientCredentialFlow fetches the oauth2 token with client credential config
func (p *ClientCredentialsTokenProvider) getTokenWithClientCredentialConfig(ctx context.Context, oidc *egv1a1.OIDC, clientSecret string) (*oauth2.Token, error) {
	if p.TokenSource == nil {
		oauth2Config := clientcredentials.Config{
			ClientID:     oidc.ClientID,
			ClientSecret: clientSecret,
			// Discovery returns the OAuth2 endpoints.
			TokenURL: *oidc.Provider.TokenEndpoint,
			Scopes:   oidc.Scopes,
		}
		p.TokenSource = oauth2Config.TokenSource(ctx)
	}
	token, err := p.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("fail to get oauth2 token %w", err)
	}

	// Handle expiration
	if token.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return token, nil
}

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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/oauth"
)

type TokenExpiry struct {
	Token     string
	ExpiresAt time.Time
}

type TokenProvider interface {
	GetToken(ctx context.Context) (TokenExpiry, error)
}

type AzureTokenProvider struct {
	credential  *azidentity.ClientSecretCredential
	tokenOption policy.TokenRequestOptions
}

func NewAzureTokenProvider(tenantID, clientID, clientSecret string, tokenOption policy.TokenRequestOptions) (*AzureTokenProvider, error) {
	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, err
	}
	return &AzureTokenProvider{credential: credential, tokenOption: tokenOption}, nil
}

func (a *AzureTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	azureToken, err := a.credential.GetToken(ctx, a.tokenOption)
	if err != nil {
		return TokenExpiry{}, err
	}
	return TokenExpiry{Token: azureToken.Token, ExpiresAt: azureToken.ExpiresOn}, nil
}

type OidcTokenProvider struct {
	oidcConfig *egv1a1.OIDC // passed in from callsite in backend_security_policy
	client     client.Client
	context    context.Context
}

func NewOidcTokenProvider(ctx context.Context, client client.Client, oidcConfig *egv1a1.OIDC) (*OidcTokenProvider, error) {
	// Hydrate oidcConfig before construct OidcTokenProvider
	// copy from original oidc_provider.go's GetToken
	issuerURL := oidcConfig.Provider.Issuer
	// construct oidc.NewProvider to hydrate and validate original egv1a1.OIDC object
	oidcProvider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create oidc config: %q, %w", issuerURL, err)
	}
	var config oidc.ProviderConfig
	if err = oidcProvider.Claims(&config); err != nil {
		return nil, fmt.Errorf("failed to decode oidc config claims: %q, %w", issuerURL, err)
	}

	// Unmarshal supported scopes
	var claims struct {
		SupportedScopes []string `json:"scopes_supported"`
	}
	if err = oidcProvider.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to decode provider scope supported claims: %w", err)
	}

	// Validate required fields.
	if config.IssuerURL == "" {
		return nil, fmt.Errorf("issuer is required in OIDC provider config")
	}
	if config.TokenURL == "" {
		return nil, fmt.Errorf("token_endpoint is required in OIDC provider config")
	}

	// Use discovered token endpoint if not explicitly provided
	if oidcConfig.Provider.TokenEndpoint == nil {
		oidcConfig.Provider.TokenEndpoint = &config.TokenURL
	}
	// Add discovered scopes if available
	if len(claims.SupportedScopes) > 0 {
		requestedScopes := make(map[string]bool, len(oidcConfig.Scopes))
		for _, scope := range oidcConfig.Scopes {
			requestedScopes[scope] = true
		}

		// Add supported scopes that aren't already requested
		for _, scope := range claims.SupportedScopes {
			if !requestedScopes[scope] {
				oidcConfig.Scopes = append(oidcConfig.Scopes, scope)
			}
		}
	}
	// Now OidcTokenProvider has all fields configured and is ready for caller to use by calling GetToken(ctx)
	return &OidcTokenProvider{oidcConfig, client, ctx}, nil
}

func (o *OidcTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	// implement logic from original client_credential_token_provider.go's GetToken and getTokenWithClientCredentialConfig
	if o.oidcConfig.ClientSecret.Namespace == nil {
		return TokenExpiry{}, fmt.Errorf("oidc client secret namespace is nil")
	}
	clientSecret, err := oauth.GetClientSecret(ctx, o.client, &corev1.SecretReference{
		Name:      string(o.oidcConfig.ClientSecret.Name),
		Namespace: string(*o.oidcConfig.ClientSecret.Namespace),
	})
	if err != nil {
		return TokenExpiry{}, err
	}
	oauth2Config := clientcredentials.Config{
		ClientSecret: clientSecret,
		ClientID:     o.oidcConfig.ClientID,
		Scopes:       o.oidcConfig.Scopes,
	}

	if o.oidcConfig.Provider.TokenEndpoint != nil {
		oauth2Config.TokenURL = *o.oidcConfig.Provider.TokenEndpoint
	}

	// Underlying token call will apply http client timeout
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: time.Minute})

	token, err := oauth2Config.Token(ctx)
	if err != nil {
		return TokenExpiry{}, fmt.Errorf("failed to get oauth2 token: %w", err)
	}
	// Handle expiration
	if token.ExpiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return TokenExpiry{Token: token.AccessToken, ExpiresAt: token.Expiry}, nil
}

// MockTokenProvider for unit tests, simply allow user to pass int any token string and expiry to facilitate mock test
type MockTokenProvider struct {
	Token     string
	ExpiresAt time.Time
	Err       error
}

func (m *MockTokenProvider) GetToken(_ context.Context) (TokenExpiry, error) {
	return TokenExpiry{m.Token, m.ExpiresAt}, m.Err
}

func NewMockTokenProvider(mockToken string, mockExpireAt time.Time, err error) *MockTokenProvider {
	mockProvider := MockTokenProvider{
		Token:     mockToken,
		ExpiresAt: mockExpireAt,
		Err:       err,
	}
	return &mockProvider
}

package backendauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// IdentityTokenValue is for retrieving an identity token
type IdentityTokenValue string

// GetIdentityToken retrieves the JWT and returns the contents as a []byte
func (j IdentityTokenValue) GetIdentityToken() ([]byte, error) {
	return []byte(j), nil
}

type oauth2TokenWithExp struct {
	token   *oauth2.Token
	expTime time.Time
}

type oidcHandler struct {
	clientSecretFileName string
	aud                  string
	oidcCredCache        oauth2TokenWithExp
	oidc                 egv1a1.OIDC
}

func newOIDCHandler(oidcCredential egv1a1.OIDC, clientSecretFileName string) (*oidcHandler, error) {
	handler := &oidcHandler{
		clientSecretFileName: clientSecretFileName,
		oidcCredCache:        oauth2TokenWithExp{},
		oidc:                 oidcCredential,
	}

	go handler.updateCredentialsIfExpired()
	return handler, nil
}

func (h *oidcHandler) extractOauth2Token(ctx context.Context) (*oauth2.Token, error) {
	provider, err := oidc.NewProvider(ctx, h.oidc.Provider.Issuer)
	if err != nil {
		return nil, fmt.Errorf("fail to create oidc provider: %w", err)
	}
	clientSecret, err := h.extractClientSecret()
	if err != nil {
		return nil, fmt.Errorf("fail to extract client secret: %w", err)
	}
	oauth2Config := clientcredentials.Config{
		ClientID:     h.oidc.ClientID,
		ClientSecret: clientSecret,
		// Discovery returns the OAuth2 endpoints.
		TokenURL: provider.Endpoint().TokenURL,
		Scopes:   h.oidc.Scopes,
	}
	oauth2Config.EndpointParams = url.Values{"audience": []string{h.aud}}
	t, err := oauth2Config.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("fail to refresh oauth2 token %w", err)
	}
	return t, nil
}

func (h *oidcHandler) oauth2TokenExpireTime(accessToken *oauth2.Token) (*time.Time, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(accessToken.AccessToken, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("fail to parse oauth2 token: %v", slog.Any("error", err))
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("fail to parse oauth2 token claims: %v", slog.Any("error", err))
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, fmt.Errorf("fail to parse oauth2 token exp: %v", slog.Any("error", err))
	}
	expTime := time.Unix(int64(exp), 0)
	return &expTime, nil
}

func (h *oidcHandler) updateOIDCTokenIfExpired(ctx context.Context) error {
	if h.oidcCredCache.token == nil || time.Now().After(h.oidcCredCache.expTime.Add(-5*time.Minute)) {
		token, err := h.extractOauth2Token(ctx)
		if err != nil {
			return err
		}

		expireTime, err := h.oauth2TokenExpireTime(token)
		if err != nil {
			return err
		}

		h.oidcCredCache.token = token
		h.oidcCredCache.expTime = *expireTime
	}
	return nil
}

func (h *oidcHandler) extractClientSecret() (string, error) {
	secret, err := os.ReadFile(h.clientSecretFileName)
	if err != nil {
		return "", fmt.Errorf("failed to read api key file: %w", err)
	}
	return strings.TrimSpace(string(secret)), nil
}

func (h *oidcHandler) updateCredentialsIfExpired() {
	for {
		err := h.updateOIDCTokenIfExpired(context.Background())
		if err != nil {
			return
		}
		time.Sleep(1 * time.Minute)
	}
}

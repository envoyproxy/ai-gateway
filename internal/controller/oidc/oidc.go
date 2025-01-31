package oidc

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// OIDC expects the SecretKey to be "client-secret".
const (
	secretKey         = "client-secret"
	timeBeforeExpired = -5 * time.Minute
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

type ProviderCredentialExchange interface {
	needsCredentialRefresh(cacheKey string) bool
	isOIDCBackendSecurityPolicy(policy aigv1a1.BackendSecurityPolicy) bool
	updateCredentials(accessToken, cacheKey string) error
	updateSecret(cacheKey string) error
	getAud(cacheKey string) string
	getOIDC(cacheKey string) egv1a1.OIDC
	createSpecIfNew(policy aigv1a1.BackendSecurityPolicy)
}

type OIDCTokenExchange struct {
	logger    *logr.Logger
	k8sClient client.Client
	provider  ProviderCredentialExchange
	// oidcCredentialCache cache key is backend security policy's namespace + name.
	oidcCredentialCache map[string]*oauth2TokenWithExp
	interval            time.Duration
	stopChan            chan struct{}
}

func NewOIDCTokenExchange(logger *logr.Logger, k8sClient client.Client, providerType aigv1a1.BackendSecurityPolicyType) (*OIDCTokenExchange, error) {
	handler := &OIDCTokenExchange{
		logger:              logger,
		k8sClient:           k8sClient,
		oidcCredentialCache: make(map[string]*oauth2TokenWithExp),
		interval:            time.Minute,
		stopChan:            make(chan struct{}),
	}

	if providerType == aigv1a1.BackendSecurityPolicyTypeAWSCredentials {
		handler.provider = newAWSCredentialExchange(logger, k8sClient)
	}

	return handler, nil
}

func (o *OIDCTokenExchange) RefreshCredentials(ctx context.Context) {
	ticker := time.NewTicker(o.interval)
	for {
		select {
		case <-ticker.C:
			backendSecurityPolicies := &aigv1a1.BackendSecurityPolicyList{}
			if err := o.k8sClient.List(context.Background(), backendSecurityPolicies); err != nil {
				o.logger.Error(err, "Failed to get backend security policies")
			}

			for _, backendSecurityPolicy := range backendSecurityPolicies.Items {
				err := o.updateBackendSecurityCredentials(ctx, backendSecurityPolicy)
				if err != nil {
					o.logger.Error(err, "Failed to update backend security credentials")
				}
			}
		case <-o.stopChan:
			ticker.Stop()
			return
		}
	}
}

func (o *OIDCTokenExchange) updateBackendSecurityCredentials(ctx context.Context, backendSecurityPolicy aigv1a1.BackendSecurityPolicy) error {
	if !o.provider.isOIDCBackendSecurityPolicy(backendSecurityPolicy) {
		return nil
	}

	o.provider.createSpecIfNew(backendSecurityPolicy)
	cacheKey := fmt.Sprintf("%s.%s", backendSecurityPolicy.Name, backendSecurityPolicy.Namespace)
	if o.needsTokenRefresh(cacheKey) {
		err := o.updateOIDCExpiredToken(ctx, cacheKey, backendSecurityPolicy.Namespace)
		if err != nil {
			o.logger.Error(err, "Failed to update OIDC token", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
	}

	if o.provider.needsCredentialRefresh(cacheKey) {
		accessToken := o.oidcCredentialCache[cacheKey].token.AccessToken
		err := o.provider.updateCredentials(accessToken, cacheKey)
		if err != nil {
			o.logger.Error(err, "Failed to get sts credentials", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
		err = o.provider.updateSecret(cacheKey)
		if err != nil {
			o.logger.Error(err, "Failed to update AWS secret", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
	}
	return nil
}

func (o *OIDCTokenExchange) getOauth2Token(ctx context.Context, namespace, cacheKey string) (*oauth2.Token, error) {
	oidcCreds := o.provider.getOIDC(cacheKey)
	provider, err := oidc.NewProvider(ctx, oidcCreds.Provider.Issuer)
	if err != nil {
		return nil, fmt.Errorf("fail to create oidc provider: %w", err)
	}
	clientSecret, err := o.extractClientSecret(ctx, namespace, string(oidcCreds.ClientSecret.Name))
	if err != nil {
		return nil, fmt.Errorf("fail to extract client secret: %w", err)
	}
	oauth2Config := clientcredentials.Config{
		ClientID:     oidcCreds.ClientID,
		ClientSecret: clientSecret,
		// Discovery returns the OAuth2 endpoints.
		TokenURL: provider.Endpoint().TokenURL,
		Scopes:   oidcCreds.Scopes,
	}
	oauth2Config.EndpointParams = url.Values{"audience": []string{o.provider.getAud(cacheKey)}}
	t, err := oauth2Config.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("fail to refresh oauth2 token %w", err)
	}
	return t, nil
}

func (o *OIDCTokenExchange) oauth2TokenExpireTime(accessToken *oauth2.Token) (*time.Time, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(accessToken.AccessToken, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("fail to parse oauth2 token: %v", slog.Any("error", err))
	}
	// Claims is defined as MapClaims in ParseUnverified to get token expiration time.
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("fail to parse oauth2 token claims: %v", slog.Any("error", err))
	}
	// Expects exp to be the expiration time in seconds.
	exp, ok := claims["exp"].(int64)
	if !ok {
		return nil, fmt.Errorf("fail to parse oauth2 token exp: %v", slog.Any("error", err))
	}
	expTime := time.Unix(exp, 0)
	return &expTime, nil
}

func (o *OIDCTokenExchange) updateOIDCExpiredToken(ctx context.Context, cacheKey, namespace string) error {
	if _, ok := o.oidcCredentialCache[cacheKey]; ok {
		o.oidcCredentialCache[cacheKey] = &oauth2TokenWithExp{}
	}

	token, err := o.getOauth2Token(ctx, namespace, cacheKey)
	if err != nil {
		return err
	}

	expireTime, err := o.oauth2TokenExpireTime(token)
	if err != nil {
		return err
	}

	o.oidcCredentialCache[cacheKey].token = token
	o.oidcCredentialCache[cacheKey].expTime = *expireTime
	return nil
}

func (o *OIDCTokenExchange) extractClientSecret(ctx context.Context, ns, secretName string) (string, error) {
	secret := &corev1.Secret{}
	if err := o.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: ns,
		Name:      secretName,
	}, secret); err != nil {
		return "", fmt.Errorf("failed to get secret %s.%s: %w", secretName, ns, err)
	}
	clientSecret, ok := secret.Data[secretKey]
	if !ok {
		return "", fmt.Errorf("missing '%s' in secret %s.%s", secretKey, secret.Name, secret.Namespace)
	}
	return string(clientSecret), nil
}

func (o *OIDCTokenExchange) needsTokenRefresh(cacheKey string) bool {
	return o.oidcCredentialCache[cacheKey].token == nil || time.Now().After(o.oidcCredentialCache[cacheKey].expTime.Add(timeBeforeExpired))
}

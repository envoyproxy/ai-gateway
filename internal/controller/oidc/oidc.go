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
const secretKey = "client-secret"
const timeBeforeExpired = -5 * time.Minute

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

type Handler struct {
	logger    *logr.Logger
	k8sClient client.Client
	// awsCredentialCache cache key is backend security policy's namespace + name.
	awsCredentialCache map[string]time.Time
	// oidcCredentialCache cache key is backend security policy's namespace + name.
	oidcCredentialCache map[string]*oauth2TokenWithExp
	interval            time.Duration
}

func NewOIDCHandler(logger *logr.Logger, k8sClient client.Client) (*Handler, error) {
	handler := &Handler{
		logger:              logger,
		k8sClient:           k8sClient,
		awsCredentialCache:  make(map[string]time.Time),
		oidcCredentialCache: make(map[string]*oauth2TokenWithExp),
		interval:            time.Minute,
	}
	return handler, nil
}

func (o *Handler) refreshCredentials(ctx context.Context) {
	for {
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
		time.Sleep(o.interval)
	}
}

func (o *Handler) updateBackendSecurityCredentials(ctx context.Context, backendSecurityPolicy aigv1a1.BackendSecurityPolicy) error {
	// Only AWS Credentials currently supports OIDC
	if backendSecurityPolicy.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAWSCredentials {
		o.logger.Info(fmt.Sprintf("Skipping credentials refresh for type %s", backendSecurityPolicy.Spec.Type))
		return nil
	}
	if backendSecurityPolicy.Spec.AWSCredentials.CredentialsFile != nil {
		o.logger.Info(fmt.Sprintf("Skiping due to credential file being set for %s", backendSecurityPolicy.Name))
		return nil
	}

	cacheKey := fmt.Sprintf("%s.%s", backendSecurityPolicy.Name, backendSecurityPolicy.Namespace)
	awsCredentials := backendSecurityPolicy.Spec.AWSCredentials
	if o.needsTokenRefresh(cacheKey) {
		oidcCred := awsCredentials.OIDCExchangeToken.OIDC
		oidcAud := awsCredentials.OIDCExchangeToken.Aud
		err := o.updateOIDCExpiredToken(ctx, oidcCred, cacheKey, oidcAud, backendSecurityPolicy.Namespace)
		if err != nil {
			o.logger.Error(err, "Failed to update OIDC token", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
	}

	if needsAWSCredentialRefresh() {
		credentials, err := getSTSCredentials(awsCredentials.Region, awsCredentials.OIDCExchangeToken.AwsRoleArn, awsCredentials.OIDCExchangeToken.ProxyURL, awsCredentials.OIDCExchangeToken.Aud)
		if err != nil {
			o.logger.Error(err, "Failed to get sts credentials", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
		err = updateAWSSecret(o.k8sClient, credentials, backendSecurityPolicy.Namespace, cacheKey)
		if err != nil {
			o.logger.Error(err, "Failed to update AWS secret", "BackendSecurityPolicy", backendSecurityPolicy.Name)
		}
		o.awsCredentialCache[cacheKey] = credentials.Expires
	}
	return nil
}

func (o *Handler) getOauth2Token(ctx context.Context, oidcCreds egv1a1.OIDC, aud, namespace string) (*oauth2.Token, error) {
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
	oauth2Config.EndpointParams = url.Values{"audience": []string{aud}}
	t, err := oauth2Config.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("fail to refresh oauth2 token %w", err)
	}
	return t, nil
}

func (o *Handler) oauth2TokenExpireTime(accessToken *oauth2.Token) (*time.Time, error) {
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

func (o *Handler) updateOIDCExpiredToken(ctx context.Context, oidcCreds egv1a1.OIDC, cacheKey, aud, namespace string) error {
	if _, ok := o.oidcCredentialCache[cacheKey]; ok {
		o.oidcCredentialCache[cacheKey] = &oauth2TokenWithExp{}
	}

	token, err := o.getOauth2Token(ctx, oidcCreds, aud, namespace)
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

func (o *Handler) extractClientSecret(ctx context.Context, ns, secretName string) (string, error) {
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

func (o *Handler) needsTokenRefresh(cacheKey string) bool {
	return o.oidcCredentialCache[cacheKey].token == nil || time.Now().After(o.oidcCredentialCache[cacheKey].expTime)
}

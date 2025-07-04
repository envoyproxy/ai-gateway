// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
	"google.golang.org/api/sts/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/tokenprovider"
)

const (
	// GCPAccessTokenKey is the key used to store GCP access token in Kubernetes secrets.
	GCPAccessTokenKey = "gcpAccessToken"
	GCPProjectNameKey = "projectName"
	GCPRegionKey      = "region"
	// grantTypeTokenExchange is the OAuth 2.0 grant type for token exchange.
	grantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange" //nolint:gosec
	// gcpIAMScope is the OAuth scope for IAM operations in GCP.
	gcpIAMScope = "https://www.googleapis.com/auth/iam" //nolint:gosec
	// tokenTypeAccessToken indicates the requested token type is an access token.
	tokenTypeAccessToken = "urn:ietf:params:oauth:token-type:access_token" //nolint:gosec
	// tokenTypeJWT indicates the subject token type is a JWT.
	tokenTypeJWT = "urn:ietf:params:oauth:token-type:jwt" //nolint:gosec
	// stsTokenScope is the OAuth scope for GCP cloud platform operations.
	stsTokenScope = "https://www.googleapis.com/auth/cloud-platform" //nolint:gosec
)

// serviceAccountTokenGenerator defines a function type for generating a GCP service account access token
// using an STS token and impersonation configuration.
type serviceAccountTokenGenerator func(
	ctx context.Context,
	stsToken string,
	saConfig aigv1a1.GCPServiceAccountImpersonationConfig,
	opts ...option.ClientOption,
) (*tokenprovider.TokenExpiry, error)

// stsTokenGenerator defines a function type for exchanging a JWT token for a GCP STS token
// using Workload Identity Federation configuration.
type stsTokenGenerator func(
	ctx context.Context,
	jwtToken string,
	wifConfig aigv1a1.GCPWorkLoadIdentityFederationConfig,
	opts ...option.ClientOption,
) (*tokenprovider.TokenExpiry, error)

// gcpOIDCTokenRotator implements Rotator interface for GCP access token exchange.
// It handles the complete authentication flow for GCP Workload Identity Federation:
//  1. Obtaining an OIDC token from the configured provider.
//  2. Exchanging the OIDC token for a GCP STS token.
//  3. Using the STS token to impersonate a GCP service account.
//  4. Storing the resulting access token in a Kubernetes secret.
type gcpOIDCTokenRotator struct {
	client client.Client // Kubernetes client for interacting with the cluster.
	logger logr.Logger   // Logger for recording rotator activities.
	// GCP Credentials configuration from BackendSecurityPolicy.
	gcpCredentials aigv1a1.BackendSecurityPolicyGCPCredentials
	// backendSecurityPolicyName provides name of backend security policy.
	backendSecurityPolicyName string
	// backendSecurityPolicyNamespace provides namespace of backend security policy.
	backendSecurityPolicyNamespace string
	// preRotationWindow is the duration before token expiry when rotation should occur.
	preRotationWindow time.Duration
	// oidcProvider provides the OIDC token needed for GCP Workload Identity Federation.
	oidcProvider tokenprovider.TokenProvider

	saTokenFunc  serviceAccountTokenGenerator
	stsTokenFunc stsTokenGenerator
}

// NewGCPOIDCTokenRotator creates a new gcpOIDCTokenRotator with the given parameters.
func NewGCPOIDCTokenRotator(
	client client.Client,
	logger logr.Logger,
	bsp aigv1a1.BackendSecurityPolicy,
	preRotationWindow time.Duration,
	tokenProvider tokenprovider.TokenProvider,
) (Rotator, error) {
	logger = logger.WithName("gcp-token-rotator")

	if bsp.Spec.GCPCredentials == nil {
		return nil, fmt.Errorf("GCP credentials are not configured in BackendSecurityPolicy %s/%s", bsp.Namespace, bsp.Name)
	}

	return &gcpOIDCTokenRotator{
		client:                         client,
		logger:                         logger,
		gcpCredentials:                 *bsp.Spec.GCPCredentials,
		backendSecurityPolicyName:      bsp.Name,
		backendSecurityPolicyNamespace: bsp.Namespace,
		preRotationWindow:              preRotationWindow,
		oidcProvider:                   tokenProvider,
		saTokenFunc:                    impersonateServiceAccount,
		stsTokenFunc:                   exchangeJWTForSTSToken,
	}, nil
}

// IsExpired implements [Rotator.IsExpired].
// IsExpired checks if the preRotation time is before the current time.
func (r *gcpOIDCTokenRotator) IsExpired(preRotationExpirationTime time.Time) bool {
	// Use the common IsBufferedTimeExpired helper to determine if the token has expired.
	// A buffer of 0 means we check exactly at the pre-rotation time.
	return IsBufferedTimeExpired(0, preRotationExpirationTime)
}

// GetPreRotationTime implements [Rotator.GetPreRotationTime].
// GetPreRotationTime retrieves the pre-rotation time for GCP token.
func (r *gcpOIDCTokenRotator) GetPreRotationTime(ctx context.Context) (time.Time, error) {
	// Look up the secret containing the current token.
	secret, err := LookupSecret(ctx, r.client, r.backendSecurityPolicyNamespace, GetBSPSecretName(r.backendSecurityPolicyName))
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the secret doesn't exist, return zero time to indicate immediate rotation is needed.
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("failed to lookup secret: %w", err)
	}
	// Extract the token expiration time from the secret's annotations.
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get expiration time from secret: %w", err)
	}

	// Calculate the pre-rotation time by subtracting the pre-rotation window from the expiration time
	// This ensures tokens are rotated before they expire.
	preRotationTime := expirationTime.Add(-r.preRotationWindow)
	return preRotationTime, nil
}

// Rotate implements [Rotator.Rotate].
// Rotate fetches new GCP access token and updates the Kubernetes secret.
// The token rotation process follows these steps:
// 1. Obtain an OIDC token from the configured provider
// 2. Exchange the OIDC token for a GCP STS token
// 3. (If configured) Use the STS token to impersonate the specified GCP service account
// 4. Store the resulting access token in a Kubernetes secret
// Returns the expiration time of the new token and any error encountered during rotation.
func (r *gcpOIDCTokenRotator) Rotate(ctx context.Context) (time.Time, error) {
	secretName := GetBSPSecretName(r.backendSecurityPolicyName)

	r.logger.Info("start rotating gcp access token", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)

	// 1. Get OIDCProvider Token.
	// This is the initial token from the configured OIDC provider (e.g., Kubernetes service account token).
	oidcTokenExpiry, err := r.oidcProvider.GetToken(ctx)
	if err != nil {
		r.logger.Error(err, "failed to get token from oidc provider", "oidcIssuer", r.gcpCredentials.WorkLoadIdentityFederationConfig.WorkloadIdentityProvider.Name)
		return time.Time{}, fmt.Errorf("failed to obtain OIDC token: %w", err)
	}

	// 2. Exchange the JWT for an STS token.
	// The OIDC JWT token is exchanged for a Google Cloud STS token.
	stsToken, err := r.stsTokenFunc(ctx, oidcTokenExpiry.Token, r.gcpCredentials.WorkLoadIdentityFederationConfig)
	if err != nil {
		wifConfig := r.gcpCredentials.WorkLoadIdentityFederationConfig
		r.logger.Error(err, "failed to exchange JWT for STS token",
			"projectID", wifConfig.ProjectID,
			"workloadIdentityPool", wifConfig.WorkloadIdentityPoolName,
			"workloadIdentityProvider", wifConfig.WorkloadIdentityProvider.Name)
		return time.Time{}, fmt.Errorf("failed to exchange JWT for STS token (project: %s, pool: %s): %w",
			wifConfig.ProjectID, wifConfig.WorkloadIdentityPoolName, err)
	}

	// 3. Exchange the STS token for a GCP service account access token.
	// The STS token is used to impersonate a GCP service account.
	var gcpAccessToken *tokenprovider.TokenExpiry
	if r.gcpCredentials.WorkLoadIdentityFederationConfig.ServiceAccountImpersonation != nil {
		gcpAccessToken, err = r.saTokenFunc(ctx, stsToken.Token, *r.gcpCredentials.WorkLoadIdentityFederationConfig.ServiceAccountImpersonation)
		if err != nil {
			saImpersonation := r.gcpCredentials.WorkLoadIdentityFederationConfig.ServiceAccountImpersonation
			saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
				saImpersonation.ServiceAccountName,
				saImpersonation.ServiceAccountProjectName)
			r.logger.Error(err, "failed to impersonate GCP service account",
				"serviceAccount", saEmail,
				"serviceAccountProject", saImpersonation.ServiceAccountProjectName)
			return time.Time{}, fmt.Errorf("failed to impersonate service account %s: %w", saEmail, err)
		}
	} else {
		// If no service account impersonation is configured, use the STS token directly.
		gcpAccessToken = stsToken
	}

	secret, err := LookupSecret(ctx, r.client, r.backendSecurityPolicyNamespace, secretName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.logger.Info("creating a new gcp access token into secret", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: r.backendSecurityPolicyNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: make(map[string][]byte),
			}
			populateInSecret(secret, filterapi.GCPAuth{
				AccessToken: gcpAccessToken.Token,
				Region:      r.gcpCredentials.Region,
				ProjectName: r.gcpCredentials.ProjectName,
			}, gcpAccessToken.ExpiresAt)
			err = r.client.Create(ctx, secret)
			if err != nil {
				r.logger.Error(err, "failed to create gcp access token", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)
				return time.Time{}, err
			}
			return gcpAccessToken.ExpiresAt, nil
		}
		r.logger.Error(err, "failed to lookup gcp access token secret", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)
		return time.Time{}, err
	}
	r.logger.Info("updating gcp access token secret", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)

	populateInSecret(secret, filterapi.GCPAuth{
		AccessToken: gcpAccessToken.Token,
		Region:      r.gcpCredentials.Region,
		ProjectName: r.gcpCredentials.ProjectName,
	}, gcpAccessToken.ExpiresAt)
	err = r.client.Update(ctx, secret)
	if err != nil {
		r.logger.Error(err, "failed to update gcp access token", "namespace", r.backendSecurityPolicyNamespace, "name", r.backendSecurityPolicyName)
		return time.Time{}, err
	}
	return gcpAccessToken.ExpiresAt, nil
}

var _ stsTokenGenerator = exchangeJWTForSTSToken

// exchangeJWTForSTSToken implements [stsTokenGenerator]
// exchangeJWTForSTSToken exchanges a JWT token for a GCP STS (Security Token Service) token.
func exchangeJWTForSTSToken(ctx context.Context, jwtToken string, wifConfig aigv1a1.GCPWorkLoadIdentityFederationConfig, opts ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
	proxyOpt, err := getGCPProxyClientOption()
	if err != nil {
		return nil, fmt.Errorf("error getting GCP proxy client option: %w", err)
	}
	if proxyOpt != nil {
		opts = append(opts, proxyOpt)
	}

	opts = append(opts, option.WithoutAuthentication())

	// Create an STS client.
	stsService, err := sts.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("error creating GCP STS service client: %w", err)
	}
	// Construct the STS request.
	// Build the audience string in the format required by GCP Workload Identity Federation.
	stsAudience := fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
		wifConfig.ProjectID,
		wifConfig.WorkloadIdentityPoolName,
		wifConfig.WorkloadIdentityProvider.Name)

	// Create the token exchange request with the appropriate parameters.
	req := &sts.GoogleIdentityStsV1ExchangeTokenRequest{
		GrantType:          grantTypeTokenExchange,
		Audience:           stsAudience,
		Scope:              gcpIAMScope,
		RequestedTokenType: tokenTypeAccessToken,
		SubjectToken:       jwtToken,
		SubjectTokenType:   tokenTypeJWT,
	}

	// Call the STS API.
	resp, err := stsService.V1.Token(req).Do()
	if err != nil {
		return nil, fmt.Errorf("error calling GCP STS Token API with audience %s: %w", stsAudience, err)
	}

	return &tokenprovider.TokenExpiry{
		Token:     resp.AccessToken,
		ExpiresAt: time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}, nil
}

var _ serviceAccountTokenGenerator = impersonateServiceAccount

// impersonateServiceAccount returns a GCP service account access token or an error if impersonation fails.
// It takes an STS token and uses it to impersonate a GCP service account,
// generating a new access token with the permissions of that service account.
//
// The service account email is constructed from serviceAccountName and serviceAccountProjectName
// in the format: <serviceAccountName>@<serviceAccountProjectName>.iam.gserviceaccount.com
//
// The resulting token will have the cloud-platform scope.
func impersonateServiceAccount(ctx context.Context, stsToken string, saConfig aigv1a1.GCPServiceAccountImpersonationConfig, opts ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
	// Construct the service account email from the configured parameters.
	saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saConfig.ServiceAccountName, saConfig.ServiceAccountProjectName)

	// Configure the impersonation parameters.
	// Define which service account to impersonate and what scopes the token should have.
	config := impersonate.CredentialsConfig{
		TargetPrincipal: saEmail,                 // The service account to impersonate.
		Scopes:          []string{stsTokenScope}, // The desired scopes for the access token.
	}

	// Use the STS token as the source token for impersonation.
	opts = append(opts, option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: stsToken, TokenType: "Bearer"})))

	// If a proxy URL is set, add it as a client option.
	proxyOpt, err := getGCPProxyClientOption()
	if err != nil {
		return nil, fmt.Errorf("error getting GCP proxy client option: %w", err)
	}

	if proxyOpt != nil {
		opts = append(opts, proxyOpt)
	}

	// Create a token source that will provide tokens with the permissions of the impersonated service account.
	ts, err := impersonate.CredentialsTokenSource(ctx, config, opts...)
	if err != nil {
		return nil, fmt.Errorf("error creating impersonated credentials for service account %s: %w", saEmail, err)
	}

	// Get the token.
	token, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("error getting access token for service account %s: %w", saEmail, err)
	}
	return &tokenprovider.TokenExpiry{
		Token:     token.AccessToken,
		ExpiresAt: token.Expiry,
	}, nil
}

// populateAzureAccessToken updates the secret with the Azure access token.
func populateInSecret(secret *corev1.Secret, gcpAuth filterapi.GCPAuth, expiryTime time.Time) {
	updateExpirationSecretAnnotation(secret, expiryTime)
	secret.Data = map[string][]byte{
		GCPAccessTokenKey: []byte(gcpAuth.AccessToken),
		GCPProjectNameKey: []byte(gcpAuth.ProjectName),
		GCPRegionKey:      []byte(gcpAuth.Region),
	}
}

func getGCPProxyClientOption() (option.ClientOption, error) {
	proxyURL := os.Getenv("AI_GATEWAY_GCP_AUTH_PROXY_URL")
	if proxyURL == "" {
		return nil, nil
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
	}
	httpClient := &http.Client{Transport: transport}
	return option.WithHTTPClient(httpClient), nil
}

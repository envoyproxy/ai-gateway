package controller

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const (
	// defaultRotationInterval is the default interval for rotating AWS credentials
	defaultRotationInterval = 24 * time.Hour
	// defaultPreRotationWindow is the default time before expiry to rotate credentials
	defaultPreRotationWindow = 1 * time.Hour
	// rotationAnnotation is used to track when credentials were last rotated
	rotationAnnotation = "aigateway.envoyproxy.io/last-rotation-timestamp"
	// credentialsKey is the key in the secret data that contains AWS credentials
	credentialsKey = "credentials"
	// defaultProfile is the default AWS credentials profile name
	defaultProfile = "default"

	// Retry configuration
	maxRetries        = 3
	baseRetryDelay    = 1 * time.Second
	maxRetryDelay     = 10 * time.Second
	retryJitterFactor = 0.1
)

// permanentError represents an error that should not be retried
type permanentError struct {
	err error
}

func (e *permanentError) Error() string {
	return e.err.Error()
}

// isPermanentError checks if an error is a permanent error that should not be retried
func isPermanentError(err error) bool {
	_, ok := err.(*permanentError)
	return ok
}

// awsCredentials represents parsed AWS credentials
type awsCredentials struct {
	profile         string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	region          string
}

// awsCredentialsFile represents a parsed AWS credentials file with multiple profiles
type awsCredentialsFile struct {
	profiles map[string]*awsCredentials
}

// IAMClient interface for AWS IAM operations
type IAMClient interface {
	CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

// STSClient interface for AWS STS operations
type STSClient interface {
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// awsCredentialsRotator implements reconcile.Reconciler for rotating AWS credentials
type awsCredentialsRotator struct {
	client     client.Client
	kubeClient kubernetes.Interface
	logger     logr.Logger
	iamClient  IAMClient
	stsClient  STSClient
	httpClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	stsClientCache map[string]STSClient
	stsClientLock  sync.RWMutex
	// For testing purposes
	keyDeletionDelay time.Duration
}

// NewAWSCredentialsRotator creates a new reconciler for rotating AWS credentials
func NewAWSCredentialsRotator(client client.Client, kubeClient kubernetes.Interface, logger logr.Logger) reconcile.Reconciler {
	return &awsCredentialsRotator{
		client:           client,
		kubeClient:       kubeClient,
		logger:           logger,
		httpClient:       http.DefaultClient,
		stsClientCache:   make(map[string]STSClient),
		keyDeletionDelay: 30 * time.Second, // Default delay
	}
}

// parseCredentialsFile parses an AWS credentials file with multiple profiles
func parseCredentialsFile(data string) *awsCredentialsFile {
	file := &awsCredentialsFile{
		profiles: make(map[string]*awsCredentials),
	}

	var currentProfile string
	var currentCreds *awsCredentials

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for profile header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if currentProfile != "" && currentCreds != nil {
				file.profiles[currentProfile] = currentCreds
			}
			currentProfile = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			currentCreds = &awsCredentials{profile: currentProfile}
			continue
		}

		// Parse key-value pairs within a profile
		if currentCreds != nil {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "aws_access_key_id":
				currentCreds.accessKeyID = value
			case "aws_secret_access_key":
				currentCreds.secretAccessKey = value
			case "aws_session_token":
				currentCreds.sessionToken = value
			case "region":
				currentCreds.region = value
			}
		}
	}

	// Add the last profile if exists
	if currentProfile != "" && currentCreds != nil {
		file.profiles[currentProfile] = currentCreds
	}

	return file
}

// formatCredentialsFile formats multiple AWS credential profiles into a credentials file
func formatCredentialsFile(file *awsCredentialsFile) string {
	var b strings.Builder

	// Sort profiles to ensure consistent output
	profiles := make([]string, 0, len(file.profiles))
	for profile := range file.profiles {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)

	for i, profile := range profiles {
		if i > 0 {
			b.WriteString("\n")
		}
		creds := file.profiles[profile]
		b.WriteString(fmt.Sprintf("[%s]\n", profile))
		b.WriteString(fmt.Sprintf("aws_access_key_id = %s\n", creds.accessKeyID))
		b.WriteString(fmt.Sprintf("aws_secret_access_key = %s\n", creds.secretAccessKey))
		if creds.sessionToken != "" {
			b.WriteString(fmt.Sprintf("aws_session_token = %s\n", creds.sessionToken))
		}
		if creds.region != "" {
			b.WriteString(fmt.Sprintf("region = %s\n", creds.region))
		}
	}
	return b.String()
}

// parseDuration parses a duration string in the format "1h2m" into a time.Duration
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// getRotationConfig returns the rotation interval and pre-rotation window from the policy
func getRotationConfig(policy *aigv1a1.BackendSecurityPolicy) (rotationInterval, preRotationWindow time.Duration) {
	rotationInterval = defaultRotationInterval
	preRotationWindow = defaultPreRotationWindow

	if policy.Spec.AWSCredentials != nil && policy.Spec.AWSCredentials.RotationConfig != nil {
		if policy.Spec.AWSCredentials.RotationConfig.RotationInterval != "" {
			if d, err := parseDuration(policy.Spec.AWSCredentials.RotationConfig.RotationInterval); err == nil {
				rotationInterval = d
			}
		}
		if policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow != "" {
			if d, err := parseDuration(policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow); err == nil {
				preRotationWindow = d
			}
		}
	}

	return rotationInterval, preRotationWindow
}

// getOIDCToken obtains an OIDC token using the configured provider
func (r *awsCredentialsRotator) getOIDCToken(ctx context.Context, oidcConfig *aigv1a1.AWSOIDCExchangeToken, namespace string) (string, error) {
	logger := r.logger.WithValues(
		"namespace", namespace,
		"issuer", oidcConfig.OIDC.Provider.Issuer,
		"clientID", oidcConfig.OIDC.ClientID,
	)
	logger.V(1).Info("starting OIDC token acquisition")

	// Get client secret from Kubernetes secret
	if oidcConfig.OIDC.ClientSecret.Name == "" {
		logger.Error(nil, "client secret name is required")
		return "", fmt.Errorf("client secret name is required")
	}

	logger.V(2).Info("retrieving client secret", "secretName", oidcConfig.OIDC.ClientSecret.Name)
	secret, err := r.kubeClient.CoreV1().Secrets(namespace).Get(ctx, string(oidcConfig.OIDC.ClientSecret.Name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "client secret not found", "secretName", oidcConfig.OIDC.ClientSecret.Name)
			return "", fmt.Errorf("client secret %q not found: %w", oidcConfig.OIDC.ClientSecret.Name, err)
		}
		logger.Error(err, "failed to get client secret", "secretName", oidcConfig.OIDC.ClientSecret.Name)
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}

	clientSecret, ok := secret.Data["client-secret"]
	if !ok {
		logger.Error(nil, "client secret data not found in secret", "secretName", oidcConfig.OIDC.ClientSecret.Name)
		return "", fmt.Errorf("client secret data not found in secret %q", oidcConfig.OIDC.ClientSecret.Name)
	}

	// Configure OAuth2 client credentials flow
	tokenURL := fmt.Sprintf("%s/oauth2/token", strings.TrimSuffix(oidcConfig.OIDC.Provider.Issuer, "/"))
	logger.V(2).Info("configuring OAuth2 client credentials flow", "tokenURL", tokenURL)

	config := &clientcredentials.Config{
		ClientID:     oidcConfig.OIDC.ClientID,
		ClientSecret: string(clientSecret),
		TokenURL:     tokenURL,
		Scopes:       []string{"openid"},
	}

	// Create context with custom HTTP client
	ctx = context.WithValue(ctx, oauth2.HTTPClient, r.httpClient)

	// Get token using client credentials grant
	logger.V(2).Info("requesting OAuth token")
	var token *oauth2.Token
	err = r.retryWithExponentialBackoff(ctx, "OIDC token retrieval", func() error {
		var err error
		token, err = config.Token(ctx)
		if err != nil {
			// Only retry on network errors or 5xx responses
			if netErr, ok := err.(net.Error); ok && (netErr.Temporary() || netErr.Timeout()) {
				return err
			}
			if strings.Contains(err.Error(), "5") { // Simple check for 5xx status codes
				return err
			}
			// Don't retry other errors (e.g., 4xx client errors)
			return &permanentError{err: err}
		}
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to get OAuth token")
		return "", fmt.Errorf("failed to get OAuth token: %w", err)
	}
	logger.V(2).Info("successfully obtained OAuth token")

	// Extract ID token from response
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		logger.Error(nil, "ID token not found in OAuth response")
		return "", fmt.Errorf("ID token not found in OAuth response")
	}

	logger.V(1).Info("successfully acquired OIDC token")
	return rawIDToken, nil
}

// getSTSCredentials exchanges an OIDC token for temporary AWS credentials
func (r *awsCredentialsRotator) getSTSCredentials(ctx context.Context, token string, roleARN string, region string) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	logger := r.logger.WithValues("roleARN", roleARN, "region", region)
	logger.V(1).Info("starting STS credentials exchange")

	// Get region-specific STS client
	stsClient, err := r.getSTSClient(ctx, region)
	if err != nil {
		logger.Error(err, "failed to get STS client")
		return nil, fmt.Errorf("failed to get STS client: %w", err)
	}

	sessionName := "ai-gateway-" + time.Now().Format("20060102150405")
	logger.V(2).Info("assuming role with web identity", "sessionName", sessionName)

	input := &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleARN),
		RoleSessionName:  aws.String(sessionName),
		WebIdentityToken: aws.String(token),
	}

	var resp *sts.AssumeRoleWithWebIdentityOutput
	err = r.retryWithExponentialBackoff(ctx, "STS assume role", func() error {
		var err error
		resp, err = stsClient.AssumeRoleWithWebIdentity(ctx, input)
		if err != nil {
			// Check for permanent errors (like invalid role ARN or expired token)
			var ae smithy.APIError
			if errors.As(err, &ae) {
				switch ae.ErrorCode() {
				case "ExpiredTokenException",
					"InvalidIdentityTokenException",
					"MalformedPolicyDocumentException",
					"PackedPolicyTooLargeException":
					return &permanentError{err: err}
				}
			}
			return err // Retry other errors
		}
		return nil
	})

	if err != nil {
		logger.Error(err, "failed to assume role with web identity")
		return nil, fmt.Errorf("failed to assume role with web identity: %w", err)
	}

	if resp.Credentials == nil {
		logger.Error(nil, "no credentials returned from STS")
		return nil, fmt.Errorf("no credentials returned from STS")
	}

	logger.V(1).Info("successfully obtained STS credentials",
		"accessKeyID", *resp.Credentials.AccessKeyId,
		"expiration", resp.Credentials.Expiration.Format(time.RFC3339))
	return resp, nil
}

// getSTSClient returns an STS client for the specified region, creating one if needed
func (r *awsCredentialsRotator) getSTSClient(ctx context.Context, region string) (STSClient, error) {
	r.stsClientLock.RLock()
	if client, ok := r.stsClientCache[region]; ok {
		r.stsClientLock.RUnlock()
		return client, nil
	}
	r.stsClientLock.RUnlock()

	// Create new client
	r.stsClientLock.Lock()
	defer r.stsClientLock.Unlock()

	// Double-check after acquiring write lock
	if client, ok := r.stsClientCache[region]; ok {
		return client, nil
	}

	// Initialize a new STS client for the region
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for region %s: %w", region, err)
	}

	client := sts.NewFromConfig(cfg)
	r.stsClientCache[region] = client
	return client, nil
}

// retryWithExponentialBackoff executes the given operation with exponential backoff
func (r *awsCredentialsRotator) retryWithExponentialBackoff(
	ctx context.Context,
	operation string,
	fn func() error,
) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check if context is cancelled
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during %s: %w", operation, ctx.Err())
		}

		// Execute the operation
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
			// Don't retry permanent errors
			if isPermanentError(err) {
				return err
			}
			// Log the retry attempt
			r.logger.V(1).Info("operation failed, will retry",
				"operation", operation,
				"attempt", attempt+1,
				"maxAttempts", maxRetries,
				"error", err)
		}

		// Calculate backoff duration with jitter
		backoff := float64(baseRetryDelay) * float64(uint(1)<<uint(attempt))
		if backoff > float64(maxRetryDelay) {
			backoff = float64(maxRetryDelay)
		}

		// Add jitter
		jitter := (rand.Float64()*2 - 1) * retryJitterFactor * backoff
		backoff = backoff + jitter

		// Wait before next attempt
		timer := time.NewTimer(time.Duration(backoff))
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("context cancelled during %s retry: %w", operation, ctx.Err())
		case <-timer.C:
			continue
		}
	}

	return fmt.Errorf("operation %s failed after %d attempts: %w", operation, maxRetries, lastErr)
}

// Reconcile handles the rotation of AWS credentials
func (r *awsCredentialsRotator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the BackendSecurityPolicy
	var policy aigv1a1.BackendSecurityPolicy
	if err := r.client.Get(ctx, req.NamespacedName, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get policy: %w", err)
	}

	// Validate rotation interval if specified
	if policy.Spec.AWSCredentials.RotationConfig != nil {
		if _, err := time.ParseDuration(policy.Spec.AWSCredentials.RotationConfig.RotationInterval); err != nil {
			logger.Error(err, "invalid rotation interval")
			return ctrl.Result{}, fmt.Errorf("invalid rotation interval: %w", err)
		}
	}

	// Only process AWS credentials
	if policy.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAWSCredentials {
		return ctrl.Result{}, nil
	}

	// Get rotation configuration
	rotationInterval, preRotationWindow := getRotationConfig(&policy)

	// Check if it's time to rotate credentials
	lastRotation := policy.Annotations[rotationAnnotation]
	if lastRotation != "" {
		lastRotationTime, err := time.Parse(time.RFC3339, lastRotation)
		if err != nil {
			r.logger.Error(err, "failed to parse last rotation timestamp")
		} else {
			timeSinceRotation := time.Since(lastRotationTime)
			timeUntilRotation := rotationInterval - timeSinceRotation
			timeUntilPreRotation := timeUntilRotation - preRotationWindow

			// If we're not in the pre-rotation window yet, schedule next check
			if timeUntilPreRotation > 0 {
				return ctrl.Result{RequeueAfter: timeUntilPreRotation}, nil
			}
		}
	}

	// Handle OIDC authentication if configured
	if policy.Spec.AWSCredentials.OIDCExchangeToken != nil {
		oidcConfig := policy.Spec.AWSCredentials.OIDCExchangeToken

		// Get OIDC token
		token, err := r.getOIDCToken(ctx, oidcConfig, req.Namespace)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get OIDC token: %w", err)
		}

		// Exchange OIDC token for AWS credentials
		stsResp, err := r.getSTSCredentials(ctx, token, oidcConfig.AwsRoleArn, policy.Spec.AWSCredentials.Region)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to exchange OIDC token for AWS credentials: %w", err)
		}

		// Create or update credentials file
		credentialsFile := &awsCredentialsFile{
			profiles: map[string]*awsCredentials{
				defaultProfile: {
					profile:         defaultProfile,
					accessKeyID:     *stsResp.Credentials.AccessKeyId,
					secretAccessKey: *stsResp.Credentials.SecretAccessKey,
					sessionToken:    *stsResp.Credentials.SessionToken,
					region:          policy.Spec.AWSCredentials.Region,
				},
			},
		}

		// Create or update secret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-oidc-creds", policy.Name),
				Namespace: req.Namespace,
			},
			Data: map[string][]byte{
				credentialsKey: []byte(formatCredentialsFile(credentialsFile)),
			},
		}

		err = r.retryWithExponentialBackoff(ctx, "secret operation", func() error {
			if err := r.client.Create(ctx, secret); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return err
				}
				if err := r.client.Update(ctx, secret); err != nil {
					// Don't retry if the error is due to conflict
					if apierrors.IsConflict(err) {
						return &permanentError{err: err}
					}
					return err
				}
			}
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to manage credentials secret: %w", err)
		}

		// Update annotation with rotation timestamp
		if policy.Annotations == nil {
			policy.Annotations = make(map[string]string)
		}
		policy.Annotations[rotationAnnotation] = time.Now().Format(time.RFC3339)

		err = r.retryWithExponentialBackoff(ctx, "policy update", func() error {
			if err := r.client.Update(ctx, &policy); err != nil {
				if apierrors.IsConflict(err) {
					return &permanentError{err: err}
				}
				return err
			}
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update policy: %w", err)
		}

		// Schedule next rotation based on token expiry or default interval
		nextRotation := rotationInterval
		if stsResp.Credentials.Expiration != nil {
			expiry := time.Until(*stsResp.Credentials.Expiration)
			if expiry < rotationInterval {
				nextRotation = expiry - preRotationWindow
			}
		}

		return ctrl.Result{RequeueAfter: nextRotation}, nil
	}

	// Handle static credentials rotation
	if policy.Spec.AWSCredentials.CredentialsFile == nil {
		return ctrl.Result{}, nil
	}

	// Validate required fields
	if policy.Spec.AWSCredentials.Region == "" {
		r.logger.Error(nil, "AWS region is required", "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("AWS region is required")
	}

	if policy.Spec.AWSCredentials.CredentialsFile.SecretRef == nil || policy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name == "" {
		r.logger.Error(nil, "AWS credentials secret reference is required", "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("AWS credentials secret reference is required")
	}

	// Get the secret containing AWS credentials
	secretRef := policy.Spec.AWSCredentials.CredentialsFile.SecretRef
	secret, err := r.kubeClient.CoreV1().Secrets(req.Namespace).Get(ctx, string(secretRef.Name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.logger.Error(err, "AWS credentials secret not found", "secret", secretRef.Name, "policy", req.NamespacedName)
			return ctrl.Result{}, fmt.Errorf("AWS credentials secret %q not found", secretRef.Name)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Secret: %w", err)
	}

	// Validate secret has required data
	if secret.Data == nil || secret.Data[credentialsKey] == nil {
		r.logger.Error(nil, "AWS credentials secret is missing required data", "secret", secretRef.Name, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("AWS credentials secret %q is missing required data", secretRef.Name)
	}

	// Parse existing credentials file
	credentialsData := string(secret.Data[credentialsKey])
	credentialsFile := parseCredentialsFile(credentialsData)

	// Get the profile to rotate (default if not specified)
	profile := defaultProfile
	if policy.Spec.AWSCredentials.CredentialsFile.Profile != "" {
		profile = policy.Spec.AWSCredentials.CredentialsFile.Profile
	}

	// Check if profile exists
	currentCreds, exists := credentialsFile.profiles[profile]
	if !exists {
		r.logger.Error(nil, "AWS credentials profile not found", "profile", profile, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("AWS credentials profile %q not found", profile)
	}

	// Initialize IAM client if not already done
	if r.iamClient == nil {
		cfg, err := config.LoadDefaultConfig(
			ctx,
			config.WithRegion(policy.Spec.AWSCredentials.Region),
			config.WithCredentialsProvider(aws.CredentialsProviderFunc(
				func(ctx context.Context) (aws.Credentials, error) {
					return aws.Credentials{
						AccessKeyID:     currentCreds.accessKeyID,
						SecretAccessKey: currentCreds.secretAccessKey,
						SessionToken:    currentCreds.sessionToken,
					}, nil
				},
			)),
		)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to load AWS config: %w", err)
		}
		r.iamClient = iam.NewFromConfig(cfg)
	}

	// Create new access key
	result, err := r.iamClient.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create new access key: %w", err)
	}

	// Update credentials for the specific profile
	credentialsFile.profiles[profile] = &awsCredentials{
		profile:         profile,
		accessKeyID:     *result.AccessKey.AccessKeyId,
		secretAccessKey: *result.AccessKey.SecretAccessKey,
		region:          policy.Spec.AWSCredentials.Region,
		sessionToken:    currentCreds.sessionToken, // Preserve session token if it exists
	}

	// Update secret with new credentials while preserving other profiles
	secret.Data[credentialsKey] = []byte(formatCredentialsFile(credentialsFile))
	if _, err := r.kubeClient.CoreV1().Secrets(req.Namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Secret: %w", err)
	}

	// Delete old access key after a delay to ensure the new one is being used
	if r.keyDeletionDelay > 0 {
		time.Sleep(r.keyDeletionDelay)
	}
	if currentCreds.accessKeyID != "" {
		_, err = r.iamClient.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
			AccessKeyId: aws.String(currentCreds.accessKeyID),
		})
		if err != nil {
			r.logger.Error(err, "failed to delete old access key", "accessKeyID", currentCreds.accessKeyID)
		}
	}

	// Update annotation with rotation timestamp
	if policy.Annotations == nil {
		policy.Annotations = make(map[string]string)
	}
	policy.Annotations[rotationAnnotation] = time.Now().Format(time.RFC3339)

	err = r.retryWithExponentialBackoff(ctx, "policy update", func() error {
		if err := r.client.Update(ctx, &policy); err != nil {
			if apierrors.IsConflict(err) {
				return &permanentError{err: err}
			}
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update policy: %w", err)
	}

	// Schedule next rotation
	return ctrl.Result{RequeueAfter: rotationInterval}, nil
}

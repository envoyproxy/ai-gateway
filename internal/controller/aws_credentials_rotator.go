package controller

import (
	"context"
	"fmt"
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
	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
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
	// defaultKeyDeletionDelay is the default delay before deleting old access keys
	defaultKeyDeletionDelay = 30 * time.Second
	// oauthTokenEndpointPath is the path for OAuth2 token endpoint
	oauthTokenEndpointPath = "/oauth2/token"
	// oauthOpenIDScope is the default scope for OpenID Connect
	oauthOpenIDScope = "openid"
	// idTokenKey is the key for ID token in OAuth response
	idTokenKey = "id_token"

	// Retry configuration
	maxRetries        = 3
	baseRetryDelay    = 1 * time.Second
	maxRetryDelay     = 10 * time.Second
	retryJitterFactor = 0.1
)

// AWS session name format
const awsSessionNameFormat = "ai-gateway-%s"

// Error messages
const (
	errMsgClientSecretRequired    = "client secret name is required"
	errMsgClientSecretNotFound    = "client secret %q not found"
	errMsgClientSecretDataMissing = "client secret data not found in secret %q"
	errMsgIDTokenNotFound         = "ID token not found in OAuth response"
	errMsgSTSCredsNotFound        = "no credentials returned from STS"
	errMsgAWSRegionRequired       = "AWS region is required"
	errMsgAWSCredsSecretRequired  = "AWS credentials secret reference is required"
	errMsgAWSCredsSecretMissing   = "AWS credentials secret %q not found"
	errMsgAWSCredsDataMissing     = "AWS credentials secret %q is missing required data"
	errMsgAWSProfileNotFound      = "AWS credentials profile %q not found"
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

// IAMOperations interface for AWS IAM operations
type IAMOperations interface {
	CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

// STSOperations interface for AWS STS operations
type STSOperations interface {
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// awsCredentialsRotator implements reconcile.Reconciler for rotating AWS credentials
type awsCredentialsRotator struct {
	k8sClient    client.Client
	k8sClientset kubernetes.Interface
	logger       logr.Logger
	iamOps       IAMOperations
	stsOps       STSOperations
	httpClient   interface {
		Do(*http.Request) (*http.Response, error)
	}
	stsClientByRegion map[string]STSOperations
	stsClientMutex    sync.RWMutex
	// For testing purposes
	keyDeletionDelay time.Duration
}

// NewAWSCredentialsRotator creates a new reconciler for rotating AWS credentials
func NewAWSCredentialsRotator(k8sClient client.Client, k8sClientset kubernetes.Interface, logger logr.Logger) reconcile.Reconciler {
	return &awsCredentialsRotator{
		k8sClient:         k8sClient,
		k8sClientset:      k8sClientset,
		logger:            logger,
		httpClient:        http.DefaultClient,
		stsClientByRegion: make(map[string]STSOperations),
		keyDeletionDelay:  defaultKeyDeletionDelay,
	}
}

// parseAWSCredentialsFile parses an AWS credentials file with multiple profiles
func parseAWSCredentialsFile(data string) *awsCredentialsFile {
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

// formatAWSCredentialsFile formats multiple AWS credential profiles into a credentials file
func formatAWSCredentialsFile(file *awsCredentialsFile) string {
	var builder strings.Builder

	// Sort profiles to ensure consistent output
	profileNames := getSortedProfileNames(file.profiles)

	for i, profileName := range profileNames {
		if i > 0 {
			builder.WriteString("\n")
		}
		creds := file.profiles[profileName]
		builder.WriteString(fmt.Sprintf("[%s]\n", profileName))
		builder.WriteString(fmt.Sprintf("aws_access_key_id = %s\n", creds.accessKeyID))
		builder.WriteString(fmt.Sprintf("aws_secret_access_key = %s\n", creds.secretAccessKey))
		if creds.sessionToken != "" {
			builder.WriteString(fmt.Sprintf("aws_session_token = %s\n", creds.sessionToken))
		}
		if creds.region != "" {
			builder.WriteString(fmt.Sprintf("region = %s\n", creds.region))
		}
	}
	return builder.String()
}

// getSortedProfileNames returns a sorted list of profile names
func getSortedProfileNames(profiles map[string]*awsCredentials) []string {
	profileNames := make([]string, 0, len(profiles))
	for profileName := range profiles {
		profileNames = append(profileNames, profileName)
	}
	sort.Strings(profileNames)
	return profileNames
}

// parseDurationWithDefault parses a duration string with a default fallback
func parseDurationWithDefault(s string, defaultDuration time.Duration) time.Duration {
	if s == "" {
		return defaultDuration
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return defaultDuration
	}
	return duration
}

// getRotationConfig returns the rotation interval and pre-rotation window from the policy
func getRotationConfig(policy *aigv1a1.BackendSecurityPolicy) (rotationInterval, preRotationWindow time.Duration) {
	rotationInterval = defaultRotationInterval
	preRotationWindow = defaultPreRotationWindow

	if policy.Spec.AWSCredentials != nil && policy.Spec.AWSCredentials.RotationConfig != nil {
		rotationInterval = parseDurationWithDefault(policy.Spec.AWSCredentials.RotationConfig.RotationInterval, defaultRotationInterval)
		preRotationWindow = parseDurationWithDefault(policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow, defaultPreRotationWindow)
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
		logger.Error(nil, errMsgClientSecretRequired)
		return "", fmt.Errorf(errMsgClientSecretRequired)
	}

	logger.V(2).Info("retrieving client secret", "secretName", oidcConfig.OIDC.ClientSecret.Name)
	secret, err := r.k8sClientset.CoreV1().Secrets(namespace).Get(ctx, string(oidcConfig.OIDC.ClientSecret.Name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, errMsgClientSecretNotFound, "secretName", oidcConfig.OIDC.ClientSecret.Name)
			return "", fmt.Errorf(errMsgClientSecretNotFound, oidcConfig.OIDC.ClientSecret.Name)
		}
		logger.Error(err, "failed to get client secret", "secretName", oidcConfig.OIDC.ClientSecret.Name)
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}

	clientSecret, ok := secret.Data["client-secret"]
	if !ok {
		logger.Error(nil, errMsgClientSecretDataMissing, "secretName", oidcConfig.OIDC.ClientSecret.Name)
		return "", fmt.Errorf(errMsgClientSecretDataMissing, oidcConfig.OIDC.ClientSecret.Name)
	}

	// Configure OAuth2 client credentials flow
	tokenURL := fmt.Sprintf("%s%s", strings.TrimSuffix(oidcConfig.OIDC.Provider.Issuer, "/"), oauthTokenEndpointPath)
	logger.V(2).Info("configuring OAuth2 client credentials flow", "tokenURL", tokenURL)

	config := &clientcredentials.Config{
		ClientID:     oidcConfig.OIDC.ClientID,
		ClientSecret: string(clientSecret),
		TokenURL:     tokenURL,
		Scopes:       []string{oauthOpenIDScope},
	}

	// Create context with custom HTTP client
	ctx = context.WithValue(ctx, oauth2.HTTPClient, r.httpClient)

	// Get token using client credentials grant
	logger.V(2).Info("requesting OAuth token")
	var token *oauth2.Token
	err = r.retryOperation(ctx, "OIDC token retrieval", func() error {
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
	rawIDToken, ok := token.Extra(idTokenKey).(string)
	if !ok {
		logger.Error(nil, errMsgIDTokenNotFound)
		return "", fmt.Errorf(errMsgIDTokenNotFound)
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
	resp, err = stsClient.AssumeRoleWithWebIdentity(ctx, input)
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
func (r *awsCredentialsRotator) getSTSClient(ctx context.Context, region string) (STSOperations, error) {
	r.stsClientMutex.RLock()
	if client, exists := r.stsClientByRegion[region]; exists {
		r.stsClientMutex.RUnlock()
		return client, nil
	}
	r.stsClientMutex.RUnlock()

	// Create new client
	r.stsClientMutex.Lock()
	defer r.stsClientMutex.Unlock()

	// Double-check after acquiring write lock
	if client, exists := r.stsClientByRegion[region]; exists {
		return client, nil
	}

	// Initialize a new STS client for the region with retry configuration
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithRetryMode(aws.RetryModeStandard),
		config.WithRetryMaxAttempts(maxRetries),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for region %s: %w", region, err)
	}

	client := sts.NewFromConfig(cfg)
	r.stsClientByRegion[region] = client
	return client, nil
}

// Initialize IAM client with retry configuration
func (r *awsCredentialsRotator) initializeIAMClient(ctx context.Context, region string, creds *awsCredentials) error {
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(region),
		config.WithRetryMode(aws.RetryModeStandard),
		config.WithRetryMaxAttempts(maxRetries),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(
			func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     creds.accessKeyID,
					SecretAccessKey: creds.secretAccessKey,
					SessionToken:    creds.sessionToken,
				}, nil
			},
		)),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	r.iamOps = iam.NewFromConfig(cfg)
	return nil
}

// retryOperation executes the given operation with exponential backoff using k8s retry utilities
func (r *awsCredentialsRotator) retryOperation(ctx context.Context, operation string, fn func() error) error {
	backoff := retry.DefaultBackoff
	backoff.Steps = 3 // Match our original maxRetries value

	var lastErr error
	err := retry.OnError(backoff,
		func(err error) bool {
			// Don't retry if context is cancelled or if it's a permanent error
			if ctx.Err() != nil || isPermanentError(err) {
				return false
			}

			// Log retry attempt
			if err != nil {
				r.logger.V(1).Info("operation failed, will retry",
					"operation", operation,
					"error", err)
				lastErr = err
			}
			return true
		},
		fn)

	if ctx.Err() != nil {
		return fmt.Errorf("context cancelled during %s: %w", operation, ctx.Err())
	}

	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("operation %s failed: %w", operation, lastErr)
		}
		return fmt.Errorf("operation %s failed: %w", operation, err)
	}

	return nil
}

// Reconcile handles the rotation of AWS credentials
func (r *awsCredentialsRotator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the BackendSecurityPolicy
	var policy aigv1a1.BackendSecurityPolicy
	if err := r.k8sClient.Get(ctx, req.NamespacedName, &policy); err != nil {
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
				credentialsKey: []byte(formatAWSCredentialsFile(credentialsFile)),
			},
		}

		err = r.retryOperation(ctx, "secret operation", func() error {
			if err := r.k8sClient.Create(ctx, secret); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return err
				}
				if err := r.k8sClient.Update(ctx, secret); err != nil {
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

		err = r.retryOperation(ctx, "policy update", func() error {
			if err := r.k8sClient.Update(ctx, &policy); err != nil {
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
		r.logger.Error(nil, errMsgAWSRegionRequired, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf(errMsgAWSRegionRequired)
	}

	if policy.Spec.AWSCredentials.CredentialsFile.SecretRef == nil || policy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name == "" {
		r.logger.Error(nil, errMsgAWSCredsSecretRequired, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf(errMsgAWSCredsSecretRequired)
	}

	// Get the secret containing AWS credentials
	secretRef := policy.Spec.AWSCredentials.CredentialsFile.SecretRef
	secret, err := r.k8sClientset.CoreV1().Secrets(req.Namespace).Get(ctx, string(secretRef.Name), metav1.GetOptions{})
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
	credentialsFile := parseAWSCredentialsFile(credentialsData)

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
	if r.iamOps == nil {
		if err := r.initializeIAMClient(ctx, policy.Spec.AWSCredentials.Region, currentCreds); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Create new access key
	result, err := r.iamOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
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
	secret.Data[credentialsKey] = []byte(formatAWSCredentialsFile(credentialsFile))
	if _, err := r.k8sClientset.CoreV1().Secrets(req.Namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Secret: %w", err)
	}

	// Delete old access key after a delay to ensure the new one is being used
	if r.keyDeletionDelay > 0 {
		time.Sleep(r.keyDeletionDelay)
	}
	if currentCreds.accessKeyID != "" {
		_, err = r.iamOps.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
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

	err = r.retryOperation(ctx, "policy update", func() error {
		if err := r.k8sClient.Update(ctx, &policy); err != nil {
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

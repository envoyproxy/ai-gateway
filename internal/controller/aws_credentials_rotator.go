package controller

import (
	"context"
	"fmt"
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
	errMsgSecretModified          = "Operation cannot be fulfilled on secrets \"aws-credentials\": object was modified"
)

// Error prefixes
const (
	errPrefixOIDCToken = "failed to get OIDC token: "
	errPrefixOAuth     = "failed to get OAuth token: "
	errPrefixOperation = "operation OIDC token retrieval failed: "
	errPrefixSecret    = "failed to update Secret: "
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
func getRotationConfig(policy *aigv1a1.BackendSecurityPolicy) (time.Duration, time.Duration) {
	var rotationInterval, preRotationWindow time.Duration

	if policy.Spec.AWSCredentials.RotationConfig != nil {
		if policy.Spec.AWSCredentials.RotationConfig.RotationInterval != "" {
			if interval, err := time.ParseDuration(policy.Spec.AWSCredentials.RotationConfig.RotationInterval); err == nil {
				rotationInterval = interval
			}
		}
		if policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow != "" {
			if window, err := time.ParseDuration(policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow); err == nil {
				preRotationWindow = window
			}
		}
	}

	// Use defaults if not set or invalid
	if rotationInterval <= 0 {
		rotationInterval = defaultRotationInterval
	}
	if preRotationWindow <= 0 {
		preRotationWindow = defaultPreRotationWindow
	}

	// Ensure pre-rotation window is less than rotation interval
	if preRotationWindow >= rotationInterval {
		preRotationWindow = rotationInterval / 2
	}

	return rotationInterval, preRotationWindow
}

// getOIDCToken retrieves an OIDC token using client credentials
func (r *awsCredentialsRotator) getOIDCToken(ctx context.Context, config *aigv1a1.AWSOIDCExchangeToken, namespace string) (string, error) {
	if config.OIDC.ClientSecret.Name == "" {
		return "", fmt.Errorf(errPrefixOIDCToken + errMsgClientSecretRequired)
	}

	// Get client secret
	secret, err := r.k8sClientset.CoreV1().Secrets(namespace).Get(ctx, string(config.OIDC.ClientSecret.Name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf(errPrefixOIDCToken+errMsgClientSecretNotFound, config.OIDC.ClientSecret.Name)
		}
		return "", fmt.Errorf(errPrefixOIDCToken+errPrefixOperation+"failed to get client secret: %w", err)
	}

	if secret.Data == nil || secret.Data["client-secret"] == nil {
		return "", fmt.Errorf(errPrefixOIDCToken+errMsgClientSecretDataMissing, config.OIDC.ClientSecret.Name)
	}

	clientID := config.OIDC.ClientID
	clientSecret := string(secret.Data["client-secret"])

	// Configure OAuth2 client with custom HTTP client
	ctx = context.WithValue(ctx, oauth2.HTTPClient, r.httpClient)
	oauthConfig := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     config.OIDC.Provider.Issuer + oauthTokenEndpointPath,
		Scopes:       []string{oauthOpenIDScope},
	}

	// Get OAuth token
	token, err := oauthConfig.Token(ctx)
	if err != nil {
		return "", fmt.Errorf(errPrefixOIDCToken+errPrefixOAuth+errPrefixOperation+"%w", err)
	}

	// Extract ID token
	rawIDToken, ok := token.Extra(idTokenKey).(string)
	if !ok {
		return "", fmt.Errorf(errPrefixOIDCToken + errMsgIDTokenNotFound)
	}

	return rawIDToken, nil
}

// getSTSCredentials exchanges an OIDC token for AWS credentials
func (r *awsCredentialsRotator) getSTSCredentials(ctx context.Context, token, roleArn, region string) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	// Initialize STS client if not already done
	r.stsClientMutex.Lock()
	if r.stsClientByRegion[region] == nil {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			r.stsClientMutex.Unlock()
			return nil, fmt.Errorf(errPrefixOperation+"failed to load AWS config: %w", err)
		}
		r.stsClientByRegion[region] = sts.NewFromConfig(cfg)
	}
	stsClient := r.stsClientByRegion[region]
	r.stsClientMutex.Unlock()

	// Exchange OIDC token for AWS credentials
	sessionName := fmt.Sprintf(awsSessionNameFormat, "oidc")
	input := &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleArn),
		RoleSessionName:  aws.String(sessionName),
		WebIdentityToken: aws.String(token),
	}

	resp, err := stsClient.AssumeRoleWithWebIdentity(ctx, input)
	if err != nil {
		return nil, fmt.Errorf(errPrefixOperation+"failed to assume role with web identity: %w", err)
	}

	if resp.Credentials == nil {
		return nil, fmt.Errorf(errMsgSTSCredsNotFound)
	}

	return resp, nil
}

// initializeIAMClient initializes the IAM client with the given credentials
func (r *awsCredentialsRotator) initializeIAMClient(ctx context.Context, region string, creds *awsCredentials) error {
	// Create AWS credentials provider
	provider := aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		return aws.Credentials{
			AccessKeyID:     creds.accessKeyID,
			SecretAccessKey: creds.secretAccessKey,
			SessionToken:    creds.sessionToken,
		}, nil
	})

	// Initialize AWS config with credentials and region
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(provider),
	)
	if err != nil {
		return fmt.Errorf(errPrefixOperation+"failed to load AWS config: %w", err)
	}

	// Create IAM client
	r.iamOps = iam.NewFromConfig(cfg)
	return nil
}

// retryOperation retries an operation with exponential backoff
func (r *awsCredentialsRotator) retryOperation(ctx context.Context, operationName string, operation func() error) error {
	backoff := retry.DefaultBackoff
	backoff.Duration = baseRetryDelay
	backoff.Cap = maxRetryDelay
	backoff.Factor = 2.0
	backoff.Jitter = retryJitterFactor

	var lastErr error
	err := retry.OnError(backoff, func(err error) bool {
		if ctx.Err() != nil || isPermanentError(err) {
			return false
		}
		lastErr = err
		r.logger.Error(err, fmt.Sprintf("retrying %s after error", operationName))
		return true
	}, operation)

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("operation %s cancelled: %w", operationName, ctx.Err())
		}
		if lastErr != nil {
			return fmt.Errorf("operation %s failed after retries: %w", operationName, lastErr)
		}
		return fmt.Errorf("operation %s failed: %w", operationName, err)
	}
	return nil
}

// validateRotationConfig validates the rotation configuration if specified
func (r *awsCredentialsRotator) validateRotationConfig(policy *aigv1a1.BackendSecurityPolicy) error {
	if policy.Spec.AWSCredentials.RotationConfig != nil {
		if policy.Spec.AWSCredentials.RotationConfig.RotationInterval != "" {
			interval, err := time.ParseDuration(policy.Spec.AWSCredentials.RotationConfig.RotationInterval)
			if err != nil {
				return fmt.Errorf("invalid rotation interval: %w", err)
			}
			if interval < time.Hour {
				return fmt.Errorf("rotation interval must be at least 1 hour")
			}
		}
		if policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow != "" {
			window, err := time.ParseDuration(policy.Spec.AWSCredentials.RotationConfig.PreRotationWindow)
			if err != nil {
				return fmt.Errorf("invalid pre-rotation window: %w", err)
			}
			if window < 0 {
				return fmt.Errorf("pre-rotation window must be positive")
			}
		}
	}
	return nil
}

// shouldRotateCredentials checks if credentials need to be rotated
func (r *awsCredentialsRotator) shouldRotateCredentials(policy *aigv1a1.BackendSecurityPolicy) (time.Duration, bool, error) {
	rotationInterval, preRotationWindow := getRotationConfig(policy)

	// If no last rotation timestamp, rotate immediately
	lastRotation := policy.Annotations[rotationAnnotation]
	if lastRotation == "" {
		return 0, true, nil
	}

	// Parse last rotation timestamp
	lastRotationTime, err := time.Parse(time.RFC3339, lastRotation)
	if err != nil {
		return 0, false, fmt.Errorf(errPrefixOperation+"failed to parse last rotation timestamp: %w", err)
	}

	// Calculate time until next rotation
	timeSinceRotation := time.Since(lastRotationTime)
	timeUntilRotation := rotationInterval - timeSinceRotation
	timeUntilPreRotation := timeUntilRotation - preRotationWindow

	// If we're not in the pre-rotation window yet, schedule next check
	if timeUntilPreRotation > 0 {
		return timeUntilPreRotation, false, nil
	}

	return 0, true, nil
}

// handleOIDCCredentials handles the OIDC-based credentials rotation
func (r *awsCredentialsRotator) handleOIDCCredentials(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, req ctrl.Request) (ctrl.Result, error) {
	oidcConfig := policy.Spec.AWSCredentials.OIDCExchangeToken

	// Validate required fields
	if policy.Spec.AWSCredentials.Region == "" {
		r.logger.Error(nil, errMsgAWSRegionRequired, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf(errMsgAWSRegionRequired)
	}

	// Get OIDC token
	token, err := r.getOIDCToken(ctx, oidcConfig, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err // Error already properly formatted in getOIDCToken
	}

	// Exchange OIDC token for AWS credentials
	stsResp, err := r.getSTSCredentials(ctx, token, oidcConfig.AwsRoleArn, policy.Spec.AWSCredentials.Region)
	if err != nil {
		return ctrl.Result{}, err // Error already properly formatted in getSTSCredentials
	}

	if stsResp.Credentials == nil {
		return ctrl.Result{}, fmt.Errorf(errMsgSTSCredsNotFound)
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
	if err := r.updateCredentialsSecret(ctx, policy, req.Namespace, credentialsFile); err != nil {
		return ctrl.Result{}, err // Error already properly formatted in updateCredentialsSecret
	}

	// Update rotation timestamp
	if err := r.updateRotationTimestamp(ctx, policy); err != nil {
		return ctrl.Result{}, err // Error already properly formatted in updateRotationTimestamp
	}

	// Schedule next rotation based on token expiry or default interval
	rotationInterval, preRotationWindow := getRotationConfig(policy)
	nextRotation := rotationInterval
	if stsResp.Credentials.Expiration != nil {
		expiry := time.Until(*stsResp.Credentials.Expiration)
		if expiry < rotationInterval {
			nextRotation = expiry - preRotationWindow
		}
	}

	return ctrl.Result{RequeueAfter: nextRotation}, nil
}

// handleStaticCredentials handles the static credentials rotation
func (r *awsCredentialsRotator) handleStaticCredentials(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, req ctrl.Request) (ctrl.Result, error) {
	// Validate required fields
	if policy.Spec.AWSCredentials.Region == "" {
		r.logger.Error(nil, errMsgAWSRegionRequired, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf(errMsgAWSRegionRequired)
	}

	if policy.Spec.AWSCredentials.CredentialsFile == nil || policy.Spec.AWSCredentials.CredentialsFile.SecretRef == nil || policy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name == "" {
		r.logger.Error(nil, errMsgAWSCredsSecretRequired, "policy", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf(errMsgAWSCredsSecretRequired)
	}

	// Get and validate existing credentials
	credentialsFile, currentCreds, err := r.getExistingCredentials(ctx, policy, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err // Error already properly formatted in getExistingCredentials
	}

	// Create new access key
	result, err := r.createNewAccessKey(ctx, policy, currentCreds)
	if err != nil {
		return ctrl.Result{}, err // Error already properly formatted in createNewAccessKey
	}

	// Update credentials file with new key
	credentialsFile.profiles[currentCreds.profile] = &awsCredentials{
		profile:         currentCreds.profile,
		accessKeyID:     *result.AccessKey.AccessKeyId,
		secretAccessKey: *result.AccessKey.SecretAccessKey,
		region:          policy.Spec.AWSCredentials.Region,
		sessionToken:    currentCreds.sessionToken, // Preserve session token if it exists
	}

	// Update secret with new credentials
	if err := r.updateCredentialsSecret(ctx, policy, req.Namespace, credentialsFile); err != nil {
		// Delete the newly created access key if we failed to update the secret
		if err := r.deleteOldAccessKey(ctx, &awsCredentials{accessKeyID: *result.AccessKey.AccessKeyId}); err != nil {
			r.logger.Error(err, "failed to delete new access key after secret update failure", "accessKeyID", *result.AccessKey.AccessKeyId)
		}
		return ctrl.Result{}, err // Error already properly formatted in updateCredentialsSecret
	}

	// Delete old access key after delay
	if err := r.deleteOldAccessKey(ctx, currentCreds); err != nil {
		r.logger.Error(err, "failed to delete old access key", "accessKeyID", currentCreds.accessKeyID)
	}

	// Update rotation timestamp
	if err := r.updateRotationTimestamp(ctx, policy); err != nil {
		return ctrl.Result{}, err // Error already properly formatted in updateRotationTimestamp
	}

	rotationInterval, _ := getRotationConfig(policy)
	return ctrl.Result{RequeueAfter: rotationInterval}, nil
}

// updateCredentialsSecret creates or updates the credentials secret
func (r *awsCredentialsRotator) updateCredentialsSecret(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, namespace string, credentialsFile *awsCredentialsFile) error {
	// Determine secret name based on credential type
	secretName := "aws-credentials"
	if policy.Spec.AWSCredentials.OIDCExchangeToken != nil {
		secretName = fmt.Sprintf("%s-oidc-creds", policy.Name)
	} else if policy.Spec.AWSCredentials.CredentialsFile != nil {
		secretName = string(policy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			credentialsKey: []byte(formatAWSCredentialsFile(credentialsFile)),
		},
	}

	err := r.retryOperation(ctx, "secret operation", func() error {
		if err := r.k8sClient.Create(ctx, secret); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return err
			}
			if err := r.k8sClient.Update(ctx, secret); err != nil {
				if apierrors.IsConflict(err) {
					return &permanentError{err: fmt.Errorf(errMsgSecretModified)}
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		if isPermanentError(err) {
			return err.(*permanentError).err
		}
		return err
	}
	return nil
}

// updateRotationTimestamp updates the last rotation timestamp annotation
func (r *awsCredentialsRotator) updateRotationTimestamp(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy) error {
	if policy.Annotations == nil {
		policy.Annotations = make(map[string]string)
	}
	policy.Annotations[rotationAnnotation] = time.Now().Format(time.RFC3339)

	err := r.retryOperation(ctx, "policy update", func() error {
		if err := r.k8sClient.Update(ctx, policy); err != nil {
			if apierrors.IsConflict(err) {
				return &permanentError{err: fmt.Errorf(errPrefixOperation+"failed to update policy: %w", err)}
			}
			return fmt.Errorf(errPrefixOperation+"failed to update policy: %w", err)
		}
		return nil
	})
	if err != nil {
		if isPermanentError(err) {
			return err
		}
		return fmt.Errorf(errPrefixOperation+"failed to update rotation timestamp: %w", err)
	}
	return nil
}

// getExistingCredentials retrieves and validates existing credentials
func (r *awsCredentialsRotator) getExistingCredentials(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, namespace string) (*awsCredentialsFile, *awsCredentials, error) {
	secretRef := policy.Spec.AWSCredentials.CredentialsFile.SecretRef
	secret, err := r.k8sClientset.CoreV1().Secrets(namespace).Get(ctx, string(secretRef.Name), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf(errMsgAWSCredsSecretMissing, secretRef.Name)
		}
		return nil, nil, fmt.Errorf(errPrefixSecret+"failed to get Secret: %w", err)
	}

	if secret.Data == nil || secret.Data[credentialsKey] == nil {
		return nil, nil, fmt.Errorf(errMsgAWSCredsDataMissing, secretRef.Name)
	}

	credentialsFile := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))

	profile := defaultProfile
	if policy.Spec.AWSCredentials.CredentialsFile.Profile != "" {
		profile = policy.Spec.AWSCredentials.CredentialsFile.Profile
	}

	currentCreds, exists := credentialsFile.profiles[profile]
	if !exists {
		return nil, nil, fmt.Errorf(errMsgAWSProfileNotFound, profile)
	}

	return credentialsFile, currentCreds, nil
}

// createNewAccessKey creates a new AWS access key
func (r *awsCredentialsRotator) createNewAccessKey(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, currentCreds *awsCredentials) (*iam.CreateAccessKeyOutput, error) {
	// Initialize IAM client if not already done
	if r.iamOps == nil {
		if err := r.initializeIAMClient(ctx, policy.Spec.AWSCredentials.Region, currentCreds); err != nil {
			return nil, fmt.Errorf(errPrefixOperation+"failed to initialize IAM client: %w", err)
		}
	}

	result, err := r.iamOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return nil, fmt.Errorf(errPrefixOperation+"failed to create new access key: %w", err)
	}

	if result.AccessKey == nil {
		return nil, fmt.Errorf(errPrefixOperation + "no access key returned from IAM")
	}

	return result, nil
}

// deleteOldAccessKey deletes the old AWS access key after a delay
func (r *awsCredentialsRotator) deleteOldAccessKey(ctx context.Context, currentCreds *awsCredentials) error {
	if r.keyDeletionDelay > 0 {
		time.Sleep(r.keyDeletionDelay)
	}

	if currentCreds.accessKeyID == "" {
		return nil
	}

	_, err := r.iamOps.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		AccessKeyId: aws.String(currentCreds.accessKeyID),
	})
	if err != nil {
		return fmt.Errorf(errPrefixOperation+"failed to delete access key %s: %w", currentCreds.accessKeyID, err)
	}

	return nil
}

// Reconcile handles the rotation of AWS credentials
func (r *awsCredentialsRotator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the BackendSecurityPolicy
	var policy aigv1a1.BackendSecurityPolicy
	if err := r.k8sClient.Get(ctx, req.NamespacedName, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf(errPrefixOperation+"failed to get BackendSecurityPolicy: %w", err)
	}

	// Skip if not AWS credentials type
	if policy.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAWSCredentials {
		return ctrl.Result{}, nil
	}

	// Validate rotation configuration
	if err := r.validateRotationConfig(&policy); err != nil {
		return ctrl.Result{}, err
	}

	// Check if rotation is needed
	shouldRotate := false
	var nextRotation time.Duration

	if policy.Spec.AWSCredentials.OIDCExchangeToken != nil {
		// For OIDC-based credentials, always rotate to ensure fresh tokens
		shouldRotate = true
		result, err := r.handleOIDCCredentials(ctx, &policy, req)
		if err != nil {
			return ctrl.Result{}, err // Error already properly formatted in handleOIDCCredentials
		}
		return result, nil
	}

	if policy.Spec.AWSCredentials.CredentialsFile != nil {
		// For static credentials, check if rotation is needed
		var err error
		nextRotation, shouldRotate, err = r.shouldRotateCredentials(&policy)
		if err != nil {
			return ctrl.Result{}, err // Error already properly formatted in shouldRotateCredentials
		}

		if shouldRotate {
			result, err := r.handleStaticCredentials(ctx, &policy, req)
			if err != nil {
				return ctrl.Result{}, err // Error already properly formatted in handleStaticCredentials
			}
			return result, nil
		}
	}

	if !shouldRotate {
		return ctrl.Result{RequeueAfter: nextRotation}, nil
	}

	return ctrl.Result{}, nil
}

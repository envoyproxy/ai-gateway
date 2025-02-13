package rotators

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AWSOIDCRotator implements the Rotator interface for AWS OIDC token exchange.
// It manages the lifecycle of temporary AWS credentials obtained through OIDC token
// exchange with AWS STS.
type AWSOIDCRotator struct {
	// ctx provides a user specified context
	ctx context.Context
	// client is used for Kubernetes API operations
	client client.Client
	// kube provides additional Kubernetes API capabilities
	kube kubernetes.Interface
	// logger is used for structured logging
	logger logr.Logger
	// stsOps provides AWS STS operations interface
	stsOps STSOperations
	// backendSecurityPolicyName provides name of backend security policy
	backendSecurityPolicyName string
	// backendSecurityPolicyNamespace provides namespace of backend security policy
	backendSecurityPolicyNamespace string
	// preRotationWindow specifies how long before expiry to rotate
	preRotationWindow time.Duration
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator with the specified configuration.
// It initializes the AWS STS client and sets up the rotation channels.
func NewAWSOIDCRotator(
	ctx context.Context,
	client client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	backendSecurityPolicyNamespace string,
	backendSecurityPolicyName string,
	preRotationWindow time.Duration,
	region string,
) (*AWSOIDCRotator, error) {
	cfg, err := defaultAWSConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	if region != "" {
		cfg.Region = region
	}

	if proxyURL := os.Getenv("AI_GATEWY_STS_PROXY_URL"); proxyURL != "" {
		cfg.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy: func(*http.Request) (*url.URL, error) {
					return url.Parse(proxyURL)
				},
			},
		}
	}

	stsClient := NewSTSClient(cfg)

	return &AWSOIDCRotator{
		client:                         client,
		kube:                           kube,
		logger:                         logger,
		stsOps:                         stsClient,
		backendSecurityPolicyNamespace: backendSecurityPolicyNamespace,
		backendSecurityPolicyName:      backendSecurityPolicyName,
		preRotationWindow:              preRotationWindow,
	}, nil
}

// SetSTSOperations sets the STS operations implementation - primarily used for testing
func (r *AWSOIDCRotator) SetSTSOperations(ops STSOperations) {
	r.stsOps = ops
}

// IsExpired checks if the preRotation time is before the current time.
func (r *AWSOIDCRotator) IsExpired() bool {
	preRotationExpirationTime := r.GetPreRotationTime()
	if preRotationExpirationTime == nil {
		return true
	}
	return IsExpired(0, *preRotationExpirationTime)
}

// GetPreRotationTime gets the expiration time minus the preRotation interval.
func (r *AWSOIDCRotator) GetPreRotationTime() *time.Time {
	secret, err := LookupSecret(r.ctx, r.client, r.backendSecurityPolicyNamespace, r.backendSecurityPolicyName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil
		}
		return nil
	}
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	if err != nil {
		return nil
	}
	preRotationTime := expirationTime.Add(-r.preRotationWindow)
	return &preRotationTime
}

// Rotate implements the retrieval and storage of AWS sts credentials
func (r *AWSOIDCRotator) Rotate(region, roleARN, token string) error {
	r.logger.Info("rotating AWS sts temporary credentials",
		"namespace", r.backendSecurityPolicyNamespace,
		"name", r.backendSecurityPolicyName)

	result, err := r.assumeRoleWithToken(roleARN, token)
	if err != nil {
		r.logger.Error(err, "failed to assume role", "role", roleARN, "ID", token)
		return err
	}

	secret, err := LookupSecret(r.ctx, r.client, r.backendSecurityPolicyNamespace, r.backendSecurityPolicyName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		secret = newSecret(r.backendSecurityPolicyNamespace, r.backendSecurityPolicyName)
	}

	updateExpirationSecretAnnotation(secret, *result.Credentials.Expiration)

	// For now have profile as default
	profile := "default"
	credsFile := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			profile: {
				profile:         profile,
				accessKeyID:     aws.ToString(result.Credentials.AccessKeyId),
				secretAccessKey: aws.ToString(result.Credentials.SecretAccessKey),
				sessionToken:    aws.ToString(result.Credentials.SessionToken),
				region:          region,
			},
		},
	}

	updateAWSCredentialsInSecret(secret, credsFile)
	return updateSecret(r.ctx, r.client, secret)
}

// assumeRoleWithToken exchanges an OIDC token for AWS credentials
func (r *AWSOIDCRotator) assumeRoleWithToken(roleARN, token string) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return r.stsOps.AssumeRoleWithWebIdentity(r.ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleARN),
		WebIdentityToken: aws.String(token),
		RoleSessionName:  aws.String(fmt.Sprintf(awsSessionNameFormat, r.backendSecurityPolicyName)),
	})
}

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AWSAssumeRoleRotator implements the Rotator interface for AWS credential file-based
// role assumption. It reads base credentials (access key + secret key) from a user-provided
// secret, performs STS AssumeRole, and stores the resulting temporary credentials in a
// generated secret.
type AWSAssumeRoleRotator struct {
	// client is used for Kubernetes API operations.
	client client.Client
	// kube provides additional Kubernetes API capabilities.
	kube kubernetes.Interface
	// logger is used for structured logging.
	logger logr.Logger
	// stsClient provides AWS STS operations interface.
	stsClient STSClient
	// backendSecurityPolicyName provides name of backend security policy.
	backendSecurityPolicyName string
	// backendSecurityPolicyNamespace provides namespace of backend security policy.
	backendSecurityPolicyNamespace string
	// preRotationWindow specifies how long before expiry to rotate.
	preRotationWindow time.Duration
	// roleArn is the role ARN to assume.
	roleArn string
	// region is the AWS region for the credentials.
	region string
	// credentialSecretName is the name of the user-provided secret containing base credentials.
	credentialSecretName string
	// credentialSecretNamespace is the namespace of the user-provided secret.
	credentialSecretNamespace string
}

// NewAWSAssumeRoleRotator creates a new AWSAssumeRoleRotator with the specified configuration.
// It reads the base credentials from the referenced secret and initializes an STS client
// configured with those credentials.
func NewAWSAssumeRoleRotator(
	ctx context.Context,
	k8sClient client.Client,
	stsOps STSClient,
	kube kubernetes.Interface,
	logger logr.Logger,
	backendSecurityPolicyNamespace string,
	backendSecurityPolicyName string,
	preRotationWindow time.Duration,
	roleArn string,
	region string,
	credentialSecretName string,
	credentialSecretNamespace string,
) (*AWSAssumeRoleRotator, error) {
	if stsOps == nil {
		// Read the base credentials from the secret.
		secret, err := LookupSecret(ctx, k8sClient, credentialSecretNamespace, credentialSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup base credentials secret %s/%s: %w", credentialSecretNamespace, credentialSecretName, err)
		}

		credData, exists := secret.Data[AwsCredentialsKey]
		if !exists {
			return nil, fmt.Errorf("missing credentials key %s in secret %s/%s", AwsCredentialsKey, credentialSecretNamespace, credentialSecretName)
		}

		// Parse the credentials file to extract access key and secret key.
		accessKeyID, secretAccessKey, err := parseAWSCredentialsFile(string(credData))
		if err != nil {
			return nil, fmt.Errorf("failed to parse credentials from secret %s/%s: %w", credentialSecretNamespace, credentialSecretName, err)
		}

		// Create an AWS config using the base credentials from the secret.
		cfg, err := config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
			config.WithRetryMode(aws.RetryModeAdaptive),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config with base credentials: %w", err)
		}

		if proxyURL := os.Getenv("AI_GATEWAY_STS_PROXY_URL"); proxyURL != "" {
			cfg.HTTPClient = &http.Client{
				Transport: &http.Transport{
					Proxy: func(*http.Request) (*url.URL, error) {
						return url.Parse(proxyURL)
					},
				},
			}
		}
		stsOps = NewSTSClient(cfg)
	}

	return &AWSAssumeRoleRotator{
		client:                         k8sClient,
		kube:                           kube,
		logger:                         logger.WithName("aws-assume-role-rotator"),
		stsClient:                      stsOps,
		backendSecurityPolicyNamespace: backendSecurityPolicyNamespace,
		backendSecurityPolicyName:      backendSecurityPolicyName,
		preRotationWindow:              preRotationWindow,
		roleArn:                        roleArn,
		region:                         region,
		credentialSecretName:           credentialSecretName,
		credentialSecretNamespace:      credentialSecretNamespace,
	}, nil
}

// IsExpired checks if the preRotation time is before the current time.
func (r *AWSAssumeRoleRotator) IsExpired(preRotationExpirationTime time.Time) bool {
	return IsBufferedTimeExpired(0, preRotationExpirationTime)
}

// GetPreRotationTime gets the expiration time minus the preRotation interval or return zero value for time.
func (r *AWSAssumeRoleRotator) GetPreRotationTime(ctx context.Context) (time.Time, error) {
	secret, err := LookupSecret(ctx, r.client, r.backendSecurityPolicyNamespace, GetBSPSecretName(r.backendSecurityPolicyName))
	if err != nil {
		return time.Time{}, err
	}
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	if err != nil {
		return time.Time{}, err
	}
	preRotationTime := expirationTime.Add(-r.preRotationWindow)
	return preRotationTime, nil
}

// Rotate performs the STS AssumeRole call using the base credentials and stores the
// resulting temporary credentials in a generated Kubernetes secret.
//
// This implements [Rotator.Rotate].
func (r *AWSAssumeRoleRotator) Rotate(ctx context.Context) (time.Time, error) {
	bspNamespace := r.backendSecurityPolicyNamespace
	bspName := r.backendSecurityPolicyName
	secretName := GetBSPSecretName(bspName)

	r.logger.Info("rotating aws credentials via assume role", "namespace", bspNamespace, "name", bspName, "roleArn", r.roleArn)

	// Call STS AssumeRole.
	assumeRoleOutput, err := r.stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(r.roleArn),
		RoleSessionName: aws.String(fmt.Sprintf(awsSessionNameFormat, bspName)),
	})
	if err != nil {
		r.logger.Error(err, "failed to assume role", "role", r.roleArn)
		return time.Time{}, err
	}
	if assumeRoleOutput.Credentials == nil {
		return time.Time{}, fmt.Errorf("unexpected nil credentials from AssumeRole for %s in %s", bspName, bspNamespace)
	}

	r.logger.Info(fmt.Sprintf("AssumeRole credentials will expire at '%s'", assumeRoleOutput.Credentials.Expiration.String()),
		"namespace", bspNamespace, "name", bspName)

	// Build the credentials file content from the temporary credentials.
	const defaultProfile = "default"
	credsFile := awsCredentialsFile{awsCredentials{
		profile:         defaultProfile,
		accessKeyID:     aws.ToString(assumeRoleOutput.Credentials.AccessKeyId),
		secretAccessKey: aws.ToString(assumeRoleOutput.Credentials.SecretAccessKey),
		sessionToken:    aws.ToString(assumeRoleOutput.Credentials.SessionToken),
		region:          r.region,
	}}

	// Upsert the generated secret.
	secret, err := LookupSecret(ctx, r.client, bspNamespace, secretName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.logger.Info("creating a new aws credentials secret for assume role", "namespace", bspNamespace, "name", bspName)
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: bspNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: make(map[string][]byte),
			}
			updateExpirationSecretAnnotation(secret, *assumeRoleOutput.Credentials.Expiration)
			updateAWSCredentialsInSecret(secret, &credsFile)
			return *assumeRoleOutput.Credentials.Expiration, r.client.Create(ctx, secret)
		}
		r.logger.Error(err, "failed to lookup aws credentials secret", "namespace", bspNamespace, "name", bspName)
		return time.Time{}, err
	}

	r.logger.Info("updating existing aws credential secret for assume role", "namespace", bspNamespace, "name", bspName)
	updateExpirationSecretAnnotation(secret, *assumeRoleOutput.Credentials.Expiration)
	updateAWSCredentialsInSecret(secret, &credsFile)
	return *assumeRoleOutput.Credentials.Expiration, r.client.Update(ctx, secret)
}

// parseAWSCredentialsFile parses an AWS credentials file content and extracts the
// access key ID and secret access key from the default profile.
func parseAWSCredentialsFile(content string) (accessKeyID, secretAccessKey string, err error) {
	lines := splitLines(content)
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '[' || line[0] == '#' {
			continue
		}
		key, value, ok := parseKeyValue(line)
		if !ok {
			continue
		}
		switch key {
		case "aws_access_key_id":
			accessKeyID = value
		case "aws_secret_access_key":
			secretAccessKey = value
		}
	}
	if accessKeyID == "" || secretAccessKey == "" {
		return "", "", fmt.Errorf("credentials file missing aws_access_key_id or aws_secret_access_key")
	}
	return accessKeyID, secretAccessKey, nil
}

// splitLines splits a string into lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// trimSpace trims leading and trailing whitespace from a string.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// parseKeyValue parses a "key = value" or "key=value" line.
func parseKeyValue(line string) (key, value string, ok bool) {
	idx := -1
	for i := 0; i < len(line); i++ {
		if line[i] == '=' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", "", false
	}
	key = trimSpace(line[:idx])
	value = trimSpace(line[idx+1:])
	return key, value, true
}

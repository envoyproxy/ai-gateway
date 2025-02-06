/*
Package backendauthrotators provides credential rotation implementations.
This file contains common AWS functionality shared between different AWS credential
rotators. It provides:
1. AWS Client Interfaces and Implementations:
- STSOperations for AWS STS API operations
- Concrete implementations with proper AWS SDK integration
2. Credential File Management:
- Parsing and formatting of AWS credentials files
- Support for multiple credential profiles
- Handling of temporary credentials and session tokens
3. Common Configuration:
- Default AWS configuration with adaptive retry
- Standard timeouts and delays
- Session name formatting
*/
package backendauthrotators

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	corev1 "k8s.io/api/core/v1"
)

// Common constants for AWS operations
const (
	// credentialsKey is the key used to store AWS credentials in Kubernetes secrets
	credentialsKey = "credentials"
	// awsSessionNameFormat is the format string for AWS session names
	awsSessionNameFormat = "ai-gateway-%s"
)

// defaultAWSConfig returns an AWS config with adaptive retry mode enabled.
// This ensures better handling of transient API failures and rate limiting.
func defaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRetryMode(aws.RetryModeAdaptive),
	)
}

// STSOperations defines the interface for AWS STS operations required by the rotators.
// This interface encapsulates the STS API operations needed for OIDC token exchange
// and role assumption.
type STSOperations interface {
	// AssumeRoleWithWebIdentity exchanges a web identity token for temporary AWS credentials
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// STSClient implements the STSOperations interface using the AWS SDK v2.
// It provides a concrete implementation for STS operations using the official AWS SDK.
type STSClient struct {
	client *sts.Client
}

// NewSTSClient creates a new STSClient with the given AWS config.
// The client is configured with the provided AWS configuration, which should
// include appropriate credentials and region settings.
func NewSTSClient(cfg aws.Config) *STSClient {
	return &STSClient{
		client: sts.NewFromConfig(cfg),
	}
}

// AssumeRoleWithWebIdentity implements the STSOperations interface by exchanging
// a web identity token for temporary AWS credentials.
func (c *STSClient) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return c.client.AssumeRoleWithWebIdentity(ctx, params, optFns...)
}

// awsCredentials represents a single set of AWS credentials, including optional
// session token and region configuration. It maps to a single profile in an
// AWS credentials file.
type awsCredentials struct {
	// profile is the name of the credentials profile
	profile string
	// accessKeyID is the AWS access key ID
	accessKeyID string
	// secretAccessKey is the AWS secret access key
	secretAccessKey string
	// sessionToken is the optional AWS session token for temporary credentials
	sessionToken string
	// region is the optional AWS region for the profile
	region string
}

// awsCredentialsFile represents a complete AWS credentials file containing
// multiple credential profiles. It provides a structured way to manage
// multiple sets of AWS credentials.
type awsCredentialsFile struct {
	// profiles maps profile names to their respective credentials
	profiles map[string]*awsCredentials
}

// formatAWSCredentialsFile formats multiple AWS credential profiles into a credentials file.
// The output follows the standard AWS credentials file format and ensures:
// - Consistent ordering of profiles through sorting
// - Proper formatting of all credential components
// - Optional inclusion of session tokens and regions
// - Profile isolation with proper section markers
func formatAWSCredentialsFile(file *awsCredentialsFile) string {
	var builder strings.Builder

	// Sort profiles to ensure consistent output
	profileNames := make([]string, 0, len(file.profiles))
	for profileName := range file.profiles {
		profileNames = append(profileNames, profileName)
	}
	sort.Strings(profileNames)

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

// updateAWSCredentialsInSecret updates AWS credentials in a secret
func updateAWSCredentialsInSecret(secret *corev1.Secret, creds *awsCredentialsFile) {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[credentialsKey] = []byte(formatAWSCredentialsFile(creds))
}

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

/*
Package rotators provides credential rotation implementations.
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
package rotators

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

// Common constants for AWS operations.
const (
	// awsCredentialsKey is the key used to store AWS credentials in Kubernetes secrets.
	awsCredentialsKey = "credentials"
	// awsSessionNameFormat is the format string for AWS session names.
	awsSessionNameFormat = "ai-gateway-%s"
)

// defaultAWSConfig returns an AWS config with adaptive retry mode enabled.
// This ensures better handling of transient API failures and rate limiting.
func defaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRetryMode(aws.RetryModeAdaptive),
	)
}

// STSClient defines the interface for AWS STS operations required by the rotators.
// This interface encapsulates the STS API operations needed for OIDC token exchange
// and role assumption.
type STSClient interface {
	// AssumeRoleWithWebIdentity exchanges a web identity token for temporary AWS credentials.
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// stsClient implements the STSOperations interface using the AWS SDK v2.
// It provides a concrete implementation for STS operations using the official AWS SDK.
type stsClient struct {
	client *sts.Client
}

// NewSTSClient creates a new STSClient with the given AWS config.
// The client is configured with the provided AWS configuration, which should
// include appropriate credentials and region settings.
func NewSTSClient(cfg aws.Config) STSClient {
	return &stsClient{
		client: sts.NewFromConfig(cfg),
	}
}

// AssumeRoleWithWebIdentity implements the STSOperations interface by exchanging
// a web identity token for temporary AWS credentials.
//
// This implements [STSClient.AssumeRoleWithWebIdentity].
func (c *stsClient) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return c.client.AssumeRoleWithWebIdentity(ctx, params, optFns...)
}

// awsCredentials represents a single set of AWS credentials, including optional
// session token and region configuration. It maps to a single profile in an
// AWS credentials file.
type awsCredentials struct {
	// profile is the name of the credentials profile.
	profile string
	// accessKeyID is the AWS access key ID.
	accessKeyID string
	// secretAccessKey is the AWS secret access key.
	secretAccessKey string
	// sessionToken is the optional AWS session token for temporary credentials.
	sessionToken string
	// region is the optional AWS region for the profile.
	region string
}

// awsCredentialsFile represents a complete AWS credentials file containing
// multiple credential profiles. It provides a structured way to manage
// multiple sets of AWS credentials.
type awsCredentialsFile struct {
	// profiles maps profile names to their respective credentials.
	profiles map[string]*awsCredentials
}

// parseAWSCredentialsFile parses an AWS credentials file with multiple profiles.
// The file format follows the standard AWS credentials file format:
//
//	[profile-name]
//	aws_access_key_id = AKIAXXXXXXXXXXXXXXXX
//	aws_secret_access_key = xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
//	aws_session_token = xxxxxxxx (optional)
//	region = xx-xxxx-x (optional)
//
// Returns a structured representation of the credentials file.
func parseAWSCredentialsFile(data string) *awsCredentialsFile {
	file := &awsCredentialsFile{
		profiles: make(map[string]*awsCredentials),
	}

	var currentCreds *awsCredentials

	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			profileName := strings.TrimPrefix(strings.TrimSuffix(line, "]"), "[")
			currentCreds = &awsCredentials{profile: profileName}
			file.profiles[profileName] = currentCreds
			continue
		}

		if currentCreds == nil {
			continue
		}

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

	return file
}

// formatAWSCredentialsFile formats multiple AWS credential profiles into a credentials file.
// The output follows the standard AWS credentials file format and ensures:
// - Consistent ordering of profiles through sorting
// - Proper formatting of all credential components
// - Optional inclusion of session tokens and regions
// - Profile isolation with proper section markers
func formatAWSCredentialsFile(file *awsCredentialsFile) string {
	var builder strings.Builder

	// Sort profiles to ensure consistent output.
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
		builder.WriteString(fmt.Sprintf("region = %s\n", creds.region))
	}
	return builder.String()
}

// updateAWSCredentialsInSecret updates AWS credentials in a secret.
func updateAWSCredentialsInSecret(secret *corev1.Secret, creds *awsCredentialsFile) {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[awsCredentialsKey] = []byte(formatAWSCredentialsFile(creds))
}

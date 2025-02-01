package token_rotators

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	defaultKeyDeletionDelay    = 60 * time.Second
	defaultMinPropagationDelay = 30 * time.Second
	defaultProfile             = "default"
	credentialsKey             = "credentials"
	awsSessionNameFormat       = "ai-gateway-%s"
)

// getDefaultAWSConfig returns an AWS config with adaptive retry mode enabled
func getDefaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRetryMode(aws.RetryModeAdaptive),
	)
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

// IAMClient implements the IAMOperations interface
type IAMClient struct {
	client *iam.Client
}

// NewIAMClient creates a new IAMClient with the given AWS config
func NewIAMClient(cfg aws.Config) *IAMClient {
	return &IAMClient{
		client: iam.NewFromConfig(cfg),
	}
}

// CreateAccessKey implements the IAMOperations interface
func (c *IAMClient) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	return c.client.CreateAccessKey(ctx, params, optFns...)
}

// DeleteAccessKey implements the IAMOperations interface
func (c *IAMClient) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	return c.client.DeleteAccessKey(ctx, params, optFns...)
}

// STSClient implements the STSOperations interface
type STSClient struct {
	client *sts.Client
}

// NewSTSClient creates a new STSClient with the given AWS config
func NewSTSClient(cfg aws.Config) *STSClient {
	return &STSClient{
		client: sts.NewFromConfig(cfg),
	}
}

// AssumeRoleWithWebIdentity implements the STSOperations interface
func (c *STSClient) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return c.client.AssumeRoleWithWebIdentity(ctx, params, optFns...)
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

// parseAWSCredentialsFile parses an AWS credentials file with multiple profiles
func parseAWSCredentialsFile(data string) *awsCredentialsFile {
	file := &awsCredentialsFile{
		profiles: make(map[string]*awsCredentials),
	}

	var currentCreds *awsCredentials

	for _, line := range strings.Split(data, "\n") {
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

// formatAWSCredentialsFile formats multiple AWS credential profiles into a credentials file
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

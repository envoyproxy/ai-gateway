package token_rotators

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	defaultKeyDeletionDelay = 30 * time.Second
	defaultProfile          = "default"
	credentialsKey          = "credentials"
	awsSessionNameFormat    = "ai-gateway-%s"
)

// IAMOperations interface for AWS IAM operations
type IAMOperations interface {
	CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

// STSOperations interface for AWS STS operations
type STSOperations interface {
	AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// IAMClient wraps the AWS IAM client to implement the IAMOperations interface
type IAMClient struct {
	*iam.Client
}

// STSClient wraps the AWS STS client to implement the STSOperations interface
type STSClient struct {
	*sts.Client
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

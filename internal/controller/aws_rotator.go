package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// AWSCredentialsRotator implements the Rotator interface for AWS IAM credentials
type AWSCredentialsRotator struct {
	k8sClient        client.Client
	k8sClientset     kubernetes.Interface
	logger           logr.Logger
	iamOps           IAMOperations
	keyDeletionDelay time.Duration
}

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func NewAWSCredentialsRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
) *AWSCredentialsRotator {
	return &AWSCredentialsRotator{
		k8sClient:        k8sClient,
		k8sClientset:     k8sClientset,
		logger:           logger,
		keyDeletionDelay: defaultKeyDeletionDelay,
	}
}

// GetType implements the Rotator interface
func (r *AWSCredentialsRotator) GetType() RotationType {
	return RotationTypeAWSCredentials
}

// Rotate implements the Rotator interface
func (r *AWSCredentialsRotator) Rotate(ctx context.Context, event RotationEvent) error {
	// Get the secret containing AWS credentials
	var secret corev1.Secret
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Parse existing credentials
	existingCreds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	if existingCreds == nil || len(existingCreds.profiles) == 0 {
		return fmt.Errorf("no valid AWS credentials found in secret")
	}

	// Create new access key
	createKeyOutput, err := r.iamOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return fmt.Errorf("failed to create new access key: %w", err)
	}

	// Update credentials in all profiles
	for _, profile := range existingCreds.profiles {
		profile.accessKeyID = *createKeyOutput.AccessKey.AccessKeyId
		profile.secretAccessKey = *createKeyOutput.AccessKey.SecretAccessKey
	}

	// Format updated credentials
	updatedCredsData := formatAWSCredentialsFile(existingCreds)

	// Update the secret with new credentials
	secret.Data[credentialsKey] = []byte(updatedCredsData)
	if err := r.k8sClient.Update(ctx, &secret); err != nil {
		return fmt.Errorf("failed to update secret with new credentials: %w", err)
	}

	// Schedule deletion of old access key after delay
	if oldKeyID := event.Metadata["old_access_key_id"]; oldKeyID != "" {
		// Create a context that will time out after the deletion delay
		deleteCtx, cancel := context.WithTimeout(ctx, r.keyDeletionDelay+5*time.Second)

		go func() {
			defer cancel()

			// Wait for either the deletion delay or context cancellation
			select {
			case <-time.After(r.keyDeletionDelay):
				// Attempt to delete the key
				_, err := r.iamOps.DeleteAccessKey(deleteCtx, &iam.DeleteAccessKeyInput{
					AccessKeyId: aws.String(oldKeyID),
				})
				if err != nil {
					r.logger.Error(err, "failed to delete old access key", "accessKeyId", oldKeyID)
				}
			case <-ctx.Done():
				// Parent context was cancelled, attempt immediate deletion
				_, err := r.iamOps.DeleteAccessKey(deleteCtx, &iam.DeleteAccessKeyInput{
					AccessKeyId: aws.String(oldKeyID),
				})
				if err != nil && err != context.Canceled {
					r.logger.Error(err, "failed to delete old access key during shutdown", "accessKeyId", oldKeyID)
				}
			case <-deleteCtx.Done():
				// Timeout or cancellation
				return
			}
		}()
	}

	return nil
}

// AWSOIDCRotator implements the Rotator interface for AWS OIDC token exchange
type AWSOIDCRotator struct {
	k8sClient    client.Client
	k8sClientset kubernetes.Interface
	logger       logr.Logger
	stsOps       STSOperations
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator
func NewAWSOIDCRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
) *AWSOIDCRotator {
	return &AWSOIDCRotator{
		k8sClient:    k8sClient,
		k8sClientset: k8sClientset,
		logger:       logger,
	}
}

// GetType implements the Rotator interface
func (r *AWSOIDCRotator) GetType() RotationType {
	return RotationTypeAWSOIDC
}

// Rotate implements the Rotator interface
func (r *AWSOIDCRotator) Rotate(ctx context.Context, event RotationEvent) error {
	roleARN := event.Metadata["role_arn"]
	if roleARN == "" {
		return fmt.Errorf("role_arn is required in metadata")
	}

	idToken := event.Metadata["id_token"]
	if idToken == "" {
		return fmt.Errorf("id_token is required in metadata")
	}

	// Assume role with web identity
	resp, err := r.stsOps.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleARN),
		WebIdentityToken: aws.String(idToken),
		RoleSessionName:  aws.String(fmt.Sprintf(awsSessionNameFormat, event.Name)),
	})
	if err != nil {
		return fmt.Errorf("failed to assume role with web identity: %w", err)
	}

	// Get the secret to update
	var secret corev1.Secret
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, &secret); err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Create credentials file with temporary credentials
	creds := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			defaultProfile: {
				profile:         defaultProfile,
				accessKeyID:     *resp.Credentials.AccessKeyId,
				secretAccessKey: *resp.Credentials.SecretAccessKey,
				sessionToken:    *resp.Credentials.SessionToken,
			},
		},
	}

	// Update the secret
	secret.Data[credentialsKey] = []byte(formatAWSCredentialsFile(creds))
	if err := r.k8sClient.Update(ctx, &secret); err != nil {
		return fmt.Errorf("failed to update secret with new credentials: %w", err)
	}

	return nil
}

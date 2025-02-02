/*
Package backendauthrotators provides credential rotation implementations.

# AWS Credentials Rotation

The AWS credentials rotator handles the rotation of IAM access keys through a multi-step process:

 1. Validation of rotation events and existing credentials
 2. Creation of new access keys
 3. Update of Kubernetes secrets with new credentials
 4. Scheduled deletion of old access keys with proper delays

# Usage

To use the AWS credentials rotator:

	rotator, err := NewAWSCredentialsRotator(k8sClient, k8sClientset, logger)
	if err != nil {
	    // Handle error
	}

	// Initialize new credentials
	err = rotator.Initialize(ctx, RotationEvent{
	    Namespace: "default",
	    Name:      "aws-creds",
	    Type:      RotationTypeAWSCredentials,
	})

	// Rotate existing credentials
	err = rotator.Rotate(ctx, RotationEvent{
	    Namespace: "default",
	    Name:      "aws-creds",
	    Type:      RotationTypeAWSCredentials,
	    Metadata: map[string]string{
	        "old_access_key_id": "AKIA...",
	    },
	})

# Key Deletion Process

The key deletion process includes two important delays:

  - MinPropagationDelay: Minimum time to wait for new credentials to propagate
  - KeyDeletionDelay: Total time to wait before deleting old credentials

These delays ensure smooth credential rotation without service interruption.
*/
package backendauthrotators

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// -----------------------------------------------------------------------------
// Types and Constants
// -----------------------------------------------------------------------------

// AWSCredentialsRotator implements the Rotator interface for AWS IAM credentials.
// It manages the lifecycle of AWS IAM access keys, including creation, rotation,
// and deletion with appropriate safety delays.
//
// The rotator ensures zero-downtime credential rotation by:
// 1. Creating new credentials before invalidating old ones
// 2. Allowing for credential propagation delay
// 3. Safely cleaning up old credentials
//
// The rotation process is configurable through KeyDeletionDelay and MinPropagationDelay
// to accommodate different environments and requirements.
type AWSCredentialsRotator struct {
	// client is used for Kubernetes API operations
	client client.Client
	// kube provides additional Kubernetes API capabilities
	kube kubernetes.Interface
	// logger is used for structured logging
	logger logr.Logger
	// IAMOps provides AWS IAM operations interface
	IAMOps IAMOperations
	// KeyDeletionDelay is the total time to wait before deleting old credentials
	KeyDeletionDelay time.Duration
	// MinPropagationDelay is the minimum time to wait for new credentials to propagate
	MinPropagationDelay time.Duration
}

// -----------------------------------------------------------------------------
// Constructor and Interface Implementation
// -----------------------------------------------------------------------------

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func NewAWSCredentialsRotator(
	client client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
) (*AWSCredentialsRotator, error) {
	cfg, err := getDefaultAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	iamClient := NewIAMClient(cfg)

	return &AWSCredentialsRotator{
		client:              client,
		kube:                kube,
		logger:              logger,
		IAMOps:              iamClient,
		KeyDeletionDelay:    defaultKeyDeletionDelay,
		MinPropagationDelay: defaultMinPropagationDelay,
	}, nil
}

// Type returns the type of rotation this rotator handles
func (r *AWSCredentialsRotator) Type() RotationType {
	return RotationTypeAWSCredentials
}

// -----------------------------------------------------------------------------
// Main Interface Methods - Initialize and Rotate
// -----------------------------------------------------------------------------

// Initialize implements the initial token retrieval for AWS credentials
func (r *AWSCredentialsRotator) Initialize(ctx context.Context, event RotationEvent) error {
	r.logger.Info("initializing AWS credentials",
		"namespace", event.Namespace,
		"name", event.Name)

	// Create new secret struct
	secret := newSecret(event.Namespace, event.Name)

	// Create new access key
	result, err := r.IAMOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return fmt.Errorf("failed to create access key: %w", err)
	}

	// Create new credentials file
	credsFile := &awsCredentialsFile{
		profiles: make(map[string]*awsCredentials),
	}

	// Update credentials
	profile := defaultProfile
	if p, ok := event.Metadata["profile"]; ok {
		profile = p
	}

	credsFile.profiles[profile] = &awsCredentials{
		profile:         profile,
		accessKeyID:     aws.ToString(result.AccessKey.AccessKeyId),
		secretAccessKey: aws.ToString(result.AccessKey.SecretAccessKey),
		region:          event.Metadata["region"],
	}

	// Update secret with credentials
	updateAWSCredentialsInSecret(secret, credsFile)
	return updateSecret(ctx, r.client, secret)
}

// Rotate implements the Rotator interface for AWS IAM credentials
func (r *AWSCredentialsRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if err := validateRotationEvent(event); err != nil {
		return err
	}

	// Get existing secret
	secret, err := lookupSecret(ctx, r.client, event.Namespace, event.Name)
	if err != nil {
		return err
	}

	existingCreds, err := validateAWSSecret(secret)
	if err != nil {
		return err
	}

	createKeyOutput, err := r.createNewAccessKey(ctx)
	if err != nil {
		return err
	}

	// Update credentials in all profiles
	for _, profile := range existingCreds.profiles {
		profile.accessKeyID = aws.ToString(createKeyOutput.AccessKey.AccessKeyId)
		profile.secretAccessKey = aws.ToString(createKeyOutput.AccessKey.SecretAccessKey)
	}

	updateAWSCredentialsInSecret(secret, existingCreds)
	if err := updateSecret(ctx, r.client, secret); err != nil {
		return err
	}

	return r.scheduleOldKeyDeletion(ctx, event)
}

// -----------------------------------------------------------------------------
// AWS Operations
// -----------------------------------------------------------------------------

// createNewAccessKey creates a new AWS access key
func (r *AWSCredentialsRotator) createNewAccessKey(ctx context.Context) (*iam.CreateAccessKeyOutput, error) {
	createKeyOutput, err := r.IAMOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to create new access key: %w", err)
	}
	return createKeyOutput, nil
}

// -----------------------------------------------------------------------------
// Key Deletion Management
// -----------------------------------------------------------------------------

// scheduleOldKeyDeletion schedules the deletion of the old access key
func (r *AWSCredentialsRotator) scheduleOldKeyDeletion(ctx context.Context, event RotationEvent) error {
	oldKeyID := event.Metadata["old_access_key_id"]
	if oldKeyID == "" {
		return nil // No old key to delete
	}

	// Create a context that will time out after the deletion delay
	deleteCtx, cancel := context.WithTimeout(ctx, r.KeyDeletionDelay)

	go func() {
		defer cancel()
		r.handleKeyDeletion(deleteCtx, oldKeyID)
	}()

	return nil
}

// handleKeyDeletion manages the key deletion process with proper delays
func (r *AWSCredentialsRotator) handleKeyDeletion(ctx context.Context, oldKeyID string) {
	// First, wait for the minimum propagation delay
	select {
	case <-ctx.Done():
		// Context was cancelled, but still ensure minimum propagation delay
		time.Sleep(r.MinPropagationDelay)
		r.deleteAccessKey(ctx, oldKeyID, true)
		return
	case <-time.After(r.MinPropagationDelay):
		// Minimum propagation delay satisfied, continue with normal flow
	}

	// Now wait for the remaining deletion delay
	remainingDelay := r.KeyDeletionDelay - r.MinPropagationDelay
	if remainingDelay > 0 {
		select {
		case <-ctx.Done():
			// Context cancelled after propagation delay, delete immediately
			r.deleteAccessKey(ctx, oldKeyID, true)
			return
		case <-time.After(remainingDelay):
			// Normal delay-based deletion
		}
	}

	// Proceed with deletion after all delays are satisfied
	r.deleteAccessKey(ctx, oldKeyID, false)
}

// deleteAccessKey performs the actual deletion of the AWS access key
func (r *AWSCredentialsRotator) deleteAccessKey(ctx context.Context, accessKeyID string, isCancelled bool) {
	_, err := r.IAMOps.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		AccessKeyId: aws.String(accessKeyID),
	})
	if err != nil {
		if isCancelled {
			r.logger.Error(err, "failed to delete old access key after context cancellation",
				"accessKeyId", accessKeyID)
		} else {
			r.logger.Error(err, "failed to delete old access key",
				"accessKeyId", accessKeyID)
		}
	}
}

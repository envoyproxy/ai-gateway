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
	"sync"
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
	iamMutex            sync.Mutex // protects IAMOps during concurrent operations
}

// -----------------------------------------------------------------------------
// Constructor and Interface Implementation
// -----------------------------------------------------------------------------

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func NewAWSCredentialsRotator(config RotatorConfig) (*AWSCredentialsRotator, error) {
	rotator := &AWSCredentialsRotator{
		client:              config.Client,
		kube:                config.KubeClient,
		logger:              config.Logger,
		KeyDeletionDelay:    defaultKeyDeletionDelay,
		MinPropagationDelay: defaultMinPropagationDelay,
	}

	// Use provided IAM operations if available
	if config.IAMOperations != nil {
		rotator.IAMOps = config.IAMOperations
		return rotator, nil
	}

	// Use provided AWS config or load default
	var awsCfg aws.Config
	if config.AWSConfig != nil {
		awsCfg = *config.AWSConfig
	} else {
		var err error
		awsCfg, err = defaultAWSConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}
	}

	rotator.IAMOps = NewIAMClient(awsCfg)
	return rotator, nil
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

	// Get existing credentials from secret
	existingSecret, err := lookupSecret(ctx, r.client, event.Namespace, event.Name)
	if err != nil {
		return fmt.Errorf("failed to lookup secret: %w", err)
	}

	existingCreds, err := validateAWSSecret(existingSecret)
	if err != nil {
		return fmt.Errorf("failed to validate AWS secret: %w", err)
	}

	// Determine which profile to use
	profile, err := profileFromMetadata(event.Metadata, existingCreds)
	if err != nil {
		return fmt.Errorf("failed to determine AWS profile: %w", err)
	}

	// Configure AWS client if needed
	if err := r.configureIAMClient(ctx, existingCreds.profiles[profile]); err != nil {
		return fmt.Errorf("failed to configure IAM client: %w", err)
	}

	// Create new access key
	result, err := r.createNewAccessKey(ctx)
	if err != nil {
		return fmt.Errorf("failed to create access key: %w", err)
	}

	// Create new credentials file
	credsFile := &awsCredentialsFile{
		profiles: make(map[string]*awsCredentials),
	}

	// Update credentials using the same profile
	credsFile.profiles[profile] = &awsCredentials{
		profile:         profile,
		accessKeyID:     aws.ToString(result.AccessKey.AccessKeyId),
		secretAccessKey: aws.ToString(result.AccessKey.SecretAccessKey),
		region:          event.Metadata["region"],
	}

	// Update the existing secret with new credentials
	updateAWSCredentialsInSecret(existingSecret, credsFile)
	return updateSecret(ctx, r.client, existingSecret)
}

// Rotate implements the Rotator interface for AWS IAM credentials
func (r *AWSCredentialsRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if err := validateRotationEvent(event); err != nil {
		return err
	}

	r.logger.Info("starting AWS credentials rotation",
		"namespace", event.Namespace,
		"name", event.Name,
		"metadata", event.Metadata)

	// Get existing secret
	secret, err := lookupSecret(ctx, r.client, event.Namespace, event.Name)
	if err != nil {
		r.logger.Error(err, "failed to lookup secret",
			"namespace", event.Namespace,
			"name", event.Name)
		return err
	}

	existingCreds, err := validateAWSSecret(secret)
	if err != nil {
		r.logger.Error(err, "failed to validate AWS secret",
			"namespace", event.Namespace,
			"name", event.Name)
		return err
	}

	// Determine which profile to use
	profile, err := profileFromMetadata(event.Metadata, existingCreds)
	if err != nil {
		r.logger.Error(err, "failed to determine AWS profile",
			"namespace", event.Namespace,
			"name", event.Name,
			"metadata", event.Metadata)
		return fmt.Errorf("failed to determine AWS profile: %w", err)
	}

	// Configure AWS client if needed
	if err := r.configureIAMClient(ctx, existingCreds.profiles[profile]); err != nil {
		r.logger.Error(err, "failed to configure IAM client",
			"namespace", event.Namespace,
			"name", event.Name,
			"profile", profile)
		return fmt.Errorf("failed to configure IAM client: %w", err)
	}

	r.logger.Info("creating new access key",
		"namespace", event.Namespace,
		"name", event.Name,
		"profile", profile)

	createKeyOutput, err := r.createNewAccessKey(ctx)
	if err != nil {
		r.logger.Error(err, "failed to create new access key",
			"namespace", event.Namespace,
			"name", event.Name,
			"profile", profile)
		return fmt.Errorf("failed to create new access key: %w", err)
	}

	r.logger.Info("successfully created new access key",
		"namespace", event.Namespace,
		"name", event.Name,
		"profile", profile,
		"new_key_id", aws.ToString(createKeyOutput.AccessKey.AccessKeyId))

	// Store the old key ID before updating the secret
	oldKeyID := existingCreds.profiles[profile].accessKeyID

	// Update only the specified profile's credentials
	existingCreds.profiles[profile].accessKeyID = aws.ToString(createKeyOutput.AccessKey.AccessKeyId)
	existingCreds.profiles[profile].secretAccessKey = aws.ToString(createKeyOutput.AccessKey.SecretAccessKey)

	updateAWSCredentialsInSecret(secret, existingCreds)
	if err := updateSecret(ctx, r.client, secret); err != nil {
		r.logger.Error(err, "failed to update secret with new credentials",
			"namespace", event.Namespace,
			"name", event.Name,
			"profile", profile)
		return err
	}

	// Add the old key ID to metadata for deletion
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["old_access_key_id"] = oldKeyID

	return r.scheduleOldKeyDeletion(ctx, event)
}

// configureIAMClient configures the IAM client if it hasn't been set
func (r *AWSCredentialsRotator) configureIAMClient(ctx context.Context, creds *awsCredentials) error {
	if r.IAMOps != nil {
		return nil
	}

	cfg, err := awsConfigFromCredentials(ctx, creds)
	if err != nil {
		return fmt.Errorf("failed to configure AWS with credentials: %w", err)
	}

	r.iamMutex.Lock()
	defer r.iamMutex.Unlock()

	// Double-check after acquiring lock
	if r.IAMOps == nil {
		r.IAMOps = NewIAMClient(cfg)
	}

	return nil
}

// -----------------------------------------------------------------------------
// AWS Operations
// -----------------------------------------------------------------------------

// createNewAccessKey creates a new AWS IAM access key
func (r *AWSCredentialsRotator) createNewAccessKey(ctx context.Context) (*iam.CreateAccessKeyOutput, error) {
	if r.IAMOps == nil {
		return nil, fmt.Errorf("IAM operations not initialized")
	}

	r.iamMutex.Lock()
	defer r.iamMutex.Unlock()

	result, err := r.IAMOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to create new access key: %w", err)
	}

	r.logger.Info("created new access key", "keyID", aws.ToString(result.AccessKey.AccessKeyId))
	return result, nil
}

// deleteAccessKey deletes an AWS IAM access key
func (r *AWSCredentialsRotator) deleteAccessKey(ctx context.Context, accessKeyID string) error {
	if r.IAMOps == nil {
		return fmt.Errorf("IAM operations not initialized")
	}

	r.iamMutex.Lock()
	defer r.iamMutex.Unlock()

	_, err := r.IAMOps.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		AccessKeyId: aws.String(accessKeyID),
	})
	if err != nil {
		r.logger.Error(err, "failed to delete access key", "keyID", accessKeyID)
		return fmt.Errorf("failed to delete access key: %w", err)
	}

	r.logger.Info("deleted access key", "keyID", accessKeyID)
	return nil
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
		if err := r.deleteAccessKey(ctx, oldKeyID); err != nil {
			r.logger.Error(err, "failed to delete access key after context cancellation", "keyID", oldKeyID)
		}
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
			if err := r.deleteAccessKey(ctx, oldKeyID); err != nil {
				r.logger.Error(err, "failed to delete access key after context cancellation during remaining delay", "keyID", oldKeyID)
			}
			return
		case <-time.After(remainingDelay):
			// Normal delay-based deletion
		}
	}

	// Proceed with deletion after all delays are satisfied
	if err := r.deleteAccessKey(ctx, oldKeyID); err != nil {
		r.logger.Error(err, "failed to delete access key after all delays", "keyID", oldKeyID)
	}
}

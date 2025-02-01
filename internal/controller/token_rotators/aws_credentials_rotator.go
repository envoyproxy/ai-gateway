/*
Package token_rotators provides credential rotation implementations.

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
package token_rotators

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// GetType implements the Rotator interface
func (r *AWSCredentialsRotator) GetType() RotationType {
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

	// Get the secret
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		// Create new secret if it doesn't exist
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      event.Name,
				Namespace: event.Namespace,
			},
			Type: corev1.SecretTypeOpaque,
			Data: make(map[string][]byte),
		}
	}

	// Create new access key
	result, err := r.IAMOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
	if err != nil {
		return fmt.Errorf("failed to create access key: %w", err)
	}

	// Parse existing credentials file or create new one
	var credsFile *awsCredentialsFile
	if data, ok := secret.Data[credentialsKey]; ok {
		credsFile = parseAWSCredentialsFile(string(data))
	} else {
		credsFile = &awsCredentialsFile{
			profiles: make(map[string]*awsCredentials),
		}
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

	// Update secret
	secret.Data[credentialsKey] = []byte(formatAWSCredentialsFile(credsFile))

	// Create or update the secret
	if secret.ResourceVersion == "" {
		if err := r.client.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	} else {
		if err := r.client.Update(ctx, secret); err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
	}

	return nil
}

// Rotate implements the Rotator interface for AWS IAM credentials
func (r *AWSCredentialsRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if err := r.validateRotationEvent(event); err != nil {
		return err
	}

	secret, existingCreds, err := r.getAndValidateSecret(ctx, event)
	if err != nil {
		return err
	}
	if secret == nil {
		return nil // Secret not found, nothing to rotate
	}

	createKeyOutput, err := r.createNewAccessKey(ctx)
	if err != nil {
		return err
	}

	if err := r.updateCredentialsInSecret(ctx, secret, existingCreds, createKeyOutput); err != nil {
		return err
	}

	return r.scheduleOldKeyDeletion(ctx, event)
}

// -----------------------------------------------------------------------------
// Validation and Secret Management
// -----------------------------------------------------------------------------

// validateRotationEvent validates the rotation event parameters
func (r *AWSCredentialsRotator) validateRotationEvent(event RotationEvent) error {
	if event.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if event.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// getAndValidateSecret retrieves and validates the AWS credentials secret
func (r *AWSCredentialsRotator) getAndValidateSecret(ctx context.Context, event RotationEvent) (*corev1.Secret, *awsCredentialsFile, error) {
	var secret corev1.Secret
	if err := r.client.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("secret not found: %s/%s", event.Namespace, event.Name)
		}
		return nil, nil, fmt.Errorf("failed to get secret: %w", err)
	}

	existingCreds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	if existingCreds == nil || len(existingCreds.profiles) == 0 {
		return nil, nil, fmt.Errorf("no valid AWS credentials found in secret")
	}

	return &secret, existingCreds, nil
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

// updateCredentialsInSecret updates the secret with new AWS credentials
func (r *AWSCredentialsRotator) updateCredentialsInSecret(ctx context.Context, secret *corev1.Secret, existingCreds *awsCredentialsFile, createKeyOutput *iam.CreateAccessKeyOutput) error {
	// Update credentials in all profiles
	for _, profile := range existingCreds.profiles {
		profile.accessKeyID = *createKeyOutput.AccessKey.AccessKeyId
		profile.secretAccessKey = *createKeyOutput.AccessKey.SecretAccessKey
	}

	// Format updated credentials
	updatedCredsData := formatAWSCredentialsFile(existingCreds)

	// Update the secret with new credentials
	secret.Data[credentialsKey] = []byte(updatedCredsData)
	if err := r.client.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to update secret with new credentials: %w", err)
	}

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
	deleteCtx, cancel := context.WithTimeout(ctx, r.KeyDeletionDelay+5*time.Second)

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

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

// AWSCredentialsRotator implements the Rotator interface for AWS IAM credentials
type AWSCredentialsRotator struct {
	k8sClient           client.Client
	k8sClientset        kubernetes.Interface
	logger              logr.Logger
	IAMOps              IAMOperations
	KeyDeletionDelay    time.Duration
	MinPropagationDelay time.Duration
}

// NewAWSCredentialsRotator creates a new AWS credentials rotator
func NewAWSCredentialsRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
) (*AWSCredentialsRotator, error) {
	cfg, err := getDefaultAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	iamClient := NewIAMClient(cfg)

	return &AWSCredentialsRotator{
		k8sClient:           k8sClient,
		k8sClientset:        k8sClientset,
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
	createKeyOutput, err := r.IAMOps.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{})
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
		deleteCtx, cancel := context.WithTimeout(ctx, r.KeyDeletionDelay+5*time.Second)

		go func() {
			defer cancel()

			// First, wait for the minimum propagation delay to ensure new credentials are active
			select {
			case <-ctx.Done():
				// Context was cancelled, but still ensure minimum propagation delay
				time.Sleep(r.MinPropagationDelay)
				// Then delete immediately without waiting for full deletion delay
				_, err := r.IAMOps.DeleteAccessKey(deleteCtx, &iam.DeleteAccessKeyInput{
					AccessKeyId: aws.String(oldKeyID),
				})
				if err != nil {
					r.logger.Error(err, "failed to delete old access key after context cancellation",
						"accessKeyId", oldKeyID)
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
					_, err := r.IAMOps.DeleteAccessKey(deleteCtx, &iam.DeleteAccessKeyInput{
						AccessKeyId: aws.String(oldKeyID),
					})
					if err != nil {
						r.logger.Error(err, "failed to delete old access key after context cancellation",
							"accessKeyId", oldKeyID)
					}
					return
				case <-time.After(remainingDelay):
					// Normal delay-based deletion
				}
			}

			// Proceed with deletion after all delays are satisfied
			_, err := r.IAMOps.DeleteAccessKey(deleteCtx, &iam.DeleteAccessKeyInput{
				AccessKeyId: aws.String(oldKeyID),
			})
			if err != nil {
				r.logger.Error(err, "failed to delete old access key",
					"accessKeyId", oldKeyID)
			}
		}()
	}

	return nil
}

// Initialize implements the initial token retrieval for AWS credentials
func (r *AWSCredentialsRotator) Initialize(ctx context.Context, event RotationEvent) error {
	r.logger.Info("initializing AWS credentials",
		"namespace", event.Namespace,
		"name", event.Name)

	// Get the secret
	secret := &corev1.Secret{}
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
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
		if err := r.k8sClient.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	} else {
		if err := r.k8sClient.Update(ctx, secret); err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
	}

	return nil
}

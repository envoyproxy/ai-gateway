package token_rotators

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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

// AWSOIDCRotator implements the Rotator interface for AWS OIDC token exchange.
// It manages the lifecycle of temporary AWS credentials obtained through OIDC token
// exchange with AWS STS. The rotator automatically schedules credential refresh
// before expiration to ensure continuous access.
//
// Key features:
// - Automatic credential refresh before expiration
// - Support for role assumption with web identity
// - Integration with Kubernetes secrets for credential storage
// - Channel-based rotation scheduling
type AWSOIDCRotator struct {
	// k8sClient is used for Kubernetes API operations
	k8sClient client.Client
	// k8sClientset provides additional Kubernetes API capabilities
	k8sClientset kubernetes.Interface
	// logger is used for structured logging
	logger logr.Logger
	// stsOps provides AWS STS operations interface
	stsOps STSOperations
	// rotationChan receives rotation events to process
	rotationChan <-chan RotationEvent
	// scheduleChan sends events for future rotations
	scheduleChan chan<- RotationEvent
}

// -----------------------------------------------------------------------------
// Constructor and Interface Implementation
// -----------------------------------------------------------------------------

// NewAWSOIDCRotator creates a new AWS OIDC rotator with the specified configuration.
// It initializes the AWS STS client and sets up the rotation channels.
func NewAWSOIDCRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
	rotationChan <-chan RotationEvent,
	scheduleChan chan<- RotationEvent,
) (*AWSOIDCRotator, error) {
	cfg, err := getDefaultAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	stsClient := NewSTSClient(cfg)

	return &AWSOIDCRotator{
		k8sClient:    k8sClient,
		k8sClientset: k8sClientset,
		logger:       logger,
		stsOps:       stsClient,
		rotationChan: rotationChan,
		scheduleChan: scheduleChan,
	}, nil
}

// GetType implements the Rotator interface
func (r *AWSOIDCRotator) GetType() RotationType {
	return RotationTypeAWSOIDC
}

// SetSTSOperations sets the STS operations implementation - primarily used for testing
func (r *AWSOIDCRotator) SetSTSOperations(ops STSOperations) {
	r.stsOps = ops
}

// -----------------------------------------------------------------------------
// Event Processing
// -----------------------------------------------------------------------------

// Start begins processing rotation events from the rotation channel.
// It runs until the context is cancelled, processing only events
// that match this rotator's type.
func (r *AWSOIDCRotator) Start(ctx context.Context) error {
	for {
		select {
		case event := <-r.rotationChan:
			// Only process events for this rotator type
			if event.Type != RotationTypeAWSOIDC {
				continue
			}

			if err := r.Rotate(ctx, event); err != nil {
				if err != context.Canceled {
					r.logger.Error(err, "failed to rotate credentials",
						"namespace", event.Namespace,
						"name", event.Name)
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// -----------------------------------------------------------------------------
// Main Interface Methods - Initialize and Rotate
// -----------------------------------------------------------------------------

// updateSecret updates (or optionally creates) a secret with AWS credentials
func (r *AWSOIDCRotator) updateSecret(ctx context.Context, event RotationEvent, resp *sts.AssumeRoleWithWebIdentityOutput, allowCreate bool) error {
	secret := &corev1.Secret{}
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		if !allowCreate {
			return fmt.Errorf("secret does not exist and creation is not allowed: %w", err)
		}
		// Create new secret if it doesn't exist and creation is allowed
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      event.Name,
				Namespace: event.Namespace,
			},
			Type: corev1.SecretTypeOpaque,
			Data: make(map[string][]byte),
		}
	}

	// Update secret with credentials
	secret.Data[credentialsKey] = r.createCredentialsFileBytes(resp, event.Metadata["region"])

	// Create or update the secret
	if secret.ResourceVersion == "" {
		if err := r.k8sClient.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	} else {
		if err := r.k8sClient.Update(ctx, secret); err != nil {
			return fmt.Errorf("failed to update secret with new credentials: %w", err)
		}
	}

	return nil
}

// Initialize implements the initial token retrieval for AWS OIDC tokens.
// It performs the following steps:
// 1. Retrieves the OIDC token from the specified secret
// 2. Exchanges the token for temporary AWS credentials
// 3. Creates or updates a secret with the credentials
func (r *AWSOIDCRotator) Initialize(ctx context.Context, event RotationEvent) error {
	r.logger.Info("initializing AWS OIDC token",
		"namespace", event.Namespace,
		"name", event.Name)

	// Get the OIDC token from the secret
	oidcToken, err := r.getOIDCToken(ctx, event)
	if err != nil {
		return err
	}

	// Exchange token for AWS credentials
	result, err := r.assumeRoleWithToken(ctx, event, string(oidcToken))
	if err != nil {
		return err
	}

	// Update the credentials secret, allowing creation for initialization
	return r.updateSecret(ctx, event, result, true)
}

// Rotate implements the Rotator interface for AWS OIDC credentials.
// It performs the following steps:
// 1. Validates the rotation event
// 2. Exchanges the OIDC token for new AWS credentials
// 3. Updates the credentials secret
// 4. Schedules the next rotation before credential expiration
func (r *AWSOIDCRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if err := r.validateRotationEvent(event); err != nil {
		return err
	}

	// Exchange token for AWS credentials
	resp, err := r.assumeRoleWithToken(ctx, event, event.Metadata["id_token"])
	if err != nil {
		return err
	}

	// Update the credentials secret, not allowing creation during rotation
	if err := r.updateSecret(ctx, event, resp, false); err != nil {
		return err
	}

	// Schedule next rotation if needed
	return r.scheduleNextRotation(event, resp)
}

// -----------------------------------------------------------------------------
// Helper Methods - Token and Credential Management
// -----------------------------------------------------------------------------

// getOIDCToken retrieves the OIDC token from the specified secret
func (r *AWSOIDCRotator) getOIDCToken(ctx context.Context, event RotationEvent) ([]byte, error) {
	oidcSecret := &corev1.Secret{}
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Metadata["oidc-token-secret"],
	}, oidcSecret); err != nil {
		return nil, fmt.Errorf("failed to get OIDC token secret: %w", err)
	}

	oidcToken, ok := oidcSecret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("OIDC token not found in secret")
	}

	return oidcToken, nil
}

// assumeRoleWithToken exchanges an OIDC token for AWS credentials
func (r *AWSOIDCRotator) assumeRoleWithToken(ctx context.Context, event RotationEvent, token string) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	roleARN := event.Metadata["role_arn"]
	if roleARN == "" {
		roleARN = event.Metadata["role-arn"] // support both formats
	}
	if roleARN == "" {
		return nil, fmt.Errorf("role ARN is required in metadata")
	}

	return r.stsOps.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleARN),
		WebIdentityToken: aws.String(token),
		RoleSessionName:  aws.String(fmt.Sprintf(awsSessionNameFormat, event.Name)),
	})
}

// createCredentialsFileBytes creates formatted AWS credentials file bytes from STS credentials
func (r *AWSOIDCRotator) createCredentialsFileBytes(resp *sts.AssumeRoleWithWebIdentityOutput, region string) []byte {
	creds := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			defaultProfile: {
				profile:         defaultProfile,
				accessKeyID:     aws.ToString(resp.Credentials.AccessKeyId),
				secretAccessKey: aws.ToString(resp.Credentials.SecretAccessKey),
				sessionToken:    aws.ToString(resp.Credentials.SessionToken),
				region:          region,
			},
		},
	}
	return []byte(formatAWSCredentialsFile(creds))
}

// -----------------------------------------------------------------------------
// Helper Methods - Validation and Scheduling
// -----------------------------------------------------------------------------

// validateRotationEvent validates the required metadata for rotation
func (r *AWSOIDCRotator) validateRotationEvent(event RotationEvent) error {
	if event.Metadata["role_arn"] == "" && event.Metadata["role-arn"] == "" {
		return fmt.Errorf("role ARN is required in metadata")
	}
	if event.Metadata["id_token"] == "" {
		return fmt.Errorf("id_token is required in metadata")
	}
	return nil
}

// scheduleNextRotation schedules the next rotation before credentials expire
func (r *AWSOIDCRotator) scheduleNextRotation(event RotationEvent, resp *sts.AssumeRoleWithWebIdentityOutput) error {
	if resp.Credentials.Expiration == nil {
		return nil
	}

	// Calculate when we should rotate - 5 minutes before expiry
	rotateAt := resp.Credentials.Expiration.Add(-5 * time.Minute)

	// If we're not too close to expiry, schedule the next rotation
	if time.Until(rotateAt) > time.Second {
		// Create a new event for the next rotation
		nextEvent := RotationEvent{
			Namespace: event.Namespace,
			Name:      event.Name,
			Type:      RotationTypeAWSOIDC,
			Metadata: map[string]string{
				"role_arn":  event.Metadata["role_arn"],
				"id_token":  event.Metadata["id_token"],
				"rotate_at": rotateAt.Format(time.RFC3339),
			},
		}

		// Send the event through the schedule channel
		select {
		case r.scheduleChan <- nextEvent:
			r.logger.Info("scheduled next rotation",
				"namespace", event.Namespace,
				"name", event.Name,
				"rotateAt", rotateAt)
		default:
			r.logger.Error(fmt.Errorf("schedule channel is full"), "failed to schedule next rotation",
				"namespace", event.Namespace,
				"name", event.Name,
				"rotateAt", rotateAt)
		}
	}

	return nil
}

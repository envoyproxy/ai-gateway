package backend_auth_rotators

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
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
	// client is used for Kubernetes API operations
	client client.Client
	// kube provides additional Kubernetes API capabilities
	kube kubernetes.Interface
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
	client client.Client,
	kube kubernetes.Interface,
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
		client:       client,
		kube:         kube,
		logger:       logger,
		stsOps:       stsClient,
		rotationChan: rotationChan,
		scheduleChan: scheduleChan,
	}, nil
}

// Type returns the type of rotation this rotator handles
func (r *AWSOIDCRotator) Type() RotationType {
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

// Initialize implements the initial token retrieval for AWS OIDC tokens
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

	// Create new secret struct
	secret := newSecret(event.Namespace, event.Name)

	// Create credentials file
	credsFile := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			defaultProfile: {
				profile:         defaultProfile,
				accessKeyID:     aws.ToString(result.Credentials.AccessKeyId),
				secretAccessKey: aws.ToString(result.Credentials.SecretAccessKey),
				sessionToken:    aws.ToString(result.Credentials.SessionToken),
				region:          event.Metadata["region"],
			},
		},
	}

	// Update secret with credentials
	updateAWSCredentialsInSecret(secret, credsFile)
	return updateSecret(ctx, r.client, secret)
}

// Rotate implements the Rotator interface for AWS OIDC credentials
func (r *AWSOIDCRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if err := validateRotationEvent(event); err != nil {
		return err
	}

	// Exchange token for AWS credentials
	resp, err := r.assumeRoleWithToken(ctx, event, event.Metadata["id_token"])
	if err != nil {
		return err
	}

	// Get existing secret
	secret, err := lookupSecret(ctx, r.client, event.Namespace, event.Name)
	if err != nil {
		return err
	}

	// Create credentials file
	credsFile := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			defaultProfile: {
				profile:         defaultProfile,
				accessKeyID:     aws.ToString(resp.Credentials.AccessKeyId),
				secretAccessKey: aws.ToString(resp.Credentials.SecretAccessKey),
				sessionToken:    aws.ToString(resp.Credentials.SessionToken),
				region:          event.Metadata["region"],
			},
		},
	}

	// Update secret with credentials
	updateAWSCredentialsInSecret(secret, credsFile)
	if err := updateSecret(ctx, r.client, secret); err != nil {
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
	if err := r.client.Get(ctx, client.ObjectKey{
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

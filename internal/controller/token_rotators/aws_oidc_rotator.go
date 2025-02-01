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

// AWSOIDCRotator implements the Rotator interface for AWS OIDC token exchange
type AWSOIDCRotator struct {
	k8sClient    client.Client
	k8sClientset kubernetes.Interface
	logger       logr.Logger
	stsOps       STSOperations
	rotationChan <-chan RotationEvent
	scheduleChan chan<- RotationEvent
}

// NewAWSOIDCRotator creates a new AWS OIDC rotator
func NewAWSOIDCRotator(
	k8sClient client.Client,
	k8sClientset kubernetes.Interface,
	logger logr.Logger,
	rotationChan <-chan RotationEvent,
	scheduleChan chan<- RotationEvent,
) *AWSOIDCRotator {
	return &AWSOIDCRotator{
		k8sClient:    k8sClient,
		k8sClientset: k8sClientset,
		logger:       logger,
		rotationChan: rotationChan,
		scheduleChan: scheduleChan,
	}
}

// GetType implements the Rotator interface
func (r *AWSOIDCRotator) GetType() RotationType {
	return RotationTypeAWSOIDC
}

// SetSTSOperations sets the STS operations implementation - primarily used for testing
func (r *AWSOIDCRotator) SetSTSOperations(ops STSOperations) {
	r.stsOps = ops
}

// Start begins processing rotation events
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

	// If we have an expiry time, schedule the next rotation by sending a new event
	if resp.Credentials.Expiration != nil {
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
					"role_arn":  roleARN,
					"id_token":  idToken,
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
	}

	return nil
}

// Initialize implements the initial token retrieval for AWS OIDC tokens
func (r *AWSOIDCRotator) Initialize(ctx context.Context, event RotationEvent) error {
	r.logger.Info("initializing AWS OIDC token",
		"namespace", event.Namespace,
		"name", event.Name)

	// Get the secret containing the OIDC token
	oidcSecret := &corev1.Secret{}
	if err := r.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Metadata["oidc-token-secret"],
	}, oidcSecret); err != nil {
		return fmt.Errorf("failed to get OIDC token secret: %w", err)
	}

	// Get the OIDC token
	oidcToken, ok := oidcSecret.Data["token"]
	if !ok {
		return fmt.Errorf("OIDC token not found in secret")
	}

	// Assume role with web identity
	result, err := r.stsOps.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(event.Metadata["role-arn"]),
		RoleSessionName:  aws.String(fmt.Sprintf(awsSessionNameFormat, event.Name)),
		WebIdentityToken: aws.String(string(oidcToken)),
	})
	if err != nil {
		return fmt.Errorf("failed to assume role with web identity: %w", err)
	}

	// Create or update the credentials secret
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

	// Schedule next rotation based on token expiry
	if result.Credentials.Expiration != nil {
		r.scheduleChan <- RotationEvent{
			Namespace: event.Namespace,
			Name:      event.Name,
			Type:      event.Type,
			Metadata:  event.Metadata,
		}
	}

	return nil
}

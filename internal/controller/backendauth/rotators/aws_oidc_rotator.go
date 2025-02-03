package backendauthrotators

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/backendauth/oauth"
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
	// oidcProvider provides OIDC token provider
	oidcProvider oauth.Provider
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
	cfg, err := defaultAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	stsClient := NewSTSClient(cfg)

	// Create OIDC provider
	baseProvider := oauth.NewBaseProvider(client, logger, &http.Client{Timeout: 30 * time.Second})
	oidcProvider := oauth.NewOIDCProvider(baseProvider)

	return &AWSOIDCRotator{
		client:       client,
		kube:         kube,
		logger:       logger,
		stsOps:       stsClient,
		oidcProvider: oidcProvider,
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
				if !errors.Is(err, context.Canceled) {
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

	// Get OIDC configuration from metadata
	config, err := r.getOIDCConfig(ctx, event)
	if err != nil {
		return fmt.Errorf("failed to get OIDC config: %w", err)
	}

	// Fetch and validate OIDC token
	token, err := r.oidcProvider.FetchToken(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC token: %w", err)
	}

	// Exchange token for AWS credentials
	result, err := r.assumeRoleWithToken(ctx, event, token.IDToken)
	if err != nil {
		return err
	}

	// Create new secret struct
	secret := newSecret(event.Namespace, event.Name)

	// Get profile from metadata, defaulting to "default" if not specified
	profile := event.Metadata["profile"]
	if profile == "" {
		profile = "default"
	}

	// Create credentials file with the specified profile
	credsFile := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			profile: {
				profile:         profile,
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

	// Get OIDC configuration from metadata
	config, err := r.getOIDCConfig(ctx, event)
	if err != nil {
		return fmt.Errorf("failed to get OIDC config: %w", err)
	}

	// Fetch and validate OIDC token
	token, err := r.oidcProvider.FetchToken(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC token: %w", err)
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

	// Determine which profile to use
	profile, err := profileFromMetadata(event.Metadata, existingCreds)
	if err != nil {
		return fmt.Errorf("failed to determine AWS profile: %w", err)
	}

	// Exchange token for AWS credentials
	resp, err := r.assumeRoleWithToken(ctx, event, token.IDToken)
	if err != nil {
		return err
	}

	// Update only the specified profile's credentials
	existingCreds.profiles[profile].accessKeyID = aws.ToString(resp.Credentials.AccessKeyId)
	existingCreds.profiles[profile].secretAccessKey = aws.ToString(resp.Credentials.SecretAccessKey)
	existingCreds.profiles[profile].sessionToken = aws.ToString(resp.Credentials.SessionToken)
	existingCreds.profiles[profile].region = event.Metadata["region"]

	// Update secret with credentials
	updateAWSCredentialsInSecret(secret, existingCreds)
	if err := updateSecret(ctx, r.client, secret); err != nil {
		return err
	}

	// Schedule next rotation if needed
	return r.scheduleNextRotation(event, resp)
}

// -----------------------------------------------------------------------------
// Helper Methods - Token and Credential Management
// -----------------------------------------------------------------------------

// getOIDCConfig creates an OAuth config from the rotation event metadata
func (r *AWSOIDCRotator) getOIDCConfig(ctx context.Context, event RotationEvent) (oauth.Config, error) {
	// Convert metadata to expected format
	params := make(map[string]string)

	// Required fields
	params["token_url"] = event.Metadata["token_url"]
	params["client_id"] = event.Metadata["client_id"]
	params["client-secret-name"] = event.Metadata["client_secret_name"]

	// Optional fields
	if issuerURL := event.Metadata["issuer_url"]; issuerURL != "" {
		params["issuer_url"] = issuerURL
	}
	if scopes := event.Metadata["scopes"]; scopes != "" {
		params["scopes"] = scopes
	}

	return oauth.NewOIDCConfig(ctx, r.client, event.Namespace, params)
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
		// Create a new event for the next rotation, preserving all metadata
		nextEvent := RotationEvent{
			Namespace: event.Namespace,
			Name:      event.Name,
			Type:      RotationTypeAWSOIDC,
			Metadata:  make(map[string]string),
		}

		// Copy all metadata from the original event
		for k, v := range event.Metadata {
			nextEvent.Metadata[k] = v
		}

		// Update the rotation time
		nextEvent.Metadata["rotate_at"] = rotateAt.Format(time.RFC3339)

		// Send the event through the schedule channel
		select {
		case r.scheduleChan <- nextEvent:
			r.logger.Info("scheduled next rotation",
				"namespace", event.Namespace,
				"name", event.Name,
				"profile", event.Metadata["profile"],
				"rotateAt", rotateAt)
		default:
			r.logger.Error(fmt.Errorf("schedule channel is full"), "failed to schedule next rotation",
				"namespace", event.Namespace,
				"name", event.Name,
				"profile", event.Metadata["profile"],
				"rotateAt", rotateAt)
		}
	}

	return nil
}

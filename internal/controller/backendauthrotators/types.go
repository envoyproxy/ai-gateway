package backendauthrotators

import "context"

// RotationType represents the type of rotation to perform
type RotationType string

const (
	// RotationTypeAWSCredentials represents rotation of AWS IAM credentials
	RotationTypeAWSCredentials RotationType = "aws-credentials"
	// RotationTypeAWSOIDC represents rotation of AWS OIDC tokens
	RotationTypeAWSOIDC RotationType = "aws-oidc"
)

// RotationEvent represents a request to rotate credentials
type RotationEvent struct {
	// Namespace is the namespace of the secret to rotate
	Namespace string
	// Name is the name of the secret to rotate
	Name string
	// Type is the type of rotation to perform
	Type RotationType
	// Metadata contains additional data needed for rotation
	Metadata map[string]string
}

// Rotator is the interface that must be implemented by credential rotators
type Rotator interface {
	// Type returns the type of rotation this rotator handles
	Type() RotationType
	// Rotate performs the rotation
	Rotate(ctx context.Context, event RotationEvent) error
	// Initialize performs the initial token retrieval
	Initialize(ctx context.Context, event RotationEvent) error
}

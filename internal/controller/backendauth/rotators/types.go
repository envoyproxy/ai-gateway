package backendauthrotators

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RotationType represents the type of rotation to perform
type RotationType string

const (
	// RotationTypeAWSCredentials represents AWS IAM credentials rotation
	RotationTypeAWSCredentials RotationType = "aws-credentials"
	// RotationTypeAWSOIDC represents AWS OIDC token rotation
	RotationTypeAWSOIDC RotationType = "aws-oidc"
)

// RotationEvent represents a request to rotate credentials
type RotationEvent struct {
	// Namespace is the Kubernetes namespace containing the secret
	Namespace string
	// Name is the name of the secret to rotate
	Name string
	// Type is the type of rotation to perform
	Type RotationType
	// Metadata contains additional data needed for rotation
	Metadata map[string]string
}

// Rotator defines the interface for credential rotation implementations
type Rotator interface {
	// Type returns the type of rotation this rotator handles
	Type() RotationType
	// Initialize performs initial credential setup
	Initialize(ctx context.Context, event RotationEvent) error
	// Rotate performs credential rotation
	Rotate(ctx context.Context, event RotationEvent) error
}

// RotatorConfig contains common configuration for rotators
type RotatorConfig struct {
	// Client is used for Kubernetes API operations
	Client client.Client
	// KubeClient provides additional Kubernetes API capabilities
	KubeClient kubernetes.Interface
	// Logger is used for structured logging
	Logger logr.Logger
	// AWSConfig is the AWS configuration to use
	// If nil, the default AWS config will be loaded
	AWSConfig *aws.Config
	// IAMOperations provides AWS IAM operations
	// If nil, a new IAM client will be created using AWSConfig
	IAMOperations IAMOperations
}

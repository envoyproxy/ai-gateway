/*
Package backendauthrotators provides implementations for credential rotation in the AI Gateway.

The package provides a set of interfaces and implementations for rotating different types of credentials:

# Interfaces

- Rotator: The main interface that all credential rotators must implement, with Initialize and Rotate methods.

# Implementations

- AWS Credentials Rotator (AWSCredentialsRotator):
  - Handles IAM access key rotation with zero-downtime
  - Manages key creation, propagation delays, and safe deletion
  - Configurable propagation and deletion delays
  - Supports multiple AWS profiles and regions

- AWS OIDC Rotator (AWSOIDCRotator):
  - Handles OIDC token exchange with AWS STS
  - Automatic credential refresh before expiration
  - Channel-based rotation scheduling
  - Support for role assumption with web identity
  - Integration with Kubernetes secrets

# Events

The package uses events to communicate rotation status:

- RotationEvent: Represents a request to rotate credentials, containing:
  - Namespace and Name: Identifies the target credentials
  - Type: The type of rotation (AWS Credentials or OIDC)
  - Metadata: Provider-specific configuration

# Usage

To use a rotator:

1. Create an instance of the appropriate rotator
2. Register it with the BackendAuthManager
3. The manager will handle scheduling and executing rotations

Example for AWS Credentials:

	rotator, err := NewAWSCredentialsRotator(k8sClient, k8sClientset, logger)
	if err != nil {
		// Handle error
	}

Example for AWS OIDC:

	rotator, err := NewAWSOIDCRotator(
		k8sClient,
		k8sClientset,
		logger,
		rotationChan,
		scheduleChan,
	)
	if err != nil {
		// Handle error
	}

# Configuration

Rotators are configured through:

1. The BackendSecurityPolicy CRD, which specifies:
  - Credential type (AWS IAM or OIDC)
  - Provider-specific configuration
  - Storage location

2. Implementation-specific settings:
  - AWS Credentials: KeyDeletionDelay and MinPropagationDelay
  - AWS OIDC: Rotation scheduling interval and token refresh timing

# Error Handling

The package implements robust error handling:

1. Validation errors:
  - Missing required metadata
  - Invalid configuration
  - Malformed credentials

2. AWS API errors:
  - IAM operations failures
  - STS token exchange issues
  - Role assumption failures

3. Kubernetes errors:
  - Secret creation/update failures
  - RBAC-related issues

4. Recovery mechanisms:
  - Cleanup of partial states
  - Automatic retry scheduling
  - Safe rollback procedures

# Monitoring

The package provides monitoring through:

1. Structured logging:
  - Rotation start/completion events
  - Error details and context
  - Operation timing information

2. Kubernetes events:
  - Secret updates
  - Rotation status changes
  - Configuration changes

3. Status tracking:
  - Current credential state
  - Next scheduled rotation
  - Last rotation attempt result

# Security

The implementation follows AWS and Kubernetes security best practices:

1. Zero-downtime rotation:
  - New credentials created before old ones are invalidated
  - Configurable propagation delays
  - Safe cleanup of old credentials

2. Least privilege:
  - Namespace-scoped operations
  - Role-based access control
  - Minimal IAM permissions

3. Secure storage:
  - Kubernetes secrets for credential storage
  - Support for secret encryption at rest
  - Proper secret cleanup and garbage collection

4. Token handling:
  - Secure token exchange
  - Automatic session token refresh
  - Token expiration management

# Credential Storage

Credentials are stored in Kubernetes secrets with specific formats:

1. AWS IAM Credentials:
  - AWS credentials file format
  - Multiple profile support
  - Region configuration
  - Access key and secret key pairs

2. AWS OIDC Credentials:
  - Temporary STS credentials
  - Session tokens
  - Expiration tracking
  - Role ARN and session information

For more details about AWS security best practices, see:
https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html
*/
package backendauthrotators

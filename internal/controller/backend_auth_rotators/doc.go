/*
Package backend_auth_rotators provides implementations for credential rotation in the AI Gateway.

The package provides a set of interfaces and implementations for rotating different types of credentials:

# Interfaces

- Rotator: The main interface that all credential rotators must implement.

# Implementations

- AWS Credentials Rotator: Rotates AWS IAM credentials
- AWS OIDC Rotator: Rotates AWS OIDC tokens

# Events

The package uses events to communicate rotation status:

- RotationEvent: Represents a request to rotate credentials
- RotationEventType: Defines the type of rotation event (Started, Succeeded, Failed)

# Usage

To use a rotator:

1. Create an instance of the appropriate rotator
2. Register it with the BackendAuthManager
3. The manager will handle scheduling and executing rotations

Example:

	rotator := NewAWSCredentialsRotator(...)
	manager.RegisterRotator(rotator)

The manager will then use the rotator to:

1. Initialize credentials when first needed
2. Rotate credentials before they expire
3. Handle any errors during rotation

# Configuration

Rotators are configured through the BackendSecurityPolicy CRD, which specifies:

1. The type of credentials to rotate
2. Where to store the credentials
3. Any provider-specific configuration

# Error Handling

Rotators should:

1. Return meaningful errors that can be logged
2. Clean up any partial state on failure
3. Be idempotent where possible

# Monitoring

The package provides events that can be used to monitor rotation status:

1. Started events when rotation begins
2. Success events when rotation completes
3. Failure events with error details

These events can be used to:

1. Track rotation success/failure rates
2. Alert on repeated failures
3. Debug rotation issues

# Security

Rotators must:

1. Handle credentials securely
2. Clean up old credentials
3. Use secure communication channels
4. Follow least privilege principles

# Key Components

## Rotator Interface

The Rotator interface defines three key methods:
- Initialize: Performs initial credential setup
- Rotate: Performs credential rotation
- Type: Returns the type of rotation handled

# Usage

See individual rotator documentation for specific usage details.

# Available Rotators

1. AWS Credentials Rotator (AWSCredentialsRotator)
  - Handles IAM access key rotation
  - Manages key creation and deletion
  - Ensures zero-downtime rotation
  - Supports multiple AWS profiles

2. AWS OIDC Rotator (AWSOIDCRotator)
  - Handles OIDC token exchange with AWS
  - Manages temporary credentials
  - Automatic token refresh

# Usage Example

To use a rotator:

	// Create a rotator
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

	// Later, rotate the credentials
	err = rotator.Rotate(ctx, RotationEvent{
		Namespace: "default",
		Name:      "aws-creds",
		Type:      RotationTypeAWSCredentials,
		Metadata: map[string]string{
			"old_access_key_id": "AKIA...",
		},
	})

# Credential Storage

Credentials are stored in Kubernetes secrets with specific formats:

1. AWS IAM Credentials:
  - Stored in AWS credentials file format
  - Supports multiple profiles
  - Region configuration

2. AWS OIDC Credentials:
  - Temporary credentials from STS
  - Includes access key, secret key, and session token
  - Automatic expiration handling

# Security Considerations

The package implements several security best practices:

1. Zero-downtime rotation:
  - New credentials are created before old ones are deleted
  - Configurable propagation delays
  - Safe handling of rotation failures

2. Secure storage:
  - Credentials stored in Kubernetes secrets
  - Support for secret encryption at rest
  - Proper secret cleanup

3. Access control:
  - Namespace-scoped operations
  - Integration with Kubernetes RBAC
  - Audit logging support

# Error Handling

Errors are wrapped with context using fmt.Errorf and %w verb:

	if err != nil {
		return fmt.Errorf("operation failed: %w", err)
	}

Common error scenarios:
- AWS API errors
- Kubernetes API errors
- Validation errors
- Timeout errors

# Monitoring

The package provides monitoring capabilities through:
1. Structured logging
2. Kubernetes events
3. Error reporting
4. Status updates

For more information about AWS IAM best practices, see:
https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html
*/
package backend_auth_rotators

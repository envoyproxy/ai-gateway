/*
Package token_rotators provides implementations for credential rotation in the AI Gateway.

This package contains rotators for different types of credentials, with the primary focus
on AWS credentials rotation. The rotators implement the Rotator interface, which defines
the contract for credential rotation operations.

# Overview

The package provides a pluggable system for credential rotation, allowing different
types of credentials to be rotated using a common interface. Each rotator implements
the Rotator interface:

	type Rotator interface {
		Initialize(ctx context.Context, event RotationEvent) error
		Rotate(ctx context.Context, event RotationEvent) error
		GetType() RotationType
	}

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
package token_rotators

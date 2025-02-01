---
sidebar_position: 3
title: Token Rotators
description: Documentation for the token rotation system in AI Gateway
---

# Token Rotators

The AI Gateway includes a robust token rotation system that provides implementations for various types of credential rotation, with a primary focus on AWS credentials rotation.

## Overview

The system provides a pluggable architecture for credential rotation, allowing different types of credentials to be rotated using a common interface. Each rotator implements a standard interface that defines the contract for credential rotation operations.

## Available Rotators

### 1. AWS Credentials Rotator

The AWS Credentials Rotator handles IAM access key rotation with the following features:
- Manages key creation and deletion
- Ensures zero-downtime rotation
- Supports multiple AWS profiles

### 2. AWS OIDC Rotator

The AWS OIDC Rotator provides:
- OIDC token exchange with AWS
- Management of temporary credentials
- Automatic token refresh

## Usage Example

```go
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
```

## Credential Storage

Credentials are securely stored in Kubernetes secrets with specific formats:

### AWS IAM Credentials
- Stored in AWS credentials file format
- Supports multiple profiles
- Includes region configuration

### AWS OIDC Credentials
- Temporary credentials from STS
- Includes access key, secret key, and session token
- Automatic expiration handling

## Security Considerations

The token rotation system implements several security best practices:

### 1. Zero-downtime Rotation
- New credentials are created before old ones are deleted
- Configurable propagation delays
- Safe handling of rotation failures

### 2. Secure Storage
- Credentials stored in Kubernetes secrets
- Support for secret encryption at rest
- Proper secret cleanup

### 3. Access Control
- Namespace-scoped operations
- Integration with Kubernetes RBAC
- Audit logging support

## Error Handling

The system implements comprehensive error handling with:
- AWS API error handling
- Kubernetes API error handling
- Validation error checks
- Timeout handling

## Monitoring

The token rotation system provides monitoring through:
1. Structured logging
2. Kubernetes events
3. Error reporting
4. Status updates

For more information about AWS IAM best practices, see the [AWS IAM documentation](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html). 

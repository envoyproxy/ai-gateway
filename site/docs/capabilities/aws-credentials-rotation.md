---
title: AWS Credentials Rotation
description: Automated rotation of AWS credentials for enhanced security
---

# AWS Credentials Rotation

The AWS Credentials Rotation feature provides automated rotation of AWS credentials for enhanced security. It supports two types of credential rotation:

1. IAM Access Key rotation
2. OIDC-based token exchange with AWS STS

## Overview

The feature is implemented through the `BackendSecurityPolicy` resource with type `AWSCredentials`. It automatically manages and rotates AWS credentials stored in Kubernetes secrets, ensuring secure access to AWS services.

## Architecture

The credential rotation system consists of three main components:

1. **Token Manager**: Central coordinator that manages different types of credential rotators
2. **Rotator Interface**: Common interface for implementing credential rotation strategies
3. **Rotator Implementations**:
   - `AWSCredentialsRotator`: Handles IAM access key rotation
   - `AWSOIDCRotator`: Manages OIDC token exchange with AWS STS

This modular architecture allows for:
- Easy addition of new rotation types
- Consistent handling of rotation events
- Centralized error handling and logging
- Clean separation of concerns

## Configuration

### Basic IAM Access Key Rotation

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: aws-credentials
  namespace: default
spec:
  type: AWSCredentials
  awsCredentials:
    credentialsFile:
      secretRef:
        name: aws-credentials
      profile: default  # Optional, defaults to "default"
```

### OIDC Token Exchange

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: aws-oidc-credentials
  namespace: default
spec:
  type: AWSCredentials
  awsCredentials:
    oidcExchangeToken:
      oidc:
        provider:
          issuer: "https://token.actions.githubusercontent.com"
        clientId: "my-client-id"
        clientSecret:
          name: github-token
      awsRoleArn: "arn:aws:iam::123456789012:role/my-role"
```

## How It Works

### Token Manager

The Token Manager:
1. Maintains a registry of credential rotators
2. Receives rotation requests from controllers
3. Routes rotation events to appropriate rotators
4. Handles concurrent rotations safely
5. Provides context-aware cancellation
6. Manages error handling and logging

### IAM Access Key Rotation

The `AWSCredentialsRotator`:
1. Monitors `BackendSecurityPolicy` resources with type `AWSCredentials`
2. When rotation is requested:
   - Creates a new IAM access key
   - Updates the credentials secret with the new key
   - Schedules deletion of the old access key after a delay
3. Handles multiple AWS profiles in a single credentials file
4. Provides safe concurrent access to AWS credentials

### OIDC Token Exchange

The `AWSOIDCRotator`:
1. Receives OIDC token exchange requests
2. Validates required metadata (role ARN and ID token)
3. Exchanges OIDC token for temporary AWS credentials using STS
4. Updates the target secret with the new credentials
5. Formats credentials in AWS credentials file format

## Security Considerations

1. **Concurrent Operations**:
   - Thread-safe rotation handling
   - Mutex-protected rotator registry
   - Safe concurrent secret updates

2. **Error Handling**:
   - Context-aware cancellation
   - Graceful error recovery
   - Detailed error logging
   - Safe cleanup on failures

3. **Credential Management**:
   - Secure credential storage in Kubernetes secrets
   - Automatic cleanup of old credentials
   - Support for multiple credential profiles

## Best Practices

1. **Implementation**:
   - Implement the `Rotator` interface for new rotation types
   - Use context for cancellation and timeouts
   - Handle concurrent operations safely
   - Provide detailed error messages

2. **Usage**:
   - Monitor rotation logs
   - Handle rotation errors appropriately
   - Set up alerts for failed rotations
   - Use appropriate IAM permissions

## Example Use Cases

1. **AWS Bedrock Integration**:
This example demonstrates how to configure credentials for accessing AWS Bedrock LLM services. This is particularly useful for:
- Secure access to AWS Bedrock models
- Automatic credential rotation for production workloads
- Maintaining secure access across multiple AWS regions

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: bedrock-credentials
spec:
  type: AWSCredentials
  awsCredentials:
    credentialsFile:
      secretRef:
        name: bedrock-credentials
```

Prerequisites:
1. Create an IAM user with appropriate Bedrock permissions
2. Store initial AWS credentials in a Kubernetes secret named 'bedrock-credentials'
3. Ensure the IAM user has permissions for key rotation

The rotator will:
1. Automatically rotate the IAM access keys
2. Update the credentials in the Kubernetes secret
3. Clean up old access keys after successful rotation

2. **Cross-Account Access**:
This example shows how to use OIDC token exchange for accessing AWS services across accounts:

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: cross-account-access
spec:
  type: AWSCredentials
  awsCredentials:
    oidcExchangeToken:
      oidc:
        provider:
          issuer: "https://oidc.eks.region.amazonaws.com/id/CLUSTER_ID"
        clientId: "sts.amazonaws.com"
        clientSecret:
          name: eks-token
      awsRoleArn: "arn:aws:iam::ACCOUNT_ID:role/bedrock-access-role"
```

Prerequisites:
1. Configure EKS OIDC provider in the target AWS account
2. Create an IAM role with appropriate trust relationships and Bedrock permissions
3. Configure service account with appropriate annotations

This setup allows:
- Secure cross-account access to AWS services
- Automatic credential rotation without long-lived access keys
- Fine-grained access control through IAM roles

## Limitations

1. AWS API rate limits apply to credential operations
2. OIDC token exchange requires valid trust relationships
3. Requires appropriate IAM permissions for key creation/deletion
4. Secret updates must be atomic to prevent race conditions
5. Rotator implementations must be registered before use

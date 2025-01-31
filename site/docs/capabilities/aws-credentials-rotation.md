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
    region: us-west-2
    credentialsFile:
      secretRef:
        name: aws-credentials
      profile: default  # Optional, defaults to "default"
    rotationConfig:     # Optional
      rotationInterval: "24h"
      preRotationWindow: "1h"
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
    region: us-west-2
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

### IAM Access Key Rotation

1. The controller monitors `BackendSecurityPolicy` resources with type `AWSCredentials`.
2. When rotation is needed (based on `rotationInterval` or first deployment):
   - Creates a new IAM access key
   - Updates the credentials secret with the new key
   - Deletes the old access key after successful update

### OIDC Token Exchange

1. The controller obtains an OIDC token using the configured provider
2. Exchanges the OIDC token for temporary AWS credentials using `AssumeRoleWithWebIdentity`
3. Creates/updates a secret with the `-oidc-creds` suffix containing the temporary credentials
4. Automatically rotates credentials before they expire

## Rotation Configuration

- `rotationInterval`: How often to rotate credentials (default: 24h)
- `preRotationWindow`: How long before expiry to start rotation (default: 1h)

For OIDC credentials, rotation is based on the expiry time of the STS credentials.

## Security Considerations

1. **Secret Management**:
   - Original credentials are stored in Kubernetes secrets
   - OIDC credentials are stored in separate secrets with `-oidc-creds` suffix
   - Secrets are automatically managed by the controller

2. **Access Control**:
   - Uses IAM roles and policies for OIDC-based access
   - Supports fine-grained permissions through AWS IAM

3. **Credential Lifecycle**:
   - Old credentials are securely deleted after rotation
   - Failed rotations are handled gracefully with retries
   - Automatic rotation before expiry prevents disruption

## Error Handling

The controller handles various error scenarios:
- Missing or invalid configuration
- AWS API errors
- Rate limiting
- Concurrent modifications
- Invalid credentials
- Network issues

## Best Practices

1. **IAM Access Keys**:
   - Use short rotation intervals (24h recommended)
   - Configure appropriate IAM policies
   - Monitor rotation logs for issues

2. **OIDC Integration**:
   - Use secure OIDC providers
   - Configure appropriate role trust relationships
   - Set reasonable session durations

3. **Monitoring**:
   - Monitor controller logs for rotation events
   - Set up alerts for rotation failures
   - Track credential usage and expiry

## Troubleshooting

Common issues and solutions:

1. **Rotation Failures**:
   - Check controller logs for error messages
   - Verify IAM permissions
   - Ensure secret access is available

2. **OIDC Issues**:
   - Verify OIDC provider configuration
   - Check client credentials
   - Validate role ARN and trust relationships

3. **Secret Management**:
   - Check secret permissions
   - Verify secret format
   - Monitor for concurrent modifications

## Example Use Cases

1. **GitHub Actions Integration**:
```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: github-aws-credentials
spec:
  type: AWSCredentials
  awsCredentials:
    region: us-west-2
    oidcExchangeToken:
      oidc:
        provider:
          issuer: "https://token.actions.githubusercontent.com"
        clientId: "my-client-id"
        clientSecret:
          name: github-token
      awsRoleArn: "arn:aws:iam::123456789012:role/github-actions-role"
```

2. **Regular IAM Key Rotation**:
```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: aws-service-account
spec:
  type: AWSCredentials
  awsCredentials:
    region: us-west-2
    credentialsFile:
      secretRef:
        name: service-account-credentials
    rotationConfig:
      rotationInterval: "12h"
      preRotationWindow: "30m"
```

## Limitations

1. Maximum of two active access keys per IAM user
2. AWS API rate limits apply
3. OIDC token exchange requires valid trust relationships
4. Minimum rotation interval of 1 hour
5. Maximum session duration based on role settings 

# Controller Package

## AWS Credentials Rotator

The AWS credentials rotator is a controller that manages the automatic rotation of AWS credentials. It supports two types of credential management:

1. IAM Access Key rotation
2. OIDC-based token exchange with AWS STS

### Key Components

- `awsCredentialsRotator`: Main controller implementing the credentials rotation logic
- `mockIAMClient` and `mockSTSClient`: Test implementations of AWS clients
- Supporting types and utilities for credentials file management

### Testing

The package includes comprehensive tests covering:
- Basic IAM key rotation scenarios
- OIDC token exchange flows
- Error handling and edge cases
- Configuration parsing and validation
- Concurrent modification handling

Run tests with:
```bash
go test -v ./internal/controller/...
```

### Implementation Details

The controller follows the Kubernetes operator pattern:
1. Watches `BackendSecurityPolicy` resources
2. Manages credential rotation based on configured intervals
3. Handles AWS API interactions for key management
4. Maintains Kubernetes secrets with credentials

For detailed feature documentation, see [AWS Credentials Rotation](../../docs/features/aws-credentials-rotation.md). 

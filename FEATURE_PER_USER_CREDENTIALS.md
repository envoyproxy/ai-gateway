# Per-Request AWS Credentials for Cost Attribution

## Summary

This feature enables per-user cost attribution in AWS billing by allowing the AI Gateway to use per-request AWS credentials from headers instead of only using the default AWS credential chain (IRSA, EKS Pod Identity, EC2 Instance Roles, ECS Task Roles, etc.).

## Implementation

### Changes Made

1. **Modified `internal/backendauth/aws.go`**:
   - Enhanced `awsHandler.Do()` method to check for per-request AWS credential headers
   - If `x-aws-access-key-id`, `x-aws-secret-access-key`, and optionally `x-aws-session-token` headers are present, use them for signing
   - If headers are not present, fall back to IRSA/default credential chain (backward compatible)
   - Automatically removes credential headers after use to prevent leakage

2. **Added comprehensive tests in `internal/backendauth/aws_test.go`**:
   - Test per-request credentials with session token
   - Test per-request credentials without session token
   - Test incomplete credentials error handling
   - Test fallback to IRSA when no headers present
   - Test concurrent requests with mixed credential sources
   - All tests pass ✅

3. **Created example configuration**:
   - `examples/basic/aws-per-user-cost-attribution.yaml` - Complete example with ext-auth integration
   - Updated `examples/basic/README.md` with documentation

## How It Works

```
User Request
  ↓
ext-auth service (FastAPI/similar)
  ├─ Validates JWT/API key
  ├─ Calls STS AssumeRole(user_email, session_tags=[...])
  ├─ Caches temporary credentials in Redis (50-min TTL)
  ├─ Injects x-aws-* headers
  ↓
Envoy AI Gateway extproc
  ├─ Checks for x-aws-* headers
  ├─ If present: Uses per-request credentials
  ├─ If not present: Falls back to AWS credential chain
  │   (IRSA, Pod Identity, EC2 role, ECS role, etc.)
  ├─ Removes credential headers
  ↓
AWS Bedrock InvokeModel
  └─ Shows per-user identity in CloudTrail with session tags
```

## Benefits

- ✅ **Per-user cost attribution** in AWS Cost Explorer and CUR 2.0
- ✅ **Session tags for aggregation** by team, department, environment, tenant
- ✅ **Backward compatible** - existing AWS credential configurations continue to work
- ✅ **Platform agnostic** - works with IRSA, Pod Identity, EC2 roles, ECS roles, Lambda, etc.
- ✅ **Security** - credential headers are removed after use
- ✅ **Thread-safe** - concurrent requests with different credentials work correctly
- ✅ **Centralized credential management** - one STS call per user per hour

## AWS Cost Attribution Use Case

Implements **AWS Scenario 4: "Per-user tracking through an LLM gateway"** from the [AWS Bedrock granular cost attribution blog](https://aws.amazon.com/blogs/machine-learning/granular-cost-attribution-for-amazon-bedrock/).

### Before (Current Behavior)
All Bedrock calls appear in CloudTrail as:
```
arn:aws:sts::123456789012:assumed-role/ai-gateway-role/1776782298841908997
```
(Pod's default role with random numeric session name)

### After (With This Feature)
Bedrock calls appear in CloudTrail as:
```
arn:aws:sts::123456789012:assumed-role/ai-gateway-bedrock-tagging/user@example.com
```

With session tags in CUR 2.0:
```json
{
  "iamPrincipal/user-id": "user@example.com",
  "iamPrincipal/tier": "standard",
  "iamPrincipal/team": "engineering",
  "iamPrincipal/environment": "production"
}
```

## Security Considerations

1. **Header validation**: Only accept credential headers from trusted sources (use ExtAuthz/SecurityPolicy)
2. **Credential removal**: Headers are automatically removed after use to prevent leakage
3. **Incomplete credentials**: Returns error if only partial credentials are provided
4. **Backward compatibility**: Falls back to IRSA when no headers present

## Testing

Run the tests:
```bash
go test -v ./internal/backendauth/... -run TestAWSHandler
```

All 17 tests pass, including:
- ✅ Per-request credentials with session token
- ✅ Per-request credentials without session token
- ✅ Incomplete credentials error handling
- ✅ Fallback to IRSA when no headers
- ✅ Concurrent requests with mixed credential sources

## Example Usage

Complete working examples for different platforms:

### Kubernetes on EKS
- **EKS 1.24+**: `examples/basic/aws-per-user-cost-attribution-pod-identity.yaml`
- **EKS < 1.24**: `examples/basic/aws-per-user-cost-attribution.yaml` (IRSA)

### Kubernetes on EC2 (self-managed)
- `examples/basic/aws-per-user-cost-attribution-ec2.yaml`

### AWS ECS/Fargate
- `examples/basic/aws-per-user-cost-attribution-ecs.yaml`

Each example includes:
- Platform-specific credential configuration
- SecurityPolicy for external authentication
- Example ext-auth service implementation
- IAM policies and trust relationships
- Testing instructions

## Related Issues

- Addresses: https://github.com/envoyproxy/ai-gateway/issues/2076
- Implements AWS Scenario 4 from: https://aws.amazon.com/blogs/machine-learning/granular-cost-attribution-for-amazon-bedrock/

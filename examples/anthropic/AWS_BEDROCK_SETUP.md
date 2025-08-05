# Anthropic Native API → AWS Bedrock Setup Guide

This guide shows how to configure AI Gateway to route native Anthropic `/v1/messages` requests to AWS Bedrock using the new InvokeModel translator.

## 🚀 Quick Start

### 1. Apply Configuration
```bash
kubectl apply -f anthropic-aws-bedrock.yaml
```

### 2. Update AWS Credentials
```bash
# Replace with your actual AWS credentials
kubectl patch secret aws-credentials -p '{
  "stringData": {
    "credentials": "[default]\naws_access_key_id=YOUR_ACTUAL_ACCESS_KEY\naws_secret_access_key=YOUR_ACTUAL_SECRET_KEY"
  }
}'
```

### 3. Test the Integration
```bash
# Test with native Anthropic API format
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-provider: aws" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "max_tokens": 1024,
    "messages": [
      {
        "role": "user",
        "content": "Hello from AWS Bedrock via native Anthropic API!"
      }
    ]
  }'
```

## 🔧 Configuration Details

### Key Components

1. **AIGatewayRoute** (`schema: Anthropic`)
   - Enables native `/v1/messages` endpoint
   - Routes based on `x-provider: aws` header

2. **AIServiceBackend** (`schema: AWSBedrock`)
   - Triggers our new InvokeModel translator
   - Points to AWS Bedrock service

3. **BackendSecurityPolicy** (`type: AWSCredentials`)
   - Handles AWS authentication
   - Supports both static credentials and OIDC

### Routing Options

**Option 1: Provider-based routing**
```bash
curl -H "x-provider: aws" ...
```

**Option 2: Model-based routing**
```bash
# Use AWS Bedrock model format in request
{
  "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
  ...
}
```

**Option 3: Default routing**
- All Anthropic requests go to AWS Bedrock

## 🔐 AWS Credentials Setup

### Option A: Static Credentials (Development)
```yaml
awsCredentials:
  credentialsFile:
    secretRef:
      name: aws-credentials
      key: credentials
```

### Option B: OIDC Role (Production)
```yaml
awsCredentials:
  oidcConfig:
    roleArn: "arn:aws:iam::123456789012:role/ai-gateway-bedrock-role"
```

## 🌍 Multi-Region Support

Update the service endpoint for different regions:
```yaml
spec:
  externalName: bedrock-runtime.us-west-2.amazonaws.com  # Change region
```

## 📋 AWS Prerequisites

1. **Enable Bedrock Models**
   - Go to AWS Bedrock Console → Model Access
   - Enable Anthropic Claude models

2. **IAM Permissions**
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": [
           "bedrock:InvokeModel",
           "bedrock:InvokeModelWithResponseStream"
         ],
         "Resource": "arn:aws:bedrock:*::foundation-model/anthropic.*"
       }
     ]
   }
   ```

## 🎯 Benefits of This Approach

- ✅ **Native Anthropic API**: Full `/v1/messages` compatibility
- ✅ **All Features**: Tools, streaming, multimodal, etc.
- ✅ **Minimal Overhead**: Just adds `anthropic_version` field
- ✅ **Future-Proof**: New Anthropic features work immediately
- ✅ **Easy Migration**: Drop-in replacement for Anthropic API

## 🔍 Troubleshooting

### Common Issues

1. **Model Access Error**
   ```
   AccessDeniedError: Could not access model
   ```
   **Solution**: Enable the model in AWS Bedrock Console

2. **Authentication Error**
   ```
   SignatureDoesNotMatch
   ```
   **Solution**: Check AWS credentials and region

3. **Model Not Found**
   ```
   ValidationException: Unknown model
   ```
   **Solution**: Use correct AWS Bedrock model ID format

### Debug Commands

```bash
# Check if configuration is applied
kubectl get aigatewayroute,aiservicebackend,backendsecuritypolicy

# Check pod logs
kubectl logs -l app=ai-gateway-extproc

# Test connectivity
kubectl exec -it deployment/ai-gateway -- curl https://bedrock-runtime.us-east-1.amazonaws.com
```

## 📚 Examples

### Basic Chat
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "max_tokens": 500,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Streaming Chat
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "max_tokens": 500,
    "stream": true,
    "messages": [{"role": "user", "content": "Count to 10"}]
  }'
```

### With System Prompt
```bash
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "max_tokens": 300,
    "system": "You are a helpful AI assistant.",
    "messages": [{"role": "user", "content": "Explain quantum computing"}]
  }'
```
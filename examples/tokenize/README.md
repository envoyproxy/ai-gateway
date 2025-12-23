# Tokenize Endpoint Configuration Examples

This directory contains example configurations for setting up the tokenize endpoint (`/tokenize`) in Envoy AI Gateway. The tokenize endpoint allows you to count tokens in text input without generating a response, which is useful for cost estimation, prompt optimization, and understanding model input limits.

## Overview

The tokenize endpoint supports:

- **Chat message tokenization** (OpenAI messages format)
- **Completion prompt tokenization** (single string prompt)
- **Multiple AI providers** (OpenAI, vLLM, GCP Vertex AI, AWS Bedrock, GCP Anthropic)
- **Provider fallback and load balancing**
- **Cost optimization workflows**

## Example Configurations

### 1. `simple-test.yaml` - Basic Setup

A minimal configuration for testing the tokenize endpoint with a single backend.

**Use Cases:**

- Initial testing and validation
- Simple development environments
- Single provider deployments

**Features:**

- Single OpenAI-compatible backend
- Basic API key authentication
- Minimal configuration

```bash
kubectl apply -f simple-test.yaml
# Create secret: kubectl create secret generic test-api-secret --from-literal=api-key="your-api-key"
```

### 2. `openai-vllm.yaml` - OpenAI and vLLM

Configuration for using both OpenAI API and vLLM backends for tokenization.

**Use Cases:**

- Hybrid cloud/on-premises deployments
- Cost optimization (vLLM for high volume, OpenAI for accuracy)
- Custom model tokenization with vLLM

**Features:**

- OpenAI backend for standard models
- vLLM backend for custom/open-source models
- Header-based routing (`x-ai-eg-backend`)
- Model-specific configurations

```bash
kubectl apply -f openai-vllm.yaml
# Update vLLM hostname in the config before applying
```

### 3. `gcp-vertex-ai.yaml` - GCP Vertex AI

Configuration for using GCP Vertex AI Gemini models for tokenization.

**Use Cases:**

- Google Cloud environments
- Gemini model tokenization
- Enterprise Google Cloud integrations

**Features:**

- GCP Vertex AI backend with automatic API translation
- Service account-based authentication
- Workload Identity support
- Regional endpoint configuration

```bash
kubectl apply -f gcp-vertex-ai.yaml
# Set up GCP authentication (see comments in file)
```

### 4. `aws-bedrock.yaml` - AWS Bedrock

Configuration for using AWS Bedrock Claude models for tokenization.

**Use Cases:**

- AWS cloud environments
- Claude model tokenization via Bedrock
- Enterprise AWS integrations

**Features:**

- AWS Bedrock backend with automatic API translation
- IAM-based authentication (access keys or IRSA)
- Regional endpoint configuration
- Chat messages only (completion prompts not supported)

```bash
kubectl apply -f aws-bedrock.yaml
# Set up AWS authentication (see comments in file)
```

### 5. `gcp-anthropic.yaml` - GCP Anthropic

Configuration for using GCP Anthropic Claude models for tokenization.

**Use Cases:**

- Google Cloud environments with Anthropic models
- Claude model tokenization via GCP
- Enterprise GCP Anthropic integrations

**Features:**

- GCP Anthropic backend with automatic API translation
- Service account-based authentication
- Workload Identity support
- Chat messages only (completion prompts not supported)

```bash
kubectl apply -f gcp-anthropic.yaml
# Set up GCP authentication (see comments in file)
```

### 6. `multi-backend.yaml` - Comprehensive Multi-Backend

Advanced configuration with multiple backends, fallback, and load balancing.

**Use Cases:**

- Production environments
- High availability deployments
- Cost-optimized routing strategies
- Multiple provider failover

**Features:**

- Multiple backends (OpenAI, vLLM, GCP Vertex AI, AWS Bedrock, GCP Anthropic)
- Provider fallback and load balancing
- Model-specific routing rules
- Usage-based rate limiting
- Comprehensive authentication

```bash
kubectl apply -f multi-backend.yaml
# Create all required secrets before applying
```

## Testing the Configuration

Once deployed, test the tokenize endpoint with different request types:

### Chat Message Tokenization

```bash
curl -X POST http://localhost:8080/tokenize \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "How many tokens is this message?"}
    ],
    "return_token_strs": true
  }'
```

### Completion Prompt Tokenization

```bash
curl -X POST http://localhost:8080/tokenize \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo-instruct",
    "prompt": "Once upon a time, in a land far away",
    "add_special_tokens": true
  }'
```

### Backend-Specific Routing

```bash
# Route to vLLM backend
curl -X POST http://localhost:8080/tokenize \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-backend: vllm" \
  -d '{
    "model": "llama-2-7b-chat",
    "prompt": "Hello, world!"
  }'

# Route to GCP backend
curl -X POST http://localhost:8080/tokenize \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-backend: gcp" \
  -d '{
    "model": "gemini-1.5-pro",
    "messages": [{"role": "user", "content": "Test message"}]
  }'
```

## Response Format

The tokenize endpoint returns:

```json
{
  "count": 15, // Total token count
  "max_model_len": 4096, // Model's context limit (optional)
  "tokens": [1234, 5678, 9012], // Token IDs (optional)
  "token_strs": ["Hello", " world", "!"] // Token strings (optional)
}
```

## Common Use Cases

### 1. Cost Estimation Before Chat Completion

```bash
# 1. First, tokenize to estimate cost
curl -X POST $GATEWAY_URL/tokenize \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Your long prompt here..."}]
  }'

# 2. If under budget, proceed with completion
curl -X POST $GATEWAY_URL/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Your long prompt here..."}]
  }'
```

### 2. Batch Processing Optimization

```bash
# Tokenize multiple prompts to optimize batching
for prompt in "prompt1" "prompt2" "prompt3"; do
  curl -X POST $GATEWAY_URL/tokenize \
    -H "Content-Type: application/json" \
    -d "{\"model\": \"gpt-3.5-turbo\", \"prompt\": \"$prompt\"}"
done
```

### 3. Model Comparison for Token Efficiency

```bash
# Compare token counts across different models
for model in "gpt-3.5-turbo" "gemini-1.5-flash"; do
  echo "Model: $model"
  curl -X POST $GATEWAY_URL/tokenize \
    -H "Content-Type: application/json" \
    -d "{\"model\": \"$model\", \"prompt\": \"Your test prompt\"}"
done
```

## Configuration Notes

### Backend Types

1. **OpenAI Schema**: Direct passthrough for OpenAI API and compatible services
2. **GCPVertexAI Schema**: Automatic translation to Gemini CountTokens API
3. **AWSBedrock Schema**: Automatic translation to AWS Bedrock CountTokens API (chat messages only)
4. **GCPAnthropic Schema**: Automatic translation to Anthropic MessageCountTokens API (chat messages only)

### Authentication

- **OpenAI/vLLM**: API key via `APIKey` security policy
- **GCP Vertex AI**: OAuth2 with service account or Workload Identity
- **AWS Bedrock**: IAM credentials via `AWS` security policy (access keys or IRSA)
- **GCP Anthropic**: OAuth2 with service account or Workload Identity
- **Enterprise**: Custom authentication mechanisms supported

### Performance Considerations

- **Caching**: Consider implementing caching for repeated tokenize requests
- **Rate Limiting**: Use `BackendRateLimitPolicy` for usage control
- **Fallback**: Configure multiple backends for high availability

### Monitoring

The tokenize endpoint provides full observability:

- **Metrics**: Request count, duration, token counts per model
- **Tracing**: End-to-end request tracing with OpenInference
- **Logging**: Structured logs with request/response details

## Troubleshooting

### Common Issues

1. **"unsupported API schema"**: Check that your backend uses a supported schema (OpenAI, GCPVertexAI, AWSBedrock, GCPAnthropic)
2. **Authentication failures**: Verify API keys and service account permissions
3. **Model not found**: Ensure the model name matches what your backend supports
4. **Invalid request format**: Check that request contains either `messages` or `prompt`, not both

### Debug Commands

```bash
# Check backend status
kubectl get aiservicebackends
kubectl describe aiservicebackend your-backend-name

# Check authentication
kubectl get backendsecuritypolicies
kubectl describe secret your-api-secret

# View logs
kubectl logs -l app=envoy-ai-gateway -f
```

For more information, see:

- [Supported Endpoints Documentation](https://aigateway.envoyproxy.io/docs/capabilities/llm-integrations/supported-endpoints/)
- [AI Gateway Configuration Guide](https://aigateway.envoyproxy.io/docs/concepts/)
- [Troubleshooting Guide](https://aigateway.envoyproxy.io/docs/troubleshooting/)

---
id: gcp-vertexai
title: Connect GCP VertexAI
sidebar_position: 3
---

import CodeBlock from '@theme/CodeBlock';
import vars from '../../\_vars.json';

# Connect GCP VertexAI

This guide will help you configure Envoy AI Gateway to work with GCP VertexAI's Gemini and Anthropic models.

## Prerequisites

Before you begin, you'll need:

- GCP credentials with access to GCP VertexAI
- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

## GCP Credentials Setup

Ensure you have:

1. Your GCP project id and name.
2. In your GCP project, enable VertexAI API access.
3. Create a GCP service account and generate the JSON key file.

:::tip GCP Best Practices
Consider using GCP Workload Identity (Federation)/IAM roles and limited-scope credentials for production environments.
:::

## Configuration Steps

### 1. Download configuration template

<CodeBlock language="shell">
{`curl -O https://raw.githubusercontent.com/envoyproxy/ai-gateway/${vars.aigwGitRef}/examples/basic/gcp_vertex.yaml`}
</CodeBlock>

### 2. Configure GCP Credentials

Edit the `gcp_vertex.yaml` file to replace these placeholder values:

- `GCP_PROJECT_NAME`: Your GCP project name
- `GCP_REGION`: GCP region
- Update the generated service account key JSON string in the secret

:::caution Security Note
Make sure to keep your GCP service account credentials secure and never commit them to version control.
The credentials will be stored in Kubernetes secrets.
:::

### 3. Apply Configuration

Apply the updated configuration and wait for the Gateway pod to be ready. If you already have a Gateway running,
then the secret credential update will be picked up automatically in a few seconds.

```shell
kubectl apply -f gcp_vertex.yaml

kubectl wait pods --timeout=2m \
  -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
  -n envoy-gateway-system \
  --for=condition=Ready
```

### 4. Test the Configuration

You should have set `$GATEWAY_URL` as part of the basic setup before connecting to providers.
See the [Basic Usage](../basic-usage.md) page for instructions.

To access a Gemini model with chat completion endpoint:

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash",
    "messages": [
      {
        "role": "user",
        "content": "Hi."
      }
    ]
  }' \
  $GATEWAY_URL/v1/chat/completions
```

To access an Anthropic model with chat completion endpoint:

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-7-sonnet@20250219",
    "messages": [
      {
        "role": "user",
        "content": "What is capital of France?"
      }
    ],
    "max_completion_tokens": 100
  }' \
  $GATEWAY_URL/v1/chat/completions
```

**NEW: To use GCP embedding models:**

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-004",
    "input": "The quick brown fox jumps over the lazy dog"
  }' \
  $GATEWAY_URL/v1/embeddings
```

**Response:**
```json
{
  "object": "list",
  "data": [
    {
      "object": "embedding",
      "index": 0,
      "embedding": [0.1, -0.2, 0.3, ...]
    }
  ],
  "model": "text-embedding-004",
  "usage": {
    "prompt_tokens": 0,
    "total_tokens": 0
  }
}
```

**NEW: To use chat-style embedding requests:**

Chat-style embedding requests allow you to convert entire conversations into embeddings, perfect for RAG (Retrieval-Augmented Generation) scenarios:

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-004",
    "messages": [
      {
        "role": "user",
        "content": "What is machine learning?"
      },
      {
        "role": "assistant",
        "content": "Machine learning is a subset of artificial intelligence that enables computers to learn and improve from experience."
      }
    ]
  }' \
  $GATEWAY_URL/v1/embeddings
```

**Multiple input support:**
```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-004",
    "input": ["First document", "Second document", "Third document"]
  }' \
  $GATEWAY_URL/v1/embeddings
```

## Supported Input Formats

The GCP VertexAI integration supports the following embedding input formats:

### ✅ Fully Supported
- **Single Text** (`string`): `"Hello world"`
- **Multiple Texts** (`[]string`): `["Text 1", "Text 2"]`
- **Chat-style Messages**: Conversation arrays that get flattened to text

### ⚠️ Future Support
- **Token Arrays** (`[]int64`): `[1234, 5678, 9012]`
- **Token Array Batches** (`[][]int64`): `[[1234, 5678], [9012, 3456]]`

Expected output:

```json
{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "message": {
        "content": "The capital of France is Paris. Paris is not only the capital city but also the largest city in France, known for its cultural significance, historic landmarks like the Eiffel Tower and the Louvre Museum, and its influence in fashion, art, and cuisine.",
        "role": "assistant"
      }
    }
  ],
  "object": "chat.completion",
  "usage": { "completion_tokens": 58, "prompt_tokens": 13, "total_tokens": 71 }
}
```

You can also access an Anthropic model with native Anthropic messages endpoint:

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-7-sonnet@20250219",
    "messages": [
      {
        "role": "user",
        "content": "What is capital of France?"
      }
    ],
    "max_tokens": 100
  }' \
  $GATEWAY_URL/anthropic/v1/messages
```

## Troubleshooting

If you encounter issues:

1. Verify your GCP credentials are correct and active
2. Check pod status:
   ```shell
   kubectl get pods -n envoy-gateway-system
   ```
3. View controller logs:
   ```shell
   kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller
   ```
4. Common errors:
   - **401/403**: Invalid credentials or insufficient permissions
   - **404**: Model not found or not available in your GCP region
   - **429**: Rate limit exceeded
   - **400**: Invalid request format or input too large

### Embedding-Specific Issues

- **"No embedding returned from GCP"**: Check that the model supports embeddings and is available in your region
- **"Request too large"**: Reduce input size or use chunking for large texts
- **Token counting shows 0**: GCP VertexAI doesn't provide token counts for embeddings - this is expected

## Configuring More Models

To use more models, add more [AIGatewayRouteRule]s to the `gcp_vertex.yaml` file with the [model ID] in the `value` field. For example, to use [Claude 3 Sonnet]

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: envoy-ai-gateway-basic-gcp-gemini
  namespace: default
spec:
  schema:
    name: OpenAI
  parentRefs:
    - name: envoy-ai-gateway-basic
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gemini-2.5-flash-pro
      backendRefs:
        - name: envoy-ai-gateway-basic-gcp
```

[AIGatewayRouteRule]: ../../api/api.mdx#aigatewayrouterule
[model ID]: https://cloud.google.com/vertex-ai/generative-ai/docs/models
[Anthropic Claude]: https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude

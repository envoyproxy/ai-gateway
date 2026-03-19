---
id: aigatewayroute-inferencepool
title: AIGatewayRoute + InferencePool Guide
sidebar_position: 3
---

import CodeBlock from '@theme/CodeBlock';
import vars from '../../\_vars.json';
import Step4MistralResources from '!!raw-loader!./examples/step4-mistral-resources.yaml';
import Step5Testupstream from '!!raw-loader!./examples/step5-testupstream.yaml';
import Step6GatewayAigwroute from '!!raw-loader!./examples/step6-gateway-aigwroute.yaml';

# AIGatewayRoute + InferencePool Guide

This guide demonstrates how to use InferencePool with AIGatewayRoute for advanced AI-specific inference routing. This approach provides enhanced features like model-based routing, token rate limiting, and advanced observability.

## Prerequisites

Before starting, ensure you have:

1. **Kubernetes cluster** with Gateway API support
2. **Envoy AI Gateway** installed and configured

## Step 1: Install Gateway API Inference Extension

Install the Gateway API Inference Extension CRDs and controller:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/v1.0.1/manifests.yaml
```

After installing InferencePool CRD, enable InferencePool support in Envoy Gateway, restart the deployment, and wait for it to be ready:

<CodeBlock language="shell">
{`kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/${vars.aigwGitRef}/examples/inference-pool/config.yaml

kubectl rollout restart -n envoy-gateway-system deployment/envoy-gateway

kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available`}
</CodeBlock>

## Step 2: Ensure Envoy Gateway is configured for InferencePool

See [Envoy Gateway Installation Guide](../../getting-started/prerequisites.md#additional-features-rate-limiting-inferencepool-etc)

## Step 3: Deploy Inference Backends

Deploy sample inference backends and related resources:

```bash
# Deploy vLLM simulation backend
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v1.0.1/config/manifests/vllm/sim-deployment.yaml

# Deploy InferenceObjective
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/gateway-api-inference-extension/refs/tags/v1.0.1/config/manifests/inferenceobjective.yaml

# Deploy InferencePool resources
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v1.0.1/config/manifests/inferencepool-resources.yaml
```

> **Note**: These deployments create the `vllm-llama3-8b-instruct` InferencePool and related resources that are referenced in the AIGatewayRoute configuration below.

## Step 4: Create Custom InferencePool Resources

Create additional inference backends with custom EndpointPicker configuration:

<CodeBlock language="yaml">{Step4MistralResources}</CodeBlock>

```shell
kubectl apply -f examples/step4-mistral-resources.yaml
```

## Step 5: Create AIServiceBackend for Mixed Routing

Create an AIServiceBackend for traditional backend routing alongside InferencePool:

<CodeBlock language="yaml">{Step5Testupstream}</CodeBlock>

```shell
kubectl apply -f examples/step5-testupstream.yaml
```

## Step 6: Configure Gateway and AIGatewayRoute

Create a Gateway and AIGatewayRoute with multiple InferencePool backends:

<CodeBlock language="yaml">{Step6GatewayAigwroute}</CodeBlock>

```shell
kubectl apply -f examples/step6-gateway-aigwroute.yaml
```

## Step 7: Test the Configuration

Test different model routing scenarios:

```bash
# Get the Gateway external IP
GATEWAY_IP=$(kubectl get gateway inference-pool-with-aigwroute -o jsonpath='{.status.addresses[0].value}')
```

Test vLLM Llama model (routed via InferencePool):

```bash
curl -H "Content-Type: application/json" \
  -d '{
        "model": "meta-llama/Llama-3.1-8B-Instruct",
        "messages": [
            {
                "role": "user",
                "content": "Hi. Say this is a test"
            }
        ]
    }' \
  http://$GATEWAY_IP/v1/chat/completions
```

Test Mistral model (routed via InferencePool):

```bash
curl -H "Content-Type: application/json" \
  -d '{
        "model": "mistral:latest",
        "messages": [
            {
                "role": "user",
                "content": "Hi. Say this is a test"
            }
        ]
    }' \
  http://$GATEWAY_IP/v1/chat/completions
```

Test AIService backend (non-InferencePool):

```bash
curl -H "Content-Type: application/json" \
  -d '{
        "model": "some-cool-self-hosted-model",
        "messages": [
            {
                "role": "user",
                "content": "Hi. Say this is a test"
            }
        ]
    }' \
  http://$GATEWAY_IP/v1/chat/completions
```

## Advanced Features

### Model-Based Routing

AIGatewayRoute automatically extracts the model name from the request body and routes to the appropriate backend:

- **Automatic Extraction**: No need to manually set headers
- **Dynamic Routing**: Different models can use different InferencePools
- **Mixed Backends**: Combine InferencePool and AIServiceBackend in the same route based on model name by request Body.

### Token Rate Limiting

Configure token-based rate limiting for InferencePool backends:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: inference-pool-with-rate-limiting
spec:
  # ... other configuration ...
  llmRequestCosts:
    - metadataKey: llm_input_token
      type: InputToken
    - metadataKey: llm_output_token
      type: OutputToken
    - metadataKey: llm_total_token
      type: TotalToken
```

### Enhanced Observability

AIGatewayRoute provides rich metrics for InferencePool usage:

- **Model-specific metrics**: Track usage per model
- **Token consumption**: Monitor token usage and costs
- **Endpoint performance**: Detailed metrics per inference endpoint

## InferencePool Configuration Annotations

InferencePool supports configuration annotations to customize the external processor behavior:

### Processing Body Mode

Configure how the external processor handles request and response bodies:

```yaml
apiVersion: inference.networking.k8s.io/v1
kind: InferencePool
metadata:
  name: my-pool
  namespace: default
  annotations:
    # Configure processing body mode: "duplex" (default) or "buffered"
    aigateway.envoyproxy.io/processing-body-mode: "buffered"
spec:
  # ... other configuration ...
```

**Available values:**

- `"duplex"` (default): Uses `FULL_DUPLEX_STREAMED` mode for streaming processing
- `"buffered"`: Uses `BUFFERED` mode for buffered processing

### Allow Mode Override

Configure whether the external processor can override the processing mode:

```yaml
apiVersion: inference.networking.k8s.io/v1
kind: InferencePool
metadata:
  name: my-pool
  namespace: default
  annotations:
    # Configure allow mode override: "false" (default) or "true"
    aigateway.envoyproxy.io/allow-mode-override: "true"
spec:
  # ... other configuration ...
```

**Available values:**

- `"false"` (default): External processor cannot override the processing mode
- `"true"`: External processor can override the processing mode

### Combined Configuration

You can use both annotations together:

```yaml
apiVersion: inference.networking.k8s.io/v1
kind: InferencePool
metadata:
  name: my-pool
  namespace: default
  annotations:
    aigateway.envoyproxy.io/processing-body-mode: "buffered"
    aigateway.envoyproxy.io/allow-mode-override: "true"
spec:
  # ... other configuration ...
```

## Key Advantages over HTTPRoute

### Advanced OpenAI Routing

- Built-in OpenAI API schema validation
- Seamless integration with OpenAI SDKs
- Route multiple models in a single listener
- Mix InferencePool and traditional backends
- Automatic model extraction from request body

### AI-Specific Features

- Token-based rate limiting
- Model performance metrics
- Cost tracking and management
- Request/response transformation

## Next Steps

- Explore [token rate limiting](../traffic/usage-based-ratelimiting.md) in detail
- Review [observability best practices](../observability/) for AI workloads
- Configure [backend security policies](../security/upstream-auth.mdx) for your inference endpoints
- Learn more about the [Gateway API Inference Extension](https://gateway-api-inference-extension.sigs.k8s.io/) for advanced endpoint picker configurations

# InferencePool Integration

AI Gateway supports integration with [Gateway API Inference Extension](https://github.com/kubernetes-sigs/gateway-api-inference-extension) InferencePool resources, enabling advanced endpoint picking and load balancing for AI workloads.

## Overview

InferencePool integration allows you to:

- Reference InferencePool resources directly in AIGatewayRoute rules
- Leverage Endpoint Picker Providers (EPP) for intelligent endpoint selection
- Enable dynamic load balancing based on model availability, capacity, and performance
- Support multi-model deployments with automatic failover

## Prerequisites

- Envoy Gateway v1.4.0 or later with AI Gateway extension enabled
- Gateway API Inference Extension CRDs installed
- An Endpoint Picker Provider (EPP) service deployed

## Installation and Setup

### 1. Install Gateway API Inference Extension CRDs

Install the latest Inference Extension CRDs:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/latest/download/manifests.yaml
```

### 2. Deploy a Sample vLLM Deployment

Deploy a sample vLLM deployment with the proper protocol to work with the LLM Instance Gateway:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/main/config/manifests/vllm/cpu-deployment.yaml
```

### 3. Deploy InferenceModel

Deploy the sample InferenceModel which is configured to forward traffic to the food-review-1 LoRA adapter of the sample model server:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/main/config/manifests/inferencemodel.yaml
```

### 4. Deploy InferencePool and Endpoint Picker Extension

Deploy the InferencePool and Endpoint Picker Provider (EPP):

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/main/config/manifests/inferencepool-resources.yaml
```

## Basic Usage

### 1. Verify InferencePool Resources

After installation, verify that the InferencePool resources are created. The installation steps above will create an InferencePool named `vllm-llama3-8b-instruct`:

```bash
kubectl get inferencepool -A
kubectl get inferencemodel -A
```

You should see output similar to:

```text
NAME                      AGE
vllm-llama3-8b-instruct   1m
```

The created InferencePool has the following configuration:

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetPortNumber: 8000
  selector:
    app: vllm-llama3-8b-instruct
  extensionRef:
    name: vllm-llama3-8b-instruct-epp
```

### 2. Configure AIGatewayRoute

Reference the InferencePool in your AIGatewayRoute:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: llama-route
  namespace: default
spec:
  parentRefs:
  - name: ai-gateway
  schema:
    name: OpenAI
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /v1/chat/completions
    backendRefs:
    - group: inference.networking.x-k8s.io
      kind: InferencePool
      name: vllm-llama3-8b-instruct  # Use the InferencePool created by the installation
```

## How It Works

1. **Route Processing**: When AI Gateway processes the AIGatewayRoute, it detects InferencePool backend references
2. **Extension Policy Creation**: AI Gateway automatically creates EnvoyExtensionPolicy resources for the EPP services
3. **Cluster Configuration**: The extension server configures Envoy clusters with `ORIGINAL_DST` type for header-based routing
4. **Request Processing**: Incoming requests are processed by the EPP service, which adds the `x-gateway-destination-endpoint` header
5. **Endpoint Selection**: Envoy routes requests to the endpoint specified in the header

## Limitations

- Only one InferencePool backend is allowed per rule
- Cannot mix InferencePool and AIServiceBackend references in the same rule
- Cross-provider fallback is not supported with InferencePool backends
- EPP services must implement the external processing protocol

## Advanced Configuration

### Creating Custom InferencePools (Optional)

If you need to create additional InferencePools for your own deployments, you can create them following the same pattern as the installed one:

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: my-custom-inference-pool
  namespace: my-namespace
spec:
  targetPortNumber: 8000
  selector:
    app: my-custom-model-deployment
  extensionRef:
    name: my-custom-epp-service
```

### Multiple Rules with Different InferencePools

You can use different InferencePools for different routes. Here's an example using the installed InferencePool for specific model requests:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: multi-model-route
spec:
  parentRefs:
  - name: ai-gateway
  schema:
    name: OpenAI
  rules:
  - matches:
    - headers:
      - name: x-model
        value: llama3-8b
    backendRefs:
    - group: inference.networking.x-k8s.io
      kind: InferencePool
      name: vllm-llama3-8b-instruct
  - matches:
    - headers:
      - name: x-model
        value: text-embedding
    backendRefs:
    - name: embedding-service  # Fallback to regular AIServiceBackend for embeddings
```

### EPP Service Requirements

Your EPP service must:

1. Implement the Envoy external processing protocol
2. Process request headers (not body processing)
3. Add the `x-gateway-destination-endpoint` header with the target endpoint
4. Listen on the configured gRPC port (typically 9002)

## Troubleshooting

### Common Issues

1. **EnvoyExtensionPolicy not created**: Verify that the InferencePool resource exists and the EPP service is properly referenced
2. **Requests not routed**: Check that the EPP service is running and accessible
3. **Validation errors**: Ensure you're not mixing backend types or using multiple InferencePool backends per rule

For more examples and advanced configurations, see the [Gateway API Inference Extension repository](https://github.com/kubernetes-sigs/gateway-api-inference-extension).

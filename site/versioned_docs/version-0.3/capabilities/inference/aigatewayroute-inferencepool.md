---
id: aigatewayroute-inferencepool
title: AIGatewayRoute + InferencePool Guide
sidebar_position: 3
---

# AIGatewayRoute + InferencePool Guide

This guide demonstrates how to use InferencePool with AIGatewayRoute for advanced AI-specific inference routing. This approach provides enhanced features like model-based routing, token rate limiting, and advanced observability.

## Prerequisites

Before starting, ensure you have:

1. **Kubernetes cluster** with Gateway API support
2. **Envoy AI Gateway** installed and configured

## Step 1: Install Gateway API Inference Extension

Install the Gateway API Inference Extension CRDs and controller:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/v0.5.1/manifests.yaml
```

After installing InferencePool CRD, enabled InferencePool support in Envoy Gateway, restart the deployment, and wait for it to be ready:

```shell
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/release/v0.3/examples/inference-pool/config.yaml

kubectl rollout restart -n envoy-gateway-system deployment/envoy-gateway

kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

## Step 2: Deploy Inference Backends

Deploy sample inference backends and related resources:

```bash
# Deploy vLLM simulation backend
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v0.5.1/config/manifests/vllm/sim-deployment.yaml

# Deploy InferenceModel
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v0.5.1/config/manifests/inferencemodel.yaml

# Deploy InferencePool resources
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v0.5.1/config/manifests/inferencepool-resources.yaml
```

> **Note**: These deployments create the `vllm-llama3-8b-instruct` InferencePool and related resources that are referenced in the AIGatewayRoute configuration below.

## Step 3: Create EndpointPicker Resources

Create the base resources for the example, including additional inference backends:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: mistral-upstream
  namespace: default
spec:
  selector:
    app: mistral-upstream
  ports:
    - protocol: TCP
      port: 8080
      targetPort: 8080
  clusterIP: None  # Headless service for direct pod access
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mistral-upstream
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: mistral-upstream
  template:
    metadata:
      labels:
        app: mistral-upstream
    spec:
      containers:
        - name: testupstream
          image: docker.io/envoyproxy/ai-gateway-testupstream:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: TESTUPSTREAM_ID
              value: test
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 1
            periodSeconds: 1
---
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: mistral
  namespace: default
spec:
  targetPortNumber: 8080
  selector:
    app: mistral-upstream
  extensionRef:
    name: mistral-epp
---
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferenceModel
metadata:
  name: mistral
  namespace: default
spec:
  modelName: mistral:latest
  criticality: Critical
  poolRef:
    name: mistral
---
apiVersion: v1
kind: Service
metadata:
  name: mistral-epp
  namespace: default
spec:
  selector:
    app: mistral-epp
  ports:
    - protocol: TCP
      port: 9002
      targetPort: 9002
      appProtocol: http2
  type: ClusterIP
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mistral-epp
  namespace: default
  labels:
    app: mistral-epp
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mistral-epp
  template:
    metadata:
      labels:
        app: mistral-epp
    spec:
      terminationGracePeriodSeconds: 130
      containers:
        - name: epp
          image: registry.k8s.io/gateway-api-inference-extension/epp:v0.5.1
          imagePullPolicy: IfNotPresent
          args:
            - -poolName
            - "mistral"
            - "-poolNamespace"
            - "default"
            - -v
            - "4"
            - --zap-encoder
            - "json"
            - -grpcPort
            - "9002"
            - -grpcHealthPort
            - "9003"
          ports:
            - containerPort: 9002
            - containerPort: 9003
            - name: metrics
              containerPort: 9090
          livenessProbe:
            grpc:
              port: 9003
              service: inference-extension
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            grpc:
              port: 9003
              service: inference-extension
            initialDelaySeconds: 5
            periodSeconds: 10
EOF
```

## Step 4: Create AIServiceBackend for Mixed Routing

Create an AIServiceBackend for traditional backend routing alongside InferencePool:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: envoy-ai-gateway-basic-testupstream
    kind: Backend
    group: gateway.envoyproxy.io
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  endpoints:
    - fqdn:
        hostname: envoy-ai-gateway-basic-testupstream.default.svc.cluster.local
        port: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: envoy-ai-gateway-basic-testupstream
  template:
    metadata:
      labels:
        app: envoy-ai-gateway-basic-testupstream
    spec:
      containers:
        - name: testupstream
          image: docker.io/envoyproxy/ai-gateway-testupstream:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: TESTUPSTREAM_ID
              value: test
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 1
            periodSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  selector:
    app: envoy-ai-gateway-basic-testupstream
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: ClusterIP
EOF
```

## Step 5: Configure Gateway and AIGatewayRoute

Create a Gateway and AIGatewayRoute with multiple InferencePool backends:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: inference-pool-with-aigwroute
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: inference-pool-with-aigwroute
  namespace: default
spec:
  gatewayClassName: inference-pool-with-aigwroute
  listeners:
    - name: http
      protocol: HTTP
      port: 80
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: inference-pool-with-aigwroute
  namespace: default
spec:
  parentRefs:
    - name: inference-pool-with-aigwroute
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    # Route for vLLM Llama model via InferencePool
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: meta-llama/Llama-3.1-8B-Instruct
      backendRefs:
        - group: inference.networking.x-k8s.io
          kind: InferencePool
          name: vllm-llama3-8b-instruct
    # Route for Mistral model via InferencePool
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: mistral:latest
      backendRefs:
        - group: inference.networking.x-k8s.io
          kind: InferencePool
          name: mistral
    # Route for traditional backend (non-InferencePool)
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: some-cool-self-hosted-model
      backendRefs:
        - name: envoy-ai-gateway-basic-testupstream
EOF
```

## Step 6: Test the Configuration

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

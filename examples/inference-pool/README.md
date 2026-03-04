# InferencePool Example

This example demonstrates how to use AI Gateway with the InferencePool feature, which enables intelligent request routing across multiple inference endpoints with load balancing and health checking capabilities.

The setup includes **three distinct backends**:

- Two `InferencePool` resources for LLMs (`Llama-3.1-8B-Instruct` and `Mistral`)
- One standard `Backend` for non-InferencePool traffic

Routing is controlled by the `x-ai-eg-model` HTTP header.

## Files in This Directory

- **`envoy-gateway-values-addon.yaml`**: Envoy Gateway values addon for InferencePool support. Combine with `../../manifests/envoy-gateway-values.yaml`.
- **`base.yaml`**: Deploys all inference backends and supporting resources using the **standard approach documented in the official guide**. This includes:
  - A `mistral` backend with custom Endpoint Picker configuration
  - A standard fallback backend (`envoy-ai-gateway-basic-testupstream`) for non-InferencePool routing
- **`aigwroute.yaml`**: Example AIGatewayRoute that uses InferencePool as a backend.
- **`httproute.yaml`**: Example HTTPRoute for traditional HTTP routing to InferencePool endpoints.
- **`with-annotations.yaml`**: Advanced example showing InferencePool with Kubernetes annotations for fine-grained control.

## Quick Start

1. Install Envoy Gateway with InferencePool support:

   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f ../../manifests/envoy-gateway-values.yaml \
     -f envoy-gateway-values-addon.yaml
   ```

2. Deploy the example:

   ```bash
   kubectl apply -f base.yaml
   kubectl apply -f aigwroute.yaml
   ```

   > Note: The `aigwroute.yaml` file defines the InferencePool and routing logic, but does not deploy the actual inference backend (e.g., the vLLM server for Llama-3.1-8B-Instruct).
   > You must deploy the backend separately by following [Step 3: Deploy Inference Backends](https://aigateway.envoyproxy.io/docs/capabilities/inference/aigatewayroute-inferencepool#step-3-deploy-inference-backends)

3. Test the setup:

You can access the gateway in two ways, depending on your environment.

✅ Option A: Using External IP (e.g., cloud LoadBalancer, MetalLB)
If your cluster assigns an external address to the Gateway:

```bash
GATEWAY_HOST=$(kubectl get gateway/inference-pool-with-aigwroute -n default -o jsonpath='{.status.addresses[0].value}')
echo "Gateway available at: http://${GATEWAY_HOST}"
```

Then send a request:

```bash
curl -X POST "http://${GATEWAY_HOST}/v1/chat/completions" \
  -H "x-ai-eg-model: meta-llama/Llama-3.1-8B-Instruct" \
  -H "Authorization: sk-abcdefghijklmnopqrstuvwxyz" \
  -H "Content-Type: application/json" \
  -d '{"model": "meta-llama/Llama-3.1-8B-Instruct", "messages": [{"role": "user", "content": "Hello!"}]}'
```

✅ Option B: Using kubectl port-forward (ideal for local clusters like Minikube/Kind)
In one terminal, forward the gateway service:

```bash
kubectl port-forward svc/envoy-default-inference-pool-with-aigwroute-d416582c 8080:80 -n envoy-gateway-system
```

In another terminal, send requests to localhost:8080:

```bash
# Route to Llama (InferencePool)
curl -X POST "http://localhost:8080/v1/chat/completions" \
  -H "x-ai-eg-model: meta-llama/Llama-3.1-8B-Instruct" \
  -H "Authorization: sk-abcdefghijklmnopqrstuvwxyz" \
  -H "Content-Type: application/json" \
  -d '{"model": "meta-llama/Llama-3.1-8B-Instruct", "messages": [{"role": "user", "content": "Hello!"}]}'

# Route to Mistral (InferencePool)
curl -X POST "http://localhost:8080/v1/chat/completions" \
  -H "x-ai-eg-model: mistral:latest" \
  -H "Content-Type: application/json" \
  -d '{"model": "mistral:latest", "messages": [{"role": "user", "content": "Hello!"}]}'

# Route to fallback backend (Standard Backend)
curl -X POST "http://localhost:8080/v1/chat/completions" \
  -H "x-ai-eg-model: some-cool-self-hosted-model" \
  -H "Content-Type: application/json" \
  -d '{"model": "some-cool-self-hosted-model", "messages": [{"role": "user", "content": "Hello!"}]}'
```

### Combining with Other Features

You can easily combine InferencePool with other features using multiple `-f` flags:

```bash
# InferencePool + rate limiting
helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-gateway-system \
  --create-namespace \
  -f ../basic/envoy-gateway-values.yaml \
  -f ../token_ratelimit/envoy-gateway-values-addon.yaml \
  -f envoy-gateway-values-addon.yaml
```

For detailed documentation, see the [AI Gateway documentation](https://gateway.envoyproxy.io/ai-gateway/).

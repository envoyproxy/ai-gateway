# InferencePool Example

This example demonstrates how to use AI Gateway with the InferencePool feature, which enables intelligent request routing across multiple inference endpoints with load balancing and health checking capabilities.

## Files in This Directory

- **`envoy-gateway-values-addon.yaml`**: Envoy Gateway values addon for InferencePool support. Combine with `../../manifests/envoy-gateway-values.yaml`.
- **`base.yaml`**: Complete example that includes Gateway, AIServiceBackend, InferencePool CRDs, and a sample application deployment.
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
   ```

3. Test the setup:

   ```bash
   GATEWAY_HOST=$(kubectl get gateway/ai-gateway -o jsonpath='{.status.addresses[0].value}')
   curl -X POST "http://${GATEWAY_HOST}/v1/chat/completions" \
     -H "Content-Type: application/json" \
     -d '{"model": "gpt-3.5-turbo", "messages": [{"role": "user", "content": "Hello!"}]}'
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

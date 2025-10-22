# Token based ratelimiting

This example demonstrates how to use the token rate limit feature of the AI Gateway.
This utilizes the Global Rate Limit API of Envoy Gateway combined with the
AI Gateway's `llmRequestCosts` configuration to capture the consumed tokens
of each request.

## Files in This Directory

- **`envoy-gateway-values-addon.yaml`**: Envoy Gateway values addon for rate limiting. Combine with `../../manifests/envoy-gateway-values.yaml`.
- **`redis.yaml`**: Redis deployment required for rate limiting. Deploy this before enabling rate limiting in Envoy Gateway.
- **`token_ratelimit.yaml`**: Example AIGatewayRoute configuration that demonstrates token-based rate limiting.

## Quick Start

1. Install Envoy Gateway with base configuration + rate limiting addon:

   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f ../../manifests/envoy-gateway-values.yaml \
     -f envoy-gateway-values-addon.yaml
   ```

2. Deploy Redis:

   ```bash
   kubectl apply -f redis.yaml
   ```

3. Apply the token rate limit example:
   ```bash
   kubectl apply -f token_ratelimit.yaml
   ```

### Combining with Other Features

You can easily combine rate limiting with other features using multiple `-f` flags:

```bash
# Rate limiting + InferencePool support
helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-gateway-system \
  --create-namespace \
  -f ../basic/envoy-gateway-values.yaml \
  -f envoy-gateway-values-addon.yaml \
  -f ../inference-pool/envoy-gateway-values-addon.yaml
```

For detailed documentation, see the [usage-based rate limiting guide](https://gateway.envoyproxy.io/ai-gateway/docs/capabilities/traffic/usage-based-ratelimiting).

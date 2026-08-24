# Token based ratelimiting

This example demonstrates how to use the token rate limit feature of the AI Gateway.
This utilizes the Global Rate Limit API of Envoy Gateway combined with the
AI Gateway's `llmRequestCosts` configuration to capture the consumed tokens
of each request.

## Files in This Directory

- **`envoy-gateway-values-addon.yaml`** and **`redis.yaml`**: Redis rate-limit backend and matching Envoy Gateway values addon.
- **`envoy-gateway-values-valkey-addon.yaml`** and **`valkey.yaml`**: Valkey rate-limit backend and matching values addon. The example pins `valkey/valkey:8.1.8-alpine`.
- **`token_ratelimit.yaml`**: Example AIGatewayRoute configuration that demonstrates token-based rate limiting.

## Quick Start

1. Choose a Redis-protocol rate-limit backend and install Envoy Gateway with its matching addon.

   Redis:

   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f ../../manifests/envoy-gateway-values.yaml \
     -f envoy-gateway-values-addon.yaml
   ```

   Valkey:

   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f ../../manifests/envoy-gateway-values.yaml \
     -f envoy-gateway-values-valkey-addon.yaml
   ```

2. Deploy the backend selected in the previous step:

   Redis:

   ```bash
   kubectl apply -f redis.yaml
   ```

   Valkey (pinned to `valkey/valkey:8.1.8-alpine`):

   ```bash
   kubectl apply -f valkey.yaml
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

Valkey compatibility for this existing Redis-protocol rate-limit path is tracked in [#2522](https://github.com/envoyproxy/ai-gateway/issues/2522).

# Response Caching

This example demonstrates how to use the response caching feature of the AI Gateway.
Response caching stores LLM responses in Redis and returns cached responses for identical requests,
reducing latency and costs for repeated queries.

## Features

- **Shared Cache**: Cache is shared across all ext_proc instances via Redis
- **HTTP Cache-Control Support**: Respects standard HTTP Cache-Control headers for opt-in/opt-out
- **Configurable TTL**: Set custom time-to-live for cached responses
- **Per-Route Configuration**: Enable/disable caching per AIGatewayRoute

## Files in This Directory

- **`redis.yaml`**: Redis deployment for caching. Deploy this before enabling response caching.
- **`response-cache.yaml`**: Example AIGatewayRoute configuration with response caching enabled.

## Quick Start

1. Deploy Redis:

   ```bash
   kubectl apply -f redis.yaml
   ```

2. Install Envoy Gateway with AI Gateway support:

   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f ../../manifests/envoy-gateway-values.yaml
   ```

3. Apply the response cache example:

   ```bash
   kubectl apply -f response-cache.yaml
   ```

## Configuration

### AIGatewayRoute Configuration

Add `responseCache` to your AIGatewayRoute spec:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: my-route
spec:
  responseCache:
    enabled: true
    ttl: 1h # Cache TTL (default: 1h)
    respectCacheControl: true # Honor HTTP Cache-Control headers (default: true)
  # ... rest of config
```

### ExtProc Redis Configuration

The ext_proc needs Redis connection flags. When using the AI Gateway CLI or Helm chart,
configure Redis via:

```bash
# CLI flags
aigw run --redisAddr=redis.redis-system.svc.cluster.local:6379

# Or with TLS and password
aigw run --redisAddr=redis:6379 --redisTLS=true --redisPassword=secret
```

## HTTP Cache-Control Headers

When `respectCacheControl: true` (default), the gateway honors standard HTTP Cache-Control headers:

### Request Headers (Client Control)

| Header                    | Behavior                                          |
| ------------------------- | ------------------------------------------------- |
| `Cache-Control: no-cache` | Bypass cache lookup, but still cache the response |
| `Cache-Control: no-store` | Bypass cache entirely (no lookup, no store)       |

### Response Headers (Backend Control)

| Header                     | Behavior                                           |
| -------------------------- | -------------------------------------------------- |
| `Cache-Control: no-store`  | Do not cache this response                         |
| `Cache-Control: no-cache`  | Do not cache this response                         |
| `Cache-Control: private`   | Do not cache (shared cache should not store)       |
| `Cache-Control: max-age=N` | Use N seconds as TTL instead of configured default |

### Examples

```bash
# Normal request - uses cache
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Hello"}]}'

# Force fresh response (bypass cache lookup)
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Cache-Control: no-cache" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Hello"}]}'

# Disable caching entirely for this request
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Cache-Control: no-store" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

## Cache Response Headers

The gateway adds a header to indicate cache status:

- `x-aigw-cache: hit` - Response served from cache
- `x-aigw-cache: miss` - Response from backend (may be cached for future requests)

## Best Practices

1. **Use for Deterministic Queries**: Caching works best for queries that should return the same response
2. **Set Appropriate TTL**: Balance freshness vs. cost savings based on your use case
3. **Monitor Cache Hit Rate**: Track the `x-aigw-cache` header to measure effectiveness
4. **Consider Temperature**: Requests with `temperature: 0` are good candidates for caching

## Limitations

- Cache key is based on the entire request body hash
- Streaming responses are buffered and cached as complete responses
- Cache hits return non-streaming responses even if the original request was streaming

## Related Examples

- [Basic Provider Setup](../basic/)
- [Token Rate Limiting](../token_ratelimit/)
- [Provider Fallback](../provider_fallback/)

For detailed documentation, see the [response caching guide](https://gateway.envoyproxy.io/ai-gateway/docs/capabilities/traffic/response-caching).

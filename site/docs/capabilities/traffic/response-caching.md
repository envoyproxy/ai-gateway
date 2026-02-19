---
id: response-caching
title: Response Caching
sidebar_position: 6
---

This guide explains how to configure response caching in AI Gateway to reduce latency and costs for repeated LLM requests.

## Overview

Response caching stores LLM responses in Redis and returns cached responses for identical requests. This is particularly useful for:

- **Cost Reduction**: Avoid paying for repeated identical queries
- **Latency Improvement**: Serve cached responses instantly without backend round-trips
- **Rate Limit Protection**: Reduce load on backend providers

Key features:

- Shared cache across all ext_proc instances via Redis
- HTTP Cache-Control header support for client opt-in/opt-out
- Configurable TTL per route
- Cache key based on request body hash

## Prerequisites

:::tip Prerequisites

Response caching requires:

1. **Redis Deployment**: A Redis instance must be running to store cached responses. See the [redis.yaml example](https://github.com/envoyproxy/ai-gateway/blob/main/examples/response-cache/redis.yaml) for a simple deployment.

2. **ExtProc Configuration**: The ext_proc must be configured with Redis connection flags (`--redisAddr`).

:::

## Configuration

### 1. Deploy Redis

Deploy a Redis instance in your cluster:

```bash
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/response-cache/redis.yaml
```

### 2. Configure ExtProc Redis Connection

Configure the Redis connection for the AI Gateway. There are two approaches:

#### Option A: Using a Secret (Recommended for Production)

Store Redis credentials securely in a Kubernetes Secret:

```bash
kubectl create secret generic redis-cache-secret \
  --from-literal=addr=redis.redis-system.svc.cluster.local:6379 \
  --from-literal=password=your-redis-password
```

Then reference the secret in your Helm values:

```yaml
extProc:
  redis:
    secretRef:
      name: redis-cache-secret
    tls: false # Set to true if Redis requires TLS
```

#### Option B: Direct Values (Development/Testing)

For simple setups without authentication, configure Redis directly:

```yaml
extProc:
  redis:
    addr: "redis.redis-system.svc.cluster.local:6379"
    tls: false
```

#### CLI Usage

When using the AI Gateway CLI directly:

```bash
# Basic usage
aigw run --redisAddr=redis.redis-system.svc.cluster.local:6379

# With TLS and authentication
aigw run --redisAddr=redis:6379 --redisTLS=true --redisPassword=your-password
```

### 3. Enable Caching in AIGatewayRoute

Add the `responseCache` configuration to your AIGatewayRoute:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: my-cached-route
spec:
  responseCache:
    enabled: true
    ttl: 1h # Cache TTL (default: 1h)
    respectCacheControl: true # Honor HTTP Cache-Control headers (default: true)
  parentRefs:
    - name: my-gateway
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gpt-4o-mini
      backendRefs:
        - name: openai-backend
```

### Configuration Options

| Field                 | Type     | Default | Description                            |
| --------------------- | -------- | ------- | -------------------------------------- |
| `enabled`             | boolean  | `false` | Enable response caching for this route |
| `ttl`                 | duration | `1h`    | Time-to-live for cached responses      |
| `respectCacheControl` | boolean  | `true`  | Honor HTTP Cache-Control headers       |

## HTTP Cache-Control Support

When `respectCacheControl: true` (default), the gateway honors standard HTTP Cache-Control headers from both requests and responses.

### Request Headers (Client Control)

Clients can control caching behavior per-request:

| Header                    | Behavior                                          |
| ------------------------- | ------------------------------------------------- |
| `Cache-Control: no-cache` | Bypass cache lookup, but still cache the response |
| `Cache-Control: no-store` | Bypass cache entirely (no lookup, no store)       |

### Response Headers (Backend Control)

Backend responses can control whether they should be cached:

| Header                     | Behavior                                           |
| -------------------------- | -------------------------------------------------- |
| `Cache-Control: no-store`  | Do not cache this response                         |
| `Cache-Control: no-cache`  | Do not cache this response                         |
| `Cache-Control: private`   | Do not cache (shared cache should not store)       |
| `Cache-Control: max-age=N` | Use N seconds as TTL instead of configured default |

### Disabling Cache-Control Support

If you want caching behavior to be determined solely by the route configuration (ignoring client headers), set `respectCacheControl: false`:

```yaml
spec:
  responseCache:
    enabled: true
    ttl: 30m
    respectCacheControl: false # Ignore Cache-Control headers
```

## Making Requests

### Normal Request (Uses Cache)

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "What is 2+2?"}]
  }'
```

### Force Fresh Response (Bypass Cache Lookup)

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Cache-Control: no-cache" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "What is 2+2?"}]
  }'
```

### Disable Caching for Request

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Cache-Control: no-store" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "What is 2+2?"}]
  }'
```

## Cache Status Headers

The gateway adds a response header to indicate cache status:

| Header         | Value  | Meaning                    |
| -------------- | ------ | -------------------------- |
| `x-aigw-cache` | `hit`  | Response served from cache |
| `x-aigw-cache` | `miss` | Response from backend      |

Use this header to monitor cache effectiveness.

## Best Practices

### Good Candidates for Caching

1. **Deterministic queries**: Questions with factual answers that don't change
2. **Low temperature requests**: Requests with `temperature: 0` produce consistent outputs
3. **Reference lookups**: Queries for definitions, documentation, or static information
4. **Repeated system prompts**: Common system prompts across multiple requests

### Poor Candidates for Caching

1. **Creative content**: Requests where variety is desired
2. **Time-sensitive queries**: Questions about current events or real-time data
3. **Personalized responses**: Queries that should vary per user
4. **High temperature requests**: Requests designed for randomness

### TTL Guidelines

| Use Case                  | Recommended TTL |
| ------------------------- | --------------- |
| Static reference data     | 24h - 7d        |
| General knowledge queries | 1h - 24h        |
| Semi-dynamic content      | 5m - 1h         |
| Frequently changing data  | Disable caching |

## Limitations

1. **Cache Key**: The cache key is a hash of the entire request body. Different formatting of the same logical request will result in cache misses.

2. **Streaming Responses**: Streaming responses are buffered completely before caching. Cache hits return non-streaming responses even if the original request specified streaming.

3. **No Partial Matching**: The cache does not support semantic similarity matching—only exact request body matches.

4. **Shared Cache**: The cache is shared across all users. Use `Cache-Control: private` or disable caching for user-specific responses.

## Monitoring

Monitor cache effectiveness by tracking:

- **Cache hit rate**: Percentage of requests served from cache (via `x-aigw-cache` header)
- **Redis memory usage**: Monitor Redis memory to ensure adequate capacity
- **Response latency**: Compare latency for cache hits vs. misses

## Example

See the complete example in the [examples/response-cache](https://github.com/envoyproxy/ai-gateway/tree/main/examples/response-cache) directory.

---
id: provider-fallback
title: Provider Fallback
sidebar_position: 6
---

# Provider Fallback

Envoy AI Gateway supports provider fallback to ensure high availability and reliability for AI/LLM workloads. With fallback, you can configure multiple upstream providers for a single route, so that if the primary provider fails (due to network errors, 5xx responses, or other health check failures), traffic is automatically routed to a healthy fallback provider.

## When to Use Fallback

- To ensure uninterrupted service when a primary AI/LLM provider is unavailable.
- To provide redundancy across multiple cloud or on-premise model providers.
- To implement active-active or active-passive failover strategies for critical AI workloads.

## How Fallback Works

- **Primary and Fallback Backends:** You can specify a prioritized list of backends in your `AIGatewayRoute` using `backendRefs`. The first backend is treated as primary, and subsequent backends are considered fallbacks.
- **Retry Policy:** Fallback is triggered based on retry policies, which can be configured using the [`BackendTrafficPolicy`](https://gateway.envoyproxy.io/contributions/design/backend-traffic-policy/) API.
- **Automatic Failover:** When the primary backend becomes unhealthy, Envoy AI Gateway automatically shifts traffic to the next healthy fallback backend.

## Example

Below is an example configuration that demonstrates provider fallback from a failing upstream to AWS Bedrock:

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: provider-fallback
  namespace: default
spec:
  parentRefs:
    - name: provider-fallback
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: us.meta.llama3-2-1b-instruct-v1:0
      backendRefs:
        - name: provider-fallback-always-failing-upstream # Primary backend (expected to fail)
          priority: 0
        - name: provider-fallback-aws # Fallback backend
          priority: 1
```

The corresponding `Backend` resources:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: provider-fallback-always-failing-upstream
  namespace: default
spec:
  endpoints:
    - fqdn:
        hostname: provider-fallback-always-failing-upstream.default.svc.cluster.local
        port: 443
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: provider-fallback-aws
  namespace: default
spec:
  endpoints:
    - fqdn:
        hostname: bedrock-runtime.us-east-1.amazonaws.com
        port: 443
```

## Configuring Fallback Behavior

Attach a `BackendTrafficPolicy` to the generated `HTTPRoute` to control retry behavior:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: provider-fallback
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: provider-fallback # HTTPRoute is created with the same name as AIGatewayRoute
  retry:
    # This ensures that only one attempt is made per priority.
    # For example, if the primary backend fails, it will not retry on the same backend.
    numAttemptsPerPriority: 1
    numRetries: 5
    perRetry:
      backOff:
        baseInterval: 100ms
        maxInterval: 10s
      timeout: 30s
    retryOn:
      httpStatusCodes:
        - 500
      triggers:
        - connect-failure
        - retriable-status-codes
```

## Dynamic Per-Request Fallback Ordering

In multi-tenant deployments, different tenants often need different fallback orders over the
same set of providers. Instead of creating a route per tenant, the fallback **order** can be
supplied per request while the route keeps owning the candidate set and everything
security-bearing (schema, credentials, TLS).

:::note
Dynamic fallback ordering requires the data plane to run Envoy 1.39 or later.
:::

Opt a route in with the `aigateway.envoyproxy.io/dynamic-fallback: "true"` annotation, and give
each backend a published name with `alias`:

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: llm-route
  annotations:
    aigateway.envoyproxy.io/dynamic-fallback: "true"
spec:
  rules:
    - matches:
        - headers: [{ type: Exact, name: x-ai-eg-model, value: gpt-5 }]
      backendRefs:
        - name: azure-openai-eastus
          alias: azure # the name callers use
          priority: 0 # default order when no chain is supplied
        - name: openai-prod
          alias: openai
          priority: 1
```

### The `x-ai-eg-fallback-chain` request header

The per-request order is carried by the `x-ai-eg-fallback-chain` request header — a
comma-separated list of published names (aliases, or backend resource names when no alias is
set), most-preferred first:

```
x-ai-eg-fallback-chain: openai,azure
```

This header is a stable, user-visible contract with the following properties:

- **Set by a trusted party.** Typically an ext_authz service or SecurityPolicy JWT
  `claimToHeaders` attaches it based on the tenant; it should not be accepted from untrusted
  clients directly unless that is intended.
- **Validated against the rule's candidates.** Entries that are not published names of the
  matched rule are ignored with a log; a chain can never reach a backend the route author did
  not attach.
- **Degrades safely.** A missing, empty, or fully-invalid chain falls back to the declared
  `priority` order — never an error.
- **Consumed by the gateway.** The header is removed before the request leaves the router and
  is never forwarded to providers.

Callers can discover the orderable names via the `fallback_candidates` extension field on the
OpenAI-compatible `/v1/models` endpoint:

```json
{ "id": "gpt-5", "object": "model", "fallback_candidates": ["azure", "openai"] }
```

### Per-model fallback on one provider

Two refs to the same backend, distinguished by `alias` and `modelNameOverride`, make models
orderable — for example falling back from a stronger to a cheaper model on the same provider:

```yaml
backendRefs:
  - name: anthropic
    alias: opus
    modelNameOverride: claude-opus-4
    priority: 0
  - name: anthropic
    alias: sonnet
    modelNameOverride: claude-sonnet-4
    priority: 1
```

```
x-ai-eg-fallback-chain: opus,sonnet
```

Rules using weighted traffic splitting (multiple backendRefs sharing a priority) keep their
existing static behavior and are not currently orderable per request.

## References

- [Provider Fallback Example](https://github.com/envoyproxy/ai-gateway/tree/main/examples/provider_fallback)
- [`BackendTrafficPolicy` API Design](https://gateway.envoyproxy.io/contributions/design/backend-traffic-policy/)

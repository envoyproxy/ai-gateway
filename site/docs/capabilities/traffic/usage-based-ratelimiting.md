---
id: usage-based-ratelimiting
title: Usage-based Rate Limiting
sidebar_position: 5
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide focuses on AI Gateway's specific capabilities for token-based rate limiting in LLM requests. For general rate limiting concepts and configurations, refer to [Envoy Gateway's Rate Limiting documentation](https://gateway.envoyproxy.io/docs/tasks/traffic/global-rate-limit/).

:::info Quota Policy vs. Rate Limiting
AI Gateway also provides [Quota Policy](./quota-policy.md) for managing **total consumption budgets** (for example, 100,000 tokens per hour). Use QuotaPolicy when you need to cap cumulative token spend, and usage-based rate limiting (this page) when you need to control **request velocity**.
:::

## Overview

AI Gateway leverages Envoy Gateway's Global Rate Limit API to provide token-based rate limiting for LLM requests. Key features include:

- Token usage tracking based on model and user identifiers
- Configuration for tracking input, output, and total token metadata from LLM responses
- Model-specific rate limiting using AI Gateway headers (`x-ai-eg-model`) which is inserted by the AI Gateway filter with the model name extracted from the request body.
- Support for custom token cost calculations using CEL expressions

## Token Usage Behavior

AI Gateway has specific behavior for token tracking and rate limiting:

1. **Token Extraction**: AI Gateway automatically extracts token usage from LLM responses that follow the OpenAI schema format. The token counts are stored in the metadata specified in your `llmRequestCosts` configuration.

2. **Rate Limit Timing**: The check for whether the total count has reached the limit happens during each request. When a request is received:
   - AI Gateway checks if processing this request would exceed the configured token limit
   - If the limit would be exceeded, the request is rejected with a 429 status code
   - If within the limit, the request is processed and its token usage is counted towards the total

3. **Token Types**:
   - `InputToken`: Counts tokens in the request prompt
   - `CachedInputToken`: Counts _cached_ input tokens in the request prompt
   - `OutputToken`: Counts tokens in the model's response
   - `TotalToken`: Combines both input and output tokens
   - `CEL`: Allows custom token calculations using CEL expressions

4. **Multiple Rate Limits**: You can configure multiple rate limit rules for the same user-model combination. For example:
   - Limit total tokens per hour
   - Separate limits for input and output tokens
   - Custom limits using CEL expressions

5. **Per Route Custom Token Calculation**: The `llmRequestCosts` defined in your `AIGatewayRoute` spec are scoped exclusively to that specific route. This means multiple `AIGatewayRoute` resources can define the exact same metadata keys, but the gateway will independently apply the correct calculations based on the route's name and namespace.

To map the request to the correct calculation, the AI Gateway parses the route from the Envoy Gateway Metadata. If that metadata is not present, it falls back to parsing the route name from the route configuration, assuming Envoy Gateway has generated the name in the following format: `httproute/<namespace>/<name>/rule/<index>`.

:::note
For model providers with OpenAI schema transformations (like AWS Bedrock), AI Gateway automatically captures token usage through its request/response transformer. This enables consistent token tracking and rate limiting across different AI services using a unified OpenAI-compatible format.
:::

## Configuration

:::tip Prerequisites

Rate limiting requires two components to be configured:

1. **Redis Deployment**: A Redis instance must be running to store rate limit data. See the [redis.yaml example](https://github.com/envoyproxy/ai-gateway/blob/main/examples/token_ratelimit/redis.yaml) for a simple deployment.

2. **Envoy Gateway Configuration**: Envoy Gateway must be configured at installation time to enable rate limiting and point to your Redis instance. See [Envoy Gateway Installation Guide](../../getting-started/prerequisites.md#additional-features-rate-limiting-inferencepool-etc)

:::

### 1. Configure Token Tracking

AI Gateway automatically tracks token usage for each request. Configure which token counts you want to track in your `AIGatewayRoute`:

```yaml
spec:
  llmRequestCosts:
    - metadataKey: llm_input_token
      type: InputToken # Counts tokens in the request
    - metadataKey: llm_cached_input_token
      type: CachedInputToken # Counts cached input tokens in the request prompt
    - metadataKey: llm_output_token
      type: OutputToken # Counts tokens in the response
    - metadataKey: llm_total_token
      type: TotalToken # Tracks combined usage
```

For advanced token calculations specific to your use case:

```yaml
spec:
  llmRequestCosts:
    - metadataKey: custom_cost
      type: CEL
      cel: "(input_tokens - cached_input_tokens) + (cached_input_tokens * 0.1) + output_tokens * 1.5" # Example: Weight cached tokens less and weight output tokens more heavily
```

LLMRequestCosts can be defined on a per-route level.

### 2. Configure Rate Limits

AI Gateway uses Envoy Gateway's Global Rate Limit API to configure rate limits. Rate limits should be defined using a combination of user and model identifiers to properly control costs at the model level. Configure this using a `BackendTrafficPolicy`:

#### Example: Cost-Based Model Rate Limiting

The following example demonstrates a common use case where different models have different token limits based on their costs. This is useful when:

- You want to limit expensive models (like GPT-4) more strictly than cheaper ones
- You need to implement different quotas for different tiers of service
- You want to prevent cost overruns while still allowing flexibility with cheaper models

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: model-specific-token-limit-policy
  namespace: default
spec:
  targetRefs:
    - name: envoy-ai-gateway-token-ratelimit
      kind: Gateway
      group: gateway.networking.k8s.io
  rateLimit:
    type: Global
    global:
      rules:
        # Rate limit rule for GPT-4: 1000 total tokens per hour per user
        # Stricter limit due to higher cost per token
        - clientSelectors:
            - headers:
                - name: x-tenant-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-4
          limit:
            requests: 1000 # 1000 total tokens per hour
            unit: Hour
          cost:
            request:
              from: Number
              number: 0 # Set to 0 so only token usage counts
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token # Uses total tokens from the responses
        # Rate limit rule for GPT-3.5: 5000 total tokens per hour per user
        # Higher limit since the model is more cost-effective
        - clientSelectors:
            - headers:
                - name: x-tenant-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-3.5-turbo
          limit:
            requests: 5000 # 5000 total tokens per hour (higher limit for less expensive model)
            unit: Hour
          cost:
            request:
              from: Number
              number: 0 # Set to 0 so only token usage counts
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token # Uses total tokens from the response
```

:::warning
When configuring rate limits:

1. Always set the request cost number to 0 to ensure only token usage counts towards the limit
2. Set appropriate limits for different models based on their costs and capabilities
3. Ensure both user and model identifiers are used in rate limiting rules
   :::

## Dynamic Per-Request Limits

The rate limits above use a static limit value defined in the `BackendTrafficPolicy` (for example `requests: 1000`). AI Gateway can instead drive a **dynamic** limit value that is decided at request time by a preceding Envoy filter — typically an `ext_authz` auth server. This is useful when the limit is per-tenant and looked up from an external system rather than known ahead of time, so a single policy can apply a different quota to each tenant without enumerating them in configuration.

Keep the distinction with token cost clear:

- **Limit** is a request-time gate. It is the maximum (`requests_per_unit`) for the current request and must be known _before_ the request is forwarded, so it is read on the **request** path.
- **Cost** (covered above) is a response-time charge. It is the amount the request consumes from that limit and is read on the **response** path from token usage.

### How it works

1. A preceding filter (typically `ext_authz` / the auth server) emits the per-request limit as a string `"<count>/<unit>"` — for example `"100000/HOUR"`, where unit is one of `SECOND`, `MINUTE`, `HOUR`, or `DAY` (case-insensitive) — into its **dynamic metadata**. Dynamic metadata is used deliberately rather than a request header: it can only be set by Envoy filters, not by downstream clients, so the limit cannot be spoofed.

2. You configure `GatewayConfig.Spec.GlobalRateLimits`, a gateway-wide list that maps a `metadataKey` to the source that supplies the limit value.

3. On the request path, AI Gateway's external processor reads the source value, parses `"<count>/<unit>"`, and writes it into the `io.envoy.ai_gateway` dynamic metadata namespace under `metadataKey` as the struct `{ requests_per_unit, unit }` that Envoy's rate-limit override expects. If the source value is absent or malformed, the key is simply not emitted and the static default in the `BackendTrafficPolicy` applies.

4. The consumer is a `BackendTrafficPolicy` whose rule reads that key via `limit.fromMetadata`.

The producer (`GlobalRateLimits`) is gateway-wide: every configured `metadataKey` is emitted on every request. Per-route differences are expressed on the **consumer** side — each route's `BackendTrafficPolicy` chooses which `metadataKey` to read — not on the `AIGatewayRoute`. This is why each `metadataKey` must be unique across the gateway. It mirrors how token cost is consumed per-route via `cost.response.metadata`.

:::warning Dynamic limits fail open to the static default
Whenever the dynamic value is unavailable — the source metadata is absent or malformed for a request, or AI Gateway briefly cannot resolve the forwarding configuration (for example, right after adding `GlobalRateLimits`, until the gateway is re-translated) — the request falls back to the static `requests`/`unit` in the `BackendTrafficPolicy`. Because the fallback direction is permissive, keep that static default **conservative**: treat it as the ceiling that applies when the dynamic limit cannot be read, not as a placeholder.
:::

### 1. Emit the limit from a preceding filter

Your `ext_authz` auth server decides the limit for the request (for example, by looking up the tenant) and returns it in its dynamic metadata. For `ext_authz` the metadata namespace is typically `envoy.filters.http.ext_authz`. The value for our example tenant would be the string:

```text
100000/HOUR
```

### 2. Map the source into `io.envoy.ai_gateway` metadata

Configure `GlobalRateLimits` on the `GatewayConfig` referenced by your Gateway. Each entry names the destination `metadataKey` and the `fromMetadata` source (the namespace and key the preceding filter wrote to):

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: GatewayConfig
metadata:
  name: envoy-ai-gateway
  namespace: default
spec:
  globalRateLimits:
    - metadataKey: tenant_request_limit
      source:
        fromMetadata:
          namespace: envoy.filters.http.ext_authz # where ext_authz wrote the value
          key: rate_limit # the field name ext_authz set
```

With this in place, when `ext_authz` sets `rate_limit: "100000/HOUR"`, AI Gateway emits the following into the `io.envoy.ai_gateway` dynamic metadata on the request path:

```yaml
tenant_request_limit:
  requests_per_unit: 100000
  unit: HOUR
```

### 3. Consume the dynamic limit in a `BackendTrafficPolicy`

On the route whose limit should be dynamic, configure a `BackendTrafficPolicy` rule that reads the limit from metadata via `limit.fromMetadata`. The static `requests`/`unit` remain as the default that applies when the metadata key is absent (for example, if `ext_authz` did not set a value for this request):

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: tenant-dynamic-limit-policy
  namespace: default
spec:
  targetRefs:
    - name: envoy-ai-gateway
      kind: Gateway
      group: gateway.networking.k8s.io
  rateLimit:
    type: Global
    global:
      rules:
        - clientSelectors:
            - headers:
                - name: x-tenant-id
                  type: Distinct
          limit:
            requests: 1000 # default used when the metadata key is absent
            unit: Hour
            fromMetadata:
              namespace: io.envoy.ai_gateway
              key: tenant_request_limit # matches the GlobalRateLimits metadataKey above
```

:::note
The `limit.fromMetadata` field is added to Envoy Gateway's `BackendTrafficPolicy` by [envoyproxy/gateway#9216](https://github.com/envoyproxy/gateway/pull/9216). At the time of writing this is merged on Envoy Gateway `main` but not yet in a tagged release, so using it requires an Envoy Gateway build that includes the change.

Until a release that includes it is available, you can wire the same override with an `EnvoyPatchPolicy` that injects the native Envoy rate-limit `limit.dynamic_metadata` override directly, pointing at the same `io.envoy.ai_gateway` namespace and `metadataKey`. AI Gateway emits the `{ requests_per_unit, unit }` struct either way; only the consumer-side configuration differs.
:::

## Making Requests

For proper cost control and rate limiting, requests must include:

- `x-tenant-id`: Identifies the user making the request

Example request:

```shell
curl -H "Content-Type: application/json" \
  -H "x-tenant-id: user123" \
  -d '{
        "model": "gpt-4",
        "messages": [
            {
                "role": "user",
                "content": "Hello!"
            }
        ]
    }' \
  $GATEWAY_URL/v1/chat/completions
```

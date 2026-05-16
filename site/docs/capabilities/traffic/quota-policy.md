---
id: quota-policy
title: Quota Policy
sidebar_position: 6
---

# Quota Policy

QuotaPolicy enables token-based quota management for AI inference services in Envoy AI Gateway.
When a service's quota is exceeded, requests are rejected with a `429` status code. If the
`AIGatewayRoute` defines multiple backends, traffic automatically fails over to the next available
backend whose quota has not been exhausted.

:::note
QuotaPolicy manages **total consumption budgets** (e.g., 100 000 tokens per hour). This is distinct
from [usage-based rate limiting](./usage-based-ratelimiting.md), which controls **request velocity**
(e.g., requests per second). Use QuotaPolicy when you need to cap cumulative token spend across a
time window.
:::

## Overview

Key features of QuotaPolicy:

- **Service-wide token quotas** -- set a single token budget that applies to all traffic hitting an AIServiceBackend.
- **Per-model quota configuration** -- assign different token limits to individual models served by the same backend.
- **CEL expressions for custom cost calculation** -- weight input, output, and cached tokens differently using CEL.
- **Exclusive vs Shared bucket modes** -- control whether matching quota buckets are charged independently or together.
- **Client-selector based quota rules** -- carve out per-tenant or per-header quotas using bucket rules.
- **Shadow mode for testing without enforcement** -- dry-run quota evaluation without rejecting traffic.

## How It Works

1. A `QuotaPolicy` resource is created and attached to one or more `AIServiceBackend` resources via `targetRefs`.
2. For each completed request, the token cost is computed using the configured cost expression (defaults to `total_tokens`).
3. The token cost is charged against the matching quota bucket (service-wide, per-model default, or a matching bucket rule).
4. When the quota for a bucket is exceeded, subsequent requests receive a `429 Too Many Requests` response.
5. If the `AIGatewayRoute` lists multiple `backendRefs`, traffic fails over to the next backend whose quota is still available.

## Configuration

### Service-Wide Quota

The simplest configuration applies a single token budget to all requests routed to the target backend.

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: QuotaPolicy
metadata:
  name: my-quota-policy
spec:
  targetRefs:
    - group: aigateway.envoyproxy.io
      kind: AIServiceBackend
      name: my-backend
  # Quota applied across all models and all clients.
  serviceQuota:
    quota:
      limit: 100000 # Maximum tokens allowed in the window.
      duration: "1h" # Sliding window duration.
```

### Custom Cost Expression

By default, cost is measured using `total_tokens`. You can override this with a CEL expression
that weights token types differently. The following variables are available in the expression:
`input_tokens`, `cached_input_tokens`, and `output_tokens`.

```yaml
serviceQuota:
  # Cached input tokens cost 10% of regular input tokens;
  # output tokens cost 6x regular input tokens.
  costExpression: "input_tokens + cached_input_tokens * 0.1 + output_tokens * 6"
  quota:
    limit: 50000
    duration: "1h"
```

:::tip
Use a custom cost expression when different token types have significantly different costs with
your provider. For example, output tokens are typically more expensive than input tokens.
:::

### Per-Model Quotas

Use `perModelQuotas` to assign different budgets to individual models served by the same backend.
Per-model quotas override the service-wide quota for the specified model.

```yaml
perModelQuotas:
  - modelName: gpt-4
    quota:
      mode: Exclusive
      defaultBucket:
        limit: 10000 # Strict limit for expensive model.
        duration: "1h"
  - modelName: gpt-3.5-turbo
    quota:
      mode: Exclusive
      defaultBucket:
        limit: 100000 # Higher limit for cost-effective model.
        duration: "1h"
```

### Bucket Modes

The `mode` field on a `QuotaDefinition` controls how the default bucket and bucket rules interact
when a request matches one or more rules.

- **Exclusive** -- the request is charged to matching `bucketRules` **or** the `defaultBucket`, but
  not both. If no bucket rule matches, the default bucket is used. The request is denied only when
  all matching buckets are exhausted.
- **Shared** -- the request is charged to **all** matching `bucketRules` **and** the `defaultBucket`.
  The request is allowed only if quota is available in every matching bucket.

```yaml
perModelQuotas:
  - modelName: gpt-4
    quota:
      mode: Shared # Charge both default and matching rule buckets.
      defaultBucket:
        limit: 10000
        duration: "1h"
      bucketRules:
        - clientSelectors:
            - headers:
                - name: x-tenant-id
                  type: Distinct
          quota:
            limit: 2000 # Per-tenant limit within the shared budget.
            duration: "1h"
```

:::warning
In `Shared` mode a request is rejected if **any** of the matching buckets is exhausted, even if
other buckets still have remaining quota. Choose `Exclusive` mode when you want independent,
non-overlapping budgets.
:::

### Client Selectors with BucketRules

Bucket rules let you carve out dedicated quotas for specific clients identified by request headers.
This is useful for multi-tenant deployments where each tenant needs its own token budget.

```yaml
perModelQuotas:
  - modelName: gpt-4
    quota:
      mode: Exclusive
      defaultBucket:
        limit: 10000 # Fallback quota for unmatched tenants.
        duration: "1h"
      bucketRules:
        - clientSelectors:
            - headers:
                - name: x-tenant-id
                  type: Exact
                  value: premium-tenant
          quota:
            limit: 50000 # Premium tenant gets a larger budget.
            duration: "1h"
```

You can also use `type: Distinct` to create a separate bucket for every unique value of a header:

```yaml
bucketRules:
  - clientSelectors:
      - headers:
          - name: x-tenant-id
            type: Distinct # One bucket per unique tenant ID.
    quota:
      limit: 5000
      duration: "1h"
```

### Shadow Mode

Shadow mode lets you test quota rules without actually rejecting traffic. When `shadowMode` is
enabled on a bucket rule, all quota checks are performed (cache lookups, counter updates, telemetry
generation), but the outcome is never enforced -- the request always succeeds even if the quota is
exceeded.

```yaml
bucketRules:
  - clientSelectors:
      - headers:
          - name: x-tenant-id
            type: Distinct
    quota:
      limit: 5000
      duration: "1h"
    shadowMode: true # Evaluate but do not enforce.
```

:::tip
Use shadow mode when rolling out new quota rules. Monitor the telemetry to verify that limits are
set correctly before switching to enforcement by removing `shadowMode` (or setting it to `false`).
:::

## Duration Format

The `duration` field on `QuotaValue` accepts a number followed by a unit suffix:

| Suffix | Unit    | Example |
| ------ | ------- | ------- |
| `s`    | Seconds | `"30s"` |
| `m`    | Minutes | `"15m"` |
| `h`    | Hours   | `"1h"`  |

If no suffix is provided, the value is interpreted as seconds.

## Full Example

The following `QuotaPolicy` combines service-wide and per-model quotas, bucket rules with client
selectors, custom cost expressions, and shadow mode.

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: QuotaPolicy
metadata:
  name: full-quota-policy
spec:
  targetRefs:
    - group: aigateway.envoyproxy.io
      kind: AIServiceBackend
      name: my-backend

  # Service-wide fallback quota (applies to models without a per-model entry).
  serviceQuota:
    costExpression: "input_tokens + output_tokens * 3"
    quota:
      limit: 200000
      duration: "1h"

  # Per-model quotas with bucket rules.
  perModelQuotas:
    - modelName: gpt-4
      quota:
        costExpression: "input_tokens + cached_input_tokens * 0.1 + output_tokens * 6"
        mode: Exclusive
        defaultBucket:
          limit: 10000
          duration: "1h"
        bucketRules:
          # Premium tenant gets a dedicated, higher quota.
          - clientSelectors:
              - headers:
                  - name: x-tenant-id
                    type: Exact
                    value: premium-tenant
            quota:
              limit: 50000
              duration: "1h"
          # Track per-tenant usage in shadow mode (no enforcement).
          - clientSelectors:
              - headers:
                  - name: x-tenant-id
                    type: Distinct
            quota:
              limit: 5000
              duration: "1h"
            shadowMode: true

    - modelName: gpt-3.5-turbo
      quota:
        mode: Shared
        defaultBucket:
          limit: 100000
          duration: "1h"
        bucketRules:
          - clientSelectors:
              - headers:
                  - name: x-tenant-id
                    type: Distinct
            quota:
              limit: 20000
              duration: "1h"
```

## References

- [API Reference](../../api/api.mdx)
- [Usage-Based Rate Limiting](./usage-based-ratelimiting.md)
- [Provider Fallback](./provider-fallback.md)

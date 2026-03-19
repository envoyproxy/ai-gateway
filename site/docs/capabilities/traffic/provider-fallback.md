---
id: provider-fallback
title: Provider Fallback
sidebar_position: 6
---

import CodeBlock from '@theme/CodeBlock';
import ProviderFallbackRoute from '!!raw-loader!./examples/provider-fallback-route.yaml';
import ProviderFallbackBackends from '!!raw-loader!./examples/provider-fallback-backends.yaml';
import ProviderFallbackPolicy from '!!raw-loader!./examples/provider-fallback-policy.yaml';

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

<CodeBlock language="yaml">{ProviderFallbackRoute}</CodeBlock>

The corresponding `Backend` resources:

<CodeBlock language="yaml">{ProviderFallbackBackends}</CodeBlock>

## Configuring Fallback Behavior

Attach a `BackendTrafficPolicy` to the generated `HTTPRoute` to control retry behavior:

<CodeBlock language="yaml">{ProviderFallbackPolicy}</CodeBlock>

## References

- [Provider Fallback Example](https://github.com/envoyproxy/ai-gateway/tree/main/examples/provider_fallback)
- [`BackendTrafficPolicy` API Design](https://gateway.envoyproxy.io/contributions/design/backend-traffic-policy/)

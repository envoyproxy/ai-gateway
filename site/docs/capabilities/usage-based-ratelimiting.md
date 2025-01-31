---
id: usage-based-ratelimiting
title: Usage-based Rate Limiting
sidebar_position: 5
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide will help you configure usage-based rate limiting for your AI Gateway to control token consumption across different LLM requests.

## Overview

Usage-based rate limiting allows you to control and monitor token consumption for your LLM requests. You can set separate limits for:
- Input tokens
- Output tokens
- Total tokens

This is particularly useful for:
- Controlling costs per user
- Implementing fair usage policies
- Preventing abuse of your LLM endpoints

## Configuration

### 1. Configure Token Tracking

First, you need to configure which metadata keys will store the token counts from LLM requests. Add the following configuration to your `AIGatewayRoute`:

```yaml
spec:
  # ... other configuration ...
  llmRequestCosts:
    - metadataKey: llm_input_token
      type: InputToken
    - metadataKey: llm_output_token
      type: OutputToken
    - metadataKey: llm_total_token
      type: TotalToken
```

### 2. Configure Rate Limit Policy

Create a `BackendTrafficPolicy` to define your rate limit rules:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: ai-gateway-token-ratelimit-policy
  namespace: default
spec:
  targetRefs:
    - name: your-gateway-name
      kind: Gateway
      group: gateway.networking.k8s.io
  rateLimit:
    type: Global
    global:
      rules:
        # Input Token Rate Limit
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
          limit:
            requests: 10000  # Adjust based on your needs
            unit: Hour
          cost:
            request:
              from: Number
              number: 0
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_input_token

        # Output Token Rate Limit
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
          limit:
            requests: 20000  # Adjust based on your needs
            unit: Hour
          cost:
            request:
              from: Number
              number: 0
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_output_token

        # Total Token Rate Limit
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
          limit:
            requests: 30000  # Adjust based on your needs
            unit: Hour
          cost:
            request:
              from: Number
              number: 0
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token
```

## Understanding the Configuration

### Rate Limit Rules

Each rule in the configuration consists of:

1. **Client Selectors**: Define how to identify unique clients (e.g., by `x-user-id` header)
2. **Limit**: Specify the token budget and time unit
3. **Cost**: Configure how to calculate the cost of each request
   - `request`: Usually set to 0 to only track response tokens
   - `response`: Uses metadata from the LLM response to count tokens

### Time Units

You can specify rate limits using different time units:
- `Second`
- `Minute`
- `Hour`
- `Day`

### Client Identification

There are several ways to identify clients for rate limiting. Here are the most common approaches:

#### 1. Simple Header-based Identification

The simplest approach is using a custom header:

```yaml
clientSelectors:
  - headers:
      - name: x-user-id
        type: Distinct
```

#### 2. JWT Token Claims

You can extract client identifiers from JWT tokens. This is particularly useful when your application already uses JWT for authentication:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: jwt-auth
  namespace: default
spec:
  targetRefs:
  - name: your-gateway-name
    group: gateway.networking.k8s.io
    kind: Gateway
  jwt:
    providers:
      my-provider:
        issuer: https://your-issuer.com
        audiences:
          - your-audience
        remoteJWKS:
          uri: https://your-issuer.com/.well-known/jwks.json
        claimToHeaders:
          - claim: sub
            header: x-jwt-sub
          - claim: client_id
            header: x-client-id

---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: rate-limit-with-jwt
  namespace: default
spec:
  targetRefs:
    - name: your-gateway-name
      kind: Gateway
      group: gateway.networking.k8s.io
  rateLimit:
    type: Global
    global:
      rules:
        - clientSelectors:
            - headers:
                - name: x-jwt-sub  # Using the extracted JWT subject claim
                  type: Distinct
                - name: x-client-id  # Additionally using client_id for more granular control
                  type: Distinct
          limit:
            requests: 10000
            unit: Hour
          # ... rest of the rate limit configuration ...
```

#### 3. Combined Identification

You can combine multiple identifiers for more granular control:

```yaml
clientSelectors:
  - headers:
      - name: x-jwt-sub
        type: Distinct
      - name: x-client-id
        type: Distinct
      - name: x-organization-id
        type: Distinct
```

#### 4. Dynamic Header Transformation

For complex scenarios, you can use Envoy's header transformation to create custom identifiers:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: HTTPRoute
metadata:
  name: transform-headers
spec:
  parentRefs:
    - name: your-gateway-name
  rules:
    - filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - name: x-rate-limit-id
                value: "%REQ(x-organization-id)%_%REQ(x-client-id)%"
    # ... rest of the route configuration ...
```

Then use the transformed header in your rate limit configuration:

```yaml
clientSelectors:
  - headers:
      - name: x-rate-limit-id
        type: Distinct
```

:::warning
Avoid using sensitive claims directly in headers. Instead, use derived or hashed values when needed.
:::

## Making Requests

When making requests to your rate-limited endpoint, include the appropriate client identifier:

```shell
curl --fail \
    -H "Content-Type: application/json" \
    -H "x-user-id: user123" \
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

## Rate Limit Responses

When a rate limit is exceeded, the API will return a 429 (Too Many Requests) status code. The response will include headers indicating:
- The current rate limit status
- When the rate limit will reset

## Best Practices

1. **Set Appropriate Limits**: Consider your use case and adjust limits accordingly
2. **Monitor Usage**: Keep track of rate limit hits to adjust limits if needed
3. **Client Identification**: Choose a reliable way to identify clients
4. **Error Handling**: Implement proper handling of rate limit responses in your applications

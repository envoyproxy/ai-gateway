---
id: usage-based-ratelimiting
title: Usage-based Rate Limiting
sidebar_position: 5
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide focuses on AI Gateway's specific capabilities for token-based rate limiting in LLM requests. For general rate limiting concepts and configurations, refer to [Envoy Gateway's Rate Limiting documentation](https://gateway.envoyproxy.io/docs/tasks/traffic/global-rate-limit/).

## Overview

AI Gateway leverages Envoy Gateway's Global Rate Limit API to provide token-based rate limiting for LLM requests. Key features include:
- Token usage tracking based on model and user identifiers
- Configuration for tracking input, output, and total token metadata from LLM responses
- Model-specific rate limiting using AI Gateway headers (`x-ai-eg-model`)
- Support for custom token cost calculations using CEL expressions

## Configuration

### 1. Configure Token Tracking

AI Gateway automatically tracks token usage for each request. Configure which token counts you want to track in your `AIGatewayRoute`:

```yaml
spec:
  llmRequestCosts:
    - metadataKey: llm_input_token
      type: InputToken    # Counts tokens in the request
    - metadataKey: llm_output_token
      type: OutputToken   # Counts tokens in the response
    - metadataKey: llm_total_token
      type: TotalToken   # Tracks combined usage
```

For advanced token calculations specific to your use case:

```yaml
spec:
  llmRequestCosts:
    - metadataKey: custom_cost
      type: CEL
      celExpression: "input_tokens * 0.5 + output_tokens * 1.5"  # Example: Weight output tokens more heavily
```

### 2. Configure Model-Specific Rate Limits

The following example shows how to use AI Gateway's token tracking with user and model identification to implement rate limits. In this example, we limit each user to 10,000 tokens per hour when using the GPT-4 model. This configuration:
- Uses the `x-user-id` header to identify users
- Uses the `x-ai-eg-model` header to apply limits to specific models
- Leverages AI Gateway's token counting (configured in step 2) to enforce the limits

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
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-4
          limit:
            requests: 10000   # Token limit for GPT-4
            unit: Hour
          cost:
            request:
              from: Number
              number: 0      # Only count tokens, not requests
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token
```

:::warning
When configuring the rate limit cost, set the number to 0 to ensure only token usage counts towards the limit. If not explicitly set to 0, the cost defaults to 1 for backward compatibility, which would count each request against the limit.
:::

## Making Requests

When making requests, include the required AI Gateway headers:

```shell
curl --fail \
    -H "Content-Type: application/json" \
    -H "x-user-id: user123" \
    -H "x-ai-eg-model: gpt-4" \
    -H "x-ai-eg-model-provider: openai" \
    -d '{
        "messages": [
            {
                "role": "user",
                "content": "Hello!"
            }
        ]
    }' \
    $GATEWAY_URL/v1/chat/completions
```

## Best Practices

1. **Model-Specific Limits**: Set appropriate token limits based on model capabilities and costs
2. **User Identification**: Ensure consistent user identification across requests
3. **Token Tracking Strategy**: Choose appropriate token tracking methods for your use case

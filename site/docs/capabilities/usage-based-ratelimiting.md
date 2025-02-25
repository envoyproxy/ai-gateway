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
   - `OutputToken`: Counts tokens in the model's response
   - `TotalToken`: Combines both input and output tokens
   - `CEL`: Allows custom token calculations using CEL expressions

4. **Multiple Rate Limits**: You can configure multiple rate limit rules for the same user-model combination. For example:
   - Limit total tokens per hour
   - Separate limits for input and output tokens
   - Custom limits using CEL expressions

:::note
For model providers with OpenAI schema transformations (like AWS Bedrock), AI Gateway automatically captures token usage through its request/response transformer. This enables consistent token tracking and rate limiting across different AI services using a unified OpenAI-compatible format.
:::

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
      cel: "input_tokens * 0.5 + output_tokens * 1.5"  # Example: Weight output tokens more heavily
```

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
                - name: x-user-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-4
          limit:
            requests: 1000    # 1000 total tokens per hour
            unit: Hour
          cost:
            request:
              from: Number
              number: 0      # Set to 0 so only token usage counts
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token    # Uses total tokens from the responses
        # Rate limit rule for GPT-3.5: 5000 total tokens per hour per user
        # Higher limit since the model is more cost-effective
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-3.5-turbo
          limit:
            requests: 5000    # 5000 total tokens per hour (higher limit for less expensive model)
            unit: Hour
          cost:
            request:
              from: Number
              number: 0      # Set to 0 so only token usage counts
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
                key: llm_total_token    # Uses total tokens from the response
```

:::warning
When configuring rate limits:
1. Always set the request cost number to 0 to ensure only token usage counts towards the limit
2. Set appropriate limits for different models based on their costs and capabilities
3. Ensure both user and model identifiers are used in rate limiting rules
:::

## Making Requests

For proper cost control and rate limiting, requests must include:
- `x-user-id`: Identifies the user making the request

Example request:
```shell
curl -H "Content-Type: application/json" \
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

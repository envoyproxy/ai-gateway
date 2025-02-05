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

<<<<<<< HEAD
=======
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
The token counts are extracted from the model's response. Make sure your model backend provides token usage information in a format compatible with the OpenAI schema.
:::

>>>>>>> update-docs
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

<<<<<<< HEAD
### 2. Configure Model-Specific Rate Limits

The following example shows how to use AI Gateway's token tracking with user and model identification to implement rate limits. In this example, we limit each user to 10,000 tokens per hour when using the GPT-4 model. This configuration:
- Uses the `x-user-id` header to identify users
- Uses the `x-ai-eg-model` header to apply limits to specific models
- Leverages AI Gateway's token counting (configured in step 2) to enforce the limits
=======
### 2. Configure Rate Limits

AI Gateway uses Envoy Gateway's Global Rate Limit API to configure rate limits. Rate limits should be defined using a combination of user and model identifiers to properly control costs at the model level. Configure this using a `BackendTrafficPolicy`:

#### Example: Cost-Based Model Rate Limiting

The following example demonstrates a common use case where different models have different token limits based on their costs. This is useful when:
- You want to limit expensive models (like GPT-4) more strictly than cheaper ones
- You need to implement different quotas for different tiers of service
- You want to prevent cost overruns while still allowing flexibility with cheaper models
>>>>>>> update-docs

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
<<<<<<< HEAD
=======
        # Rate limit rule for GPT-4: 1000 total tokens per hour per user
        # Stricter limit due to higher cost per token
>>>>>>> update-docs
        - clientSelectors:
            - headers:
                - name: x-user-id
                  type: Distinct
                - name: x-ai-eg-model
                  type: Exact
                  value: gpt-4
          limit:
<<<<<<< HEAD
            requests: 10000   # Token limit for GPT-4
=======
            requests: 1000    # 1000 total tokens per hour
>>>>>>> update-docs
            unit: Hour
          cost:
            request:
              from: Number
<<<<<<< HEAD
              number: 0      # Only count tokens, not requests
=======
              number: 0      # Set to 0 so only token usage counts
>>>>>>> update-docs
            response:
              from: Metadata
              metadata:
                namespace: io.envoy.ai_gateway
<<<<<<< HEAD
                key: llm_total_token
```

:::warning
When configuring the rate limit cost, set the number to 0 to ensure only token usage counts towards the limit. If not explicitly set to 0, the cost defaults to 1 for backward compatibility, which would count each request against the limit.
=======
                key: llm_total_token    # Uses total tokens from the response
        
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
>>>>>>> update-docs
:::

## Making Requests

<<<<<<< HEAD
When making requests, include the required AI Gateway headers:

=======
For proper cost control and rate limiting, requests must include:
- `x-user-id`: Identifies the user making the request
- `x-ai-eg-model`: Identifies the model being used

Example request:
>>>>>>> update-docs
```shell
curl --fail \
    -H "Content-Type: application/json" \
    -H "x-user-id: user123" \
<<<<<<< HEAD
    -H "x-ai-eg-model: gpt-4" \
    -H "x-ai-eg-model-provider: openai" \
=======
    -H "x-ai-eg-model: gpt-4" \    # Both user ID and model are required
>>>>>>> update-docs
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
<<<<<<< HEAD

## Best Practices

1. **Model-Specific Limits**: Set appropriate token limits based on model capabilities and costs
2. **User Identification**: Ensure consistent user identification across requests
3. **Token Tracking Strategy**: Choose appropriate token tracking methods for your use case
=======
>>>>>>> update-docs

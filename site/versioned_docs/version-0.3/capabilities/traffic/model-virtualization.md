---
id: model-name-virtualization
title: Model Name Virtualization
sidebar_position: 7
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Envoy AI Gateway provides an advanced model name virtualization capability that allows you to manage and route requests to different AI models seamlessly.
This guide covers the key feature and configuration for model virtualization.

## Motivation

It is not uncommon for multiple AI providers to offer a similar or identical model, such as Llama-3-70b, etc.
However, each provider tends to have its own unique naming convention for the same model.
For example, `Claude 3.5 Sonnet` is hosted both on GCP and AWS Bedrock, but they have different model names:
* GCP: `claude-3-5-sonnet-v2@20241022`, etc.
* AWS Bedrock: `arn:aws:bedrock:us-west-2:123456789012:provisioned-model/abc123xyz`

From downstream GenAI applications' perspective, it is beneficial to have a unified model name that abstracts away these differences.

## Virtualization with modelNameOverride API

In our top level AIGatewayRoute configuration, you can specify a `modelNameOverride` inside [AIGatewayRouteBackendRef](/api/api.mdx#aigatewayrouterulebackendref) on each route rule to override the model name that is sent to the upstream AI provider.
This feature is primarily designed for scenarios where you want to dynamically change the model name based on the actual AI provider the request is being sent to.

The example configuration looks like this:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: test-route
spec:
  targetRefs: [...]
  rules:
  - matches:
      - headers:
        - type: Exact
          name: x-ai-eg-model
          value: claude-3-5-sonnet-v2
    backendRefs:
    - name: aws-backend
      modelNameOverride: arn:aws:bedrock:us-west-2:123456789012:provisioned-model/abc123xyz
      weight: 50
    - name: gcp-backend
      modelNameOverride: claude-3-5-sonnet-v2@20241022
      weight: 50
```

This configuration allows downstream applications to use a unified model name `claude-3-5-sonnet-v2` while splitting traffic between the AWS Bedrock and GCP AI providers based on the specified `modelNameOverride`.
This is what the word "Virtualization" means in this context: abstracting away the differences in model names across different AI providers and providing a unified interface for downstream applications.
It also can be thought of as "one-to-many" aliasing of model names, where one unified model name can map to multiple different model names on different providers depending on the routing path.

## Virtualization for fallback scenarios

As we see in the [Provider Fallback](./provider-fallback) page, Envoy AI Gateway allows you to fallback to a different AI provider if the primary one fails.
However, sometimes we want to fallback to a different model on the same provider.
For example, it is natural to set up the Envoy AI Gateway in a way that if the primary expensive model fails (rate limit, etc), Envoy retries the request to a less expensive model on the same provider.
More concretely, if the request to `gpt-4` fails, we want to retry it with `gpt-3.5-turbo` on the same OpenAI provider.

`modelNameOverride` can also be used in this scenario to achieve the desired behavior. The configuration would look like this:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: test-route
spec:
  targetRefs: [...]
  rules:
  - matches:
      - headers:
        - type: Exact
          name: x-ai-eg-model
          value: gpt-4
    backendRefs:
    - name: openai-backend
      # This doesn't specify modelNameOverride, so it will use the default model name `gpt-4` in the request.
      priority: 0
    - name: openai-backend
      modelNameOverride: gpt-3.5-turbo
      priority: 1
```

With this configuration, assuming the retry is properly configured as per the [Provider Fallback](./provider-fallback) page, if the request to `gpt-4` fails, Envoy AI Gateway will automatically retry the request to `gpt-3.5-turbo` on the same OpenAI provider without requiring any changes to the downstream application.

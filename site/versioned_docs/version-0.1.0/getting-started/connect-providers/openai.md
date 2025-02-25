---
id: openai
title: Connect OpenAI
sidebar_position: 2
---

# Connect OpenAI

This guide will help you configure Envoy AI Gateway to work with OpenAI's models.

## Prerequisites

Before you begin, you'll need:

- An OpenAI API key from [OpenAI's platform](https://platform.openai.com)
- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

## Configuration Steps

:::info Ready to proceed?
Ensure you have followed the steps in [Connect Providers](../connect-providers/)
:::

### 1. Configure OpenAI Credentials

Edit the `basic.yaml` file to replace the OpenAI placeholder value:

- Find the section containing `OPENAI_API_KEY`
- Replace it with your actual OpenAI API key

:::caution Security Note
Make sure to keep your API key secure and never commit it to version control.
The key will be stored in a Kubernetes secret.
:::

### 2. Apply Configuration

Apply the updated configuration and wait for the Gateway pod to be ready. If you already have a Gateway running,
then the secret credential update will be picked up automatically in a few seconds.

```shell
kubectl apply -f basic.yaml

kubectl wait pods --timeout=2m \
  -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
  -n envoy-gateway-system \
  --for=condition=Ready
```

### 3. Test the Configuration

You should have set `$GATEWAY_URL` as part of the basic setup before connecting to providers.
See the [Basic Usage](../basic-usage.md) page for instructions.

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "Hi."
      }
    ]
  }' \
  $GATEWAY_URL/v1/chat/completions
```

## Troubleshooting

If you encounter issues:

1. Verify your API key is correct and active

2. Check pod status:

   ```shell
   kubectl get pods -n envoy-gateway-system
   ```

3. View controller logs:

   ```shell
   kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller
   ```

4. View External Processor Logs

   ```shell
   kubectl logs services/ai-eg-route-extproc-envoy-ai-gateway-basic
   ```

5. Common errors:
   - 401: Invalid API key
   - 429: Rate limit exceeded
   - 503: OpenAI service unavailable

## Next Steps

After configuring OpenAI:

- [Connect AWS Bedrock](./aws-bedrock.md) to add another provider

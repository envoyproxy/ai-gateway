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
   kubectl logs -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -c ai-gateway-extproc
   ```

5. Common errors:
   - 401: Invalid API key
   - 429: Rate limit exceeded
   - 503: OpenAI service unavailable

## Configuring More Models

To use more models, add more [AIGatewayRouteRule]s to the `basic.yaml` file with the [model alias] in the `value` field. For example, to use [o1]

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: envoy-ai-gateway-basic
  namespace: default
spec:
  schema:
    name: OpenAI
  targetRefs:
    - name: envoy-ai-gateway-basic
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: o1
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
```

## Next Steps

After configuring OpenAI:

- [Connect AWS Bedrock](./aws-bedrock.md) to add another provider

[AIGatewayRouteRule]: ../../api/api.mdx#aigatewayrouterule
[model alias]: https://platform.openai.com/docs/models#current-model-aliases
[o1]: https://platform.openai.com/docs/models#o1

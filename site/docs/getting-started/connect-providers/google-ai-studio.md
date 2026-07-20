---
id: google-ai-studio
title: Connect Google AI Studio
sidebar_position: 3
---

import CodeBlock from '@theme/CodeBlock';
import vars from '../../\_vars.json';

# Connect Google AI Studio

This guide will help you configure Envoy AI Gateway to work with Google AI Studio (the Gemini Developer API
hosted at `generativelanguage.googleapis.com`) for image generation.

Google AI Studio is authenticated with a plain API key, which is what makes it different from
[GCP VertexAI](./gcp-vertexai.md): VertexAI hosts the same Gemini models but expects GCP service account
credentials. Use this guide when you have an AI Studio API key; use the VertexAI guide when you have GCP
credentials.

## Prerequisites

Before you begin, you'll need:

- A Google AI Studio API key from [Google AI Studio](https://aistudio.google.com/apikey)
- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

## Configuration Steps

:::info Ready to proceed?
Ensure you have followed the steps in [Connect Providers](../connect-providers/)
:::

### 1. Download configuration template

<CodeBlock language="shell">
{`curl -O https://raw.githubusercontent.com/envoyproxy/ai-gateway/${vars.aigwGitRef}/examples/basic/googleai.yaml`}
</CodeBlock>

### 2. Configure Google AI Studio Credentials

Edit the `googleai.yaml` file to replace the Google AI Studio placeholder value:

- Find the section containing `GOOGLE_AI_STUDIO_API_KEY`
- Replace it with your actual Google AI Studio API key

The gateway injects this key as the `x-goog-api-key` header that Gemini's native endpoints expect.

:::caution Security Note
Make sure to keep your API key secure and never commit it to version control.
The key will be stored in a Kubernetes secret.
:::

### 3. Apply Configuration

Apply the updated configuration and wait for the Gateway pod to be ready. If you already have a Gateway running,
then the secret credential update will be picked up automatically in a few seconds.

```shell
kubectl apply -f googleai.yaml

kubectl wait pods --timeout=2m \
  -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
  -n envoy-gateway-system \
  --for=condition=Ready
```

### 4. Test the Configuration

You should have set `$GATEWAY_URL` as part of the basic setup before connecting to providers.
See the [Basic Usage](../basic-usage.md) page for instructions.

#### Test Image Generation

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash-image",
    "prompt": "A watercolor painting of a lighthouse at sunrise"
  }' \
  $GATEWAY_URL/v1/images/generations
```

The response is in the OpenAI image generation shape, with the image returned as base64 in `data[0].b64_json`.
Gemini always returns image bytes inline, so `response_format` may be omitted or set to `b64_json`; `url` and
`stream` are rejected with a 422. Use `n` (1-10) to ask for more than one image. The DALL-E and gpt-image-1
rendering hints (`size`, `quality`, `style`, `background`, `output_format`, `output_compression`, `moderation`)
have no `generateContent` equivalent and are not forwarded.

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
   - 400: The model does not support image generation, or the request asked for something Gemini cannot return
   - 401 / 403: Invalid or unauthorized API key
   - 429: Rate limit exceeded
   - 503: Google AI Studio service unavailable

## Configuring More Models

To use more models, add more [AIGatewayRouteRule]s to the `googleai.yaml` file with the model name in the
`value` field, alongside the existing `gemini-2.5-flash-image` rule.

## Next Steps

After configuring Google AI Studio:

- [Connect GCP VertexAI](./gcp-vertexai.md) to reach Gemini with GCP service account credentials
- [Connect OpenAI](./openai.md) to add another provider

[AIGatewayRouteRule]: ../../api/api.mdx#aigatewayrouterule

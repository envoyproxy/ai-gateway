---
id: connect-providers
title: Connect Providers
sidebar_position: 5
---

# Connect Providers

After setting up the basic AI Gateway with the mock backend, you can configure it to work with real AI model providers. This section will guide you through connecting different AI providers to your gateway.

## Available Providers

Currently, Envoy AI Gateway supports the following providers:

- [OpenAI](./openai.md) - Connect to OpenAI's GPT models
- [AWS Bedrock](./aws-bedrock.md) - Access AWS Bedrock's suite of foundation models
- [Azure OpenAI](./azure-openai.md) - Access Azure OpenAI's suite of foundation models

## Before You Begin

Before configuring any provider:

1. Complete the [Basic Usage](../basic-usage.md) guide
2. Remove the basic configuration with the mock backend

   ```shell
   kubectl delete -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/basic.yaml

   kubectl wait pods --timeout=15s \
     -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
     -n envoy-gateway-system \
     --for=delete
   ```

3. Download configuration template

   ```shell
   curl -O https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/basic.yaml
   ```

## Security Best Practices

When configuring AI providers, keep these security considerations in mind:

- Store credentials securely using Kubernetes secrets
- Never commit API keys or credentials to version control
- Regularly rotate your credentials
- Use the principle of least privilege when setting up access
- Monitor usage and set up appropriate rate limits

## Next Steps

Choose your provider to get started:
- [Connect OpenAI](./openai.md)
- [Connect AWS Bedrock](./aws-bedrock.md)
- [Connect Azure OpenAI](./azure-openai.md)

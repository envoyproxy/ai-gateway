---
id: aws-bedrock
title: Connect AWS Bedrock
sidebar_position: 3
---

# Connect AWS Bedrock

This guide will help you configure Envoy AI Gateway to work with AWS Bedrock's foundation models.

## Prerequisites

Before you begin, you'll need:
- AWS credentials with access to Bedrock
- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

## AWS Credentials Setup

Ensure you have:
1. An AWS account with Bedrock access enabled
2. AWS credentials with permissions to:
   - `bedrock:InvokeModel`
   - `bedrock:ListFoundationModels`
3. Your AWS access key ID and secret access key
4. Enabled model access to "Llama 3.2 1B Instruct" in the `us-east-1` region
   - If you want to use a different AWS region, you must update all instances of the string
     `us-east-1` with the desired region in `basic.yaml`.

:::tip AWS Best Practices
Consider using AWS IAM roles and limited-scope credentials for production environments.
:::

## Configuration Steps

:::info Ready to proceed?
Ensure you have followed the steps in [Connect Providers](../connect-providers/)
:::

### 1. Configure AWS Credentials

Edit the `basic.yaml` file to replace these placeholder values:
- `AWS_ACCESS_KEY_ID`: Your AWS access key ID
- `AWS_SECRET_ACCESS_KEY`: Your AWS secret access key

:::caution Security Note
Make sure to keep your AWS credentials secure and never commit them to version control.
The credentials will be stored in Kubernetes secrets.
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
    "model": "us.meta.llama3-2-1b-instruct-v1:0",
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

1. Verify your AWS credentials are correct and active
2. Check pod status:
   ```shell
   kubectl get pods -n envoy-gateway-system
   ```
3. View controller logs:
   ```shell
   kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller
   ```
4. Common errors:
   - 401/403: Invalid credentials or insufficient permissions
   - 404: Model not found or not available in region
   - 429: Rate limit exceeded

## Configuring More Models

To use more models, add more [AIGatewayRouteRule]s to the `basic.yaml` file with the [model ID] in the `value` field. For example, to use [Claude 3 Sonnet]

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
              value: anthropic.claude-3-sonnet-20240229-v1:0
      backendRefs:
        - name: envoy-ai-gateway-basic-aws
```

[AIGatewayRouteRule]: ../../api/api.mdx#aigatewayrouterule
[model ID]: https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html
[Claude 3 Sonnet]: https://docs.anthropic.com/en/docs/about-claude/models#model-comparison-table

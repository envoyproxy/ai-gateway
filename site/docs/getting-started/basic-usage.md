---
id: basic-usage
title: Basic Usage
sidebar_position: 4
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide will help you set up a basic AI Gateway configuration and make your first request.

For Windows users, note that you are able to use Windows Subsystem for Linux (WSL) to run the commands below if they do not work on the Windows command prompt.

## Setting Up Your Environment

### Deploy Basic Configuration

Let's start by deploying a basic AI Gateway setup that includes a test backend:

```shell
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/basic.yaml
```

Wait for the Gateway pod to be ready:
```shell
kubectl wait pods --timeout=2m \
    -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
    -n envoy-gateway-system \
    --for=condition=Ready
```

## Configure `$GATEWAY_URL`

First, check if your Gateway has an external IP address assigned:

```shell
kubectl get svc -n envoy-gateway-system \
    --selector=gateway.envoyproxy.io/owning-gateway-namespace=default,gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic
```

You'll see output similar to this:
```
NAME                    TYPE           CLUSTER-IP      EXTERNAL-IP      PORT(S)
eg-envoy-ai-gateway    LoadBalancer   10.96.61.234    <pending/IP>     80:31234/TCP
```

Choose one of these options based on the EXTERNAL-IP status:

<Tabs>
<TabItem value="external-ip" label="Using External IP">

If the EXTERNAL-IP shows an actual IP address (not `<pending>`), you can access the gateway directly:

First, save the external IP and set the gateway URL:
```shell
export GATEWAY_URL=$(kubectl get gateway/envoy-ai-gateway-basic -o jsonpath='{.status.addresses[0].value}')
```

Verify the URL is available:
```shell
echo $GATEWAY_URL
```

</TabItem>
<TabItem value="port-forward" label="Using Port Forwarding">

If the EXTERNAL-IP shows `<pending>` or your cluster doesn't support LoadBalancer services, use port forwarding.

First, set the gateway URL:
```shell
export GATEWAY_URL="http://localhost:8080"
```

Then set up port forwarding (this will block the terminal):
```shell
export ENVOY_SERVICE=$(kubectl get svc -n envoy-gateway-system \
    --selector=gateway.envoyproxy.io/owning-gateway-namespace=default,gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
    -o jsonpath='{.items[0].metadata.name}')

kubectl port-forward -n envoy-gateway-system svc/$ENVOY_SERVICE 8080:80
```

</TabItem>
</Tabs>

## Testing the Gateway

### Making Test Requests

Open a new terminal and send test requests to the AI Gateway using the `GATEWAY_URL` we set up:

#### Test Chat Completions

```shell
curl -H "Content-Type: application/json" \
    -d '{
        "model": "some-cool-self-hosted-model",
        "messages": [
            {
                "role": "system",
                "content": "Hi."
            }
        ]
    }' \
    $GATEWAY_URL/v1/chat/completions
```

#### Test Embeddings

```shell
curl -H "Content-Type: application/json" \
    -d '{
        "model": "some-cool-self-hosted-model",
        "input": "Hello, world!"
    }' \
    $GATEWAY_URL/v1/embeddings
```

:::tip

If you're opening a new terminal, you'll need to set the `GATEWAY_URL` variable again.

:::

### Expected Responses

#### Chat Completions Response

You should receive a response like:

```json
{
    "choices": [
        {
            "message": {
                "content": "I'll be back."
            }
        }
    ]
}
```

#### Embeddings Response

For embeddings requests, you should receive a response like:

```json
{
    "object": "list",
    "data": [
        {
            "object": "embedding",
            "embedding": [0.1, 0.2, 0.3, 0.4, 0.5],
            "index": 0
        }
    ],
    "model": "some-cool-self-hosted-model",
    "usage": {
        "prompt_tokens": 3,
        "total_tokens": 3
    }
}
```

:::note

These responses come from a mock backend. The model `some-cool-self-hosted-model` is configured to return test responses.
For real AI model responses, see the [Connect Providers](./connect-providers) guide.

:::

### Understanding the Response Formats

The basic setup includes a mock backend that demonstrates the API structure but doesn't provide real AI responses. The response formats follow the standard OpenAI API formats:

**Chat Completions:**
- A `choices` array containing responses
- Each message having a `role` and `content`

**Embeddings:**
- A `data` array containing embedding objects
- Each embedding having a vector array and index
- Usage information showing token consumption

## Next Steps

Now that you've tested the basic setup, you can:
- Configure [real AI model backends](./connect-providers) like OpenAI or AWS Bedrock
- Explore the [API Reference](../api/) for more details about available endpoints

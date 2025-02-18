---
id: localmodel
title: Connect Local Model
sidebar_position: 3
---

# Connect Local Model

This guide will help you configure Envoy AI Gateway to work with a locally hosted model such as [DeepSeek R1](https://github.com/deepseek-ai/DeepSeek-R1).

## Prerequisites

Before you begin, you'll need:

- [Ollama](https://ollama.com/) installed on local machine considering for self-hosted model
- Serve DeepSeek R1 or similar model on your local machine

```text
ollama pull deepseek-r1:7b
OLLAMA_HOST=0.0.0.0 ollama serve
```

- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

## Configuration Steps

:::info Ready to proceed?
Ensure you have followed the steps in [Connect Providers](../connect-providers/)
:::

### Apply Configuration

Apply the updated configuration and wait for the Gateway pod to be ready. If you already have a Gateway running,
the secret credential update will take effect automatically in a few seconds.

```shell
curl -O https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/localmodel.yaml

kubectl apply -f localmodel.yaml

kubectl wait pods --timeout=2m \
  -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
  -n envoy-gateway-system \
  --for=condition=Ready
```

### Test the Configuration

You should have set `$GATEWAY_URL` as part of the basic setup before connecting to providers.
See the [Basic Usage](../basic-usage.md) page for instructions.

```shell
curl --fail \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-r1:7b",
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

1. Verify that Ollama has loaded the model and you have memory and computer available on your local machine to run LLM

2. Check pod status:

   ```shell
   kubectl get pods -n envoy-gateway-system
   ```

3. View controller logs:

   ```shell
   kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller
   ```

4. View External Process Logs

   ```shell
   kubectl logs services/ai-eg-route-extproc-envoy-ai-gateway-basic
   ```
5. If you are running Kubernetes locally, ensure that Envoy Service URL is correct and port 8080 is available on your local

```
export ENVOY_SERVICE=$(kubectl get svc -n envoy-gateway-system \
    --selector=gateway.envoyproxy.io/owning-gateway-namespace=default,gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
    -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n envoy-gateway-system svc/$ENVOY_SERVICE 8080:80
```

6. Common errors:
   - 500: Incorrect Model Id in the HTTP request. Check localmodel.yaml for host name in backend configuration
   - 503: Model is unavailable or request time out because of latency by model

## Next Steps

After configuring DeepSeek:

- [Connect AWS Bedrock](./aws-bedrock.md) to add another provider

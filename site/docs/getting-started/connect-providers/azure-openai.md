---
id: azure-openai
title: Connect Azure OpenAI
sidebar_position: 3
---

import CodeBlock from '@theme/CodeBlock';
import vars from '../../\_vars.json';

# Connect Azure OpenAI

This guide will help you configure Envoy AI Gateway to work with Azure OpenAI's foundation models.

Azure OpenAI supports two authentication methods:

1. **API Key**: Simple authentication using an API key from your Azure OpenAI resource.

2. **Microsoft Entra ID**: OAuth-based authentication with two options:
   - **Client Secret**: Uses client credentials (client ID, tenant ID, and secret)
   - **Managed Identity / Workload Identity**: Uses Azure Managed Identity/Workload identity (recommended for production on Azure Kubernetes Clusters)

This guide focuses on Microsoft Entra ID authentication. For API Key authentication, refer to the [Azure OpenAI API documentation](https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#authentication).

## Prerequisites

Before you begin, you'll need:

- Azure OpenAI resource with model access enabled (e.g., "GPT-4o")
- Basic setup completed from the [Basic Usage](../basic-usage.md) guide
- Basic configuration removed as described in the [Advanced Configuration](./index.md) overview

For Microsoft Entra ID authentication, additionally:
- Azure tenant ID, client ID, and either a client secret OR a configured Managed Identity
- For AKS Workload Identity: See [Azure documentation](https://learn.microsoft.com/en-us/azure/aks/workload-identity-deploy-cluster) for infrastructure setup

## Configuration Steps

### 1. Download configuration template

<CodeBlock language="shell">
{`curl -O https://raw.githubusercontent.com/envoyproxy/ai-gateway/${vars.aigwGitRef}/examples/basic/azure_openai.yaml`}
</CodeBlock>

### 2. Configure Azure Credentials

The `azure_openai.yaml` file includes examples for both Entra ID authentication methods. By default, it uses client secret authentication.

#### Client Secret Authentication (Default)

Edit the `azure_openai.yaml` file to replace these placeholder values:

- `AZURE_TENANT_ID`: Your Azure tenant ID
- `AZURE_CLIENT_ID`: Your Azure client ID
- `AZURE_CLIENT_SECRET`: Your Azure client secret

#### Managed Identity / Workload Identity Authentication

To use Workload Identity instead, see the commented examples in `azure_openai.yaml`. And configure the service account annotations during Helm installation:

```shell
helm upgrade --install ai-gateway-controller envoyproxy/ai-gateway-helm \
  --set controller.serviceAccount.annotations."azure\.workload\.identity/client-id"="<your-managed-identity-client-id>" \
  --set controller.serviceAccount.annotations."azure\.workload\.identity/tenant-id"="<your-tenant-id>" \
  --create-namespace \
  -n envoy-ai-gateway-system

# Add the required label:
kubectl label serviceaccount ai-gateway-controller \
  azure.workload.identity/use=true \
  -n envoy-ai-gateway-system
```

Then use the Managed Identity `BackendSecurityPolicy` example from the YAML file (uncomment Option 2, comment out Option 1). Omitting the `CLIENT_ID`, `CLIENT_SECRET` and only specify `useManagedIdentity: true`. When configuring the identity on Azure your federated subject should look something like this `system:serviceaccount:envoy-ai-gateway-system:ai-gateway-controller`.

:::caution Security Note
Keep your Azure credentials secure and never commit them to version control.
:::

### 3. Apply Configuration

Apply the updated configuration and wait for the Gateway pod to be ready. If you already have a Gateway running, then the secret credential update will be picked up automatically in a few seconds.

```shell
kubectl apply -f azure_openai.yaml

kubectl wait pods --timeout=2m \
  -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic \
  -n envoy-gateway-system \
  --for=condition=Ready
```

### 4. Test the Configuration

You should have set `$GATEWAY_URL` as part of the basic setup before connecting to providers.
See the [Basic Usage](../basic-usage.md) page for instructions.

```shell
curl -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {
        "role": "user",
        "content": "Hi."
      }
    ]
  }' \
  $GATEWAY_URL/v1/chat/completions
```

## Troubleshoot

If you encounter issues:

1. Verify your Azure credentials are correct and active
2. Check pod status

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

To use more models, add more [AIGatewayRouteRule]s to the `azure_openai.yaml` file with the [model ID] in the `value` field. For example, to use [GPT-4.5 Preview]

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: envoy-ai-gateway-basic-azure
  namespace: default
spec:
  parentRefs:
    - name: envoy-ai-gateway-basic
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gpt-4.5-preview
      backendRefs:
        - name: envoy-ai-gateway-basic-azure
```

[AIGatewayRouteRule]: ../../api/api.mdx#aigatewayrouterule
[model ID]: https://learn.microsoft.com/en-us/azure/ai-services/openai/concepts/models
[GPT-4.5 Preview]: https://learn.microsoft.com/en-us/azure/ai-services/openai/concepts/models?tabs=global-standard%2Cstandard-chat-completions#gpt-45-preview

---
id: installation
title: Installation
sidebar_position: 3
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide will walk you through installing Envoy AI Gateway and its required components.

## Installing Envoy AI Gateway

The easiest way to install Envoy AI Gateway is using the Helm chart. First, install the AI Gateway Helm chart; this will install the CRDs as well. Once completed, wait for the deployment to be ready.

```shell
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  --create-namespace

kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=condition=Available
```

:::tip
Note that you are browsing the documentation for the main branch version of Envoy AI Gateway, which is not a stable release.
We highly recommend you replace `v0.0.0-latest` with `v0.0.0-${commit hash of https://github.com/envoyproxy/ai-gateway}` to pin to a specific version.
Otherwise, the controller will be installed with the latest version at the time of installation, which can be unstable over time due to ongoing development (the latest container tags are overwritten).
:::

> If you are experiencing network issues with `docker.io` , you can install the helm chart from the code repo [ai-gateway-helm](https://github.com/envoyproxy/ai-gateway/tree/main/manifests/charts/ai-gateway-helm) instead.

### Installing CRDs separately

If you want to manage the CRDs separately, install the CRD Helm chart (`ai-gateway-crds-helm`) which will install just the CRDs:

```shell
helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  --create-namespace
```

After the CRDs are installed, you can install the AI Gateway Helm chart without re-installing the CRDs by using the `--skip-crds` flag.

```shell
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  --create-namespace \
  --skip-crds
```

## Configuring Envoy Gateway

Envoy Gateway needs to be configured with AI Gateway-specific settings. The configuration is passed via helm values when installing/upgrading Envoy Gateway.

### Using Helm Values Files

Use the base Envoy Gateway values file for AI Gateway integration. You can combine it with addon files for additional features:

```shell
# Basic installation (no rate limiting)
helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-gateway-system \
  --create-namespace \
  -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-values.yaml

kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

### Configuration Details

The Envoy Gateway configuration must include the following AI Gateway-specific settings:

<details>
<summary>Click to view full configuration with explanations</summary>

```yaml
config:
  apiVersion: gateway.envoyproxy.io/v1alpha1
  kind: EnvoyGateway
  gateway:
    controllerName: gateway.envoyproxy.io/gatewayclass-controller
  logging:
    level:
      default: info
  provider:
    type: Kubernetes
  extensionApis:
    # Required: Enable Backend API for AI service backends
    enableEnvoyPatchPolicy: true
    enableBackend: true
  extensionManager:
    hooks:
      xdsTranslator:
        translation:
          # Required: AI Gateway needs to translate all resource types
          listener:
            includeAll: true
          route:
            includeAll: true
          cluster:
            includeAll: true
          secret:
            includeAll: true
        post:
          # Required: Enable post-translation hooks
          - Translation
          - Cluster
          - Route
    service:
      fqdn:
        # IMPORTANT: Update this to match your AI Gateway controller service
        # Format: <service-name>.<namespace>.svc.cluster.local
        # Default if you followed the installation steps above:
        hostname: ai-gateway-controller.envoy-ai-gateway-system.svc.cluster.local
        port: 1063
```

</details>

**What to customize:**

- `extensionManager.service.fqdn.hostname`: If you installed AI Gateway in a different namespace or with a different name, update this value to match your deployment.

:::tip Adding Features with Addons

Need additional features like rate limiting or InferencePool? You can add them using addon values files:

- **Rate Limiting**: See the [token_ratelimit example](https://github.com/envoyproxy/ai-gateway/tree/main/examples/token_ratelimit) for setup with Redis and rate limiting configuration
- **InferencePool**: See the [inference-pool example](https://github.com/envoyproxy/ai-gateway/tree/main/examples/inference-pool) for intelligent request routing across multiple endpoints

These examples show how to combine the base configuration with addon files using multiple `-f` flags.

:::

:::tip Verify Installation

Check the status of the pods. All pods should be in the `Running` state with `Ready` status.

Check AI Gateway pods:

```shell
kubectl get pods -n envoy-ai-gateway-system
```

Check Envoy Gateway pods:

```shell
kubectl get pods -n envoy-gateway-system
```

:::

## Next Steps

After completing the installation:

- Continue to [Basic Usage](./basic-usage.md) to learn how to make your first request
- Or jump to [Connect Providers](./connect-providers) to set up OpenAI and AWS Bedrock integration

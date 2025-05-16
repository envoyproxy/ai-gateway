---
id: installation
title: Installation
sidebar_position: 3
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide will walk you through installing Envoy AI Gateway and its required components.

## Installing Envoy AI Gateway

The easiest way to install Envoy AI Gateway is using the Helm chart. First, install the AI Gateway Helm chart and wait for the deployment to be ready:

```shell
helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm \
    --version v0.0.0-latest \
    --namespace envoy-ai-gateway-system \
    --create-namespace

helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
    --version v0.0.0-latest \
    --namespace envoy-ai-gateway-system \
    --create-namespace

kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=condition=Available
```

## Configuring Envoy Gateway

After installing Envoy AI Gateway, apply the AI Gateway-specific configuration to Envoy Gateway, restart the deployment, and wait for it to be ready:

```shell
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/redis.yaml
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/config.yaml
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/rbac.yaml

kubectl rollout restart -n envoy-gateway-system deployment/envoy-gateway

kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

Note that the redis configuration is only used for the rate limiting feature. If you don't need rate limiting, you can skip the redis configuration,
but you need to remove the relevant configuration in the `config.yaml` file as well.

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

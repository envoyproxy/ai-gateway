---
id: installation
title: Installation
sidebar_position: 3
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

This guide will walk you through installing Envoy AI Gateway and its required components.

## Installing Envoy AI Gateway

The easiest way to install Envoy AI Gateway is using the Helm charts. You need to install the CRDs first, followed by the AI Gateway controller.

### Step 1: Install AI Gateway CRDs

First, install the CRD Helm chart (`ai-gateway-crds-helm`) which manages all Custom Resource Definitions:

```shell
helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm \
  --version v0.4.0 \
  --namespace envoy-ai-gateway-system \
  --create-namespace
```

### Step 2: Install AI Gateway Resources

After the CRDs are installed, install the AI Gateway Helm chart:

```shell
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.4.0 \
  --namespace envoy-ai-gateway-system \
  --create-namespace

kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=condition=Available
```

> If you are experiencing network issues with `docker.io`, you can install the helm charts from the code repo [ai-gateway-crds-helm](https://github.com/envoyproxy/ai-gateway/tree/v0.4.0/manifests/charts/ai-gateway-crds-helm) and [ai-gateway-helm](https://github.com/envoyproxy/ai-gateway/tree/v0.4.0/manifests/charts/ai-gateway-helm) instead.

:::tip Verify Installation

Check the status of the pods. All pods should be in the `Running` state with `Ready` status.

Check AI Gateway pods:

```shell
kubectl get pods -n envoy-ai-gateway-system
```

:::

:::note Upgrading from Previous Versions

If you installed AI Gateway with only `ai-gateway-helm` previously, first install the CRD chart with `--take-ownership` to transfer CRD ownership, then upgrade the main chart:

```shell
helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm --version v0.4.0 --namespace envoy-ai-gateway-system --take-ownership
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm --version v0.4.0 --namespace envoy-ai-gateway-system
```

:::

## Next Steps

After completing the installation:

- Continue to [Basic Usage](./basic-usage.md) to learn how to make your first request
- Or jump to [Connect Providers](./connect-providers) to set up OpenAI and AWS Bedrock integration

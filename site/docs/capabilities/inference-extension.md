---
id: gateway-api-inference-extension
title: Gateway API Inference Extension
sidebar_position: 7
---

[Gateway API Inference Extension](https://gateway-api-inference-extension.sigs.k8s.io) is a new extension of the Kubernetes Gateway API that aims to address serving large language models (LLMs) inside Kubernetes, with particular focus on intelligent load balancing decisions.
Envoy AI Gateway was, on the other hand, initially designed to route traffic to different AI providers outside a k8s cluster, serving primarily as an egress solution.

To make Envoy AI Gateway a comprehensive solution for all AI traffic management, we have support for the Gateway API Inference Extension that can be used together with Envoy AI Gateway APIs.

:::note
This features is experimental and currently under active development.
:::

## Setup

Before you begin, you'll need to complete the [installation](../getting-started/installation.md) guide.
To set up the Gateway API Inference Extension, you need to run the Envoy AI Gateway with the `--enableInferenceExtension=true` flag, which can be done by setting the `controller.enableInferenceExtension` Helm chart value to `true` like this:

```
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  --set controller.enableInferenceExtension=true \
  --create-namespace
```

The Inference Extension is essentially a set of custom resources, so you need to install its CRDs in your cluster. You can do this by running the following command:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/v0.2.0/manifests.yaml
```

## How to use

### Configure AIGatewayRoute to use InferencePool

With [AIGatewayRoute](../api/api.mdx#aigatewayrouterule) resource, you will specify a destination backend for each routing rule with [AIGatewayRouteRuleBackendRef](../api/api.mdx#aigatewayrouterulebackendref).
The reference defaults to [AIServiceBackend](../api/api.mdx#aiservicebackend) resource type, which is the standard resource for Envoy AI Gateway.
When the Inference Extension is enabled, you can also specify [InferencePool](https://gateway-api-inference-extension.sigs.k8s.io/reference/spec/#inferencepool) object as a destination backend.

An inference pool is a collection of endpoints that can serve one or more AI models.
In our implementation, you can bundle multiple AIServiceBackend resources into a single InferencePool resource that can server the same set of "models", and Envoy AI Gateway will intelligently load balance the traffic to the endpoints in the pool.

For example, let's say you have the following rules in your AIGatewayRoute

```yaml
rules:
- matches:
  - headers:
    - type: Exact
      name: x-target-inference-extension
      value: "yes"
  backendRefs:
    # The name of the InferencePool that this route will route to.
  - name: inference-extension-example-pool
    # Explicitly specify the kind of the backend to be InferencePool.
    kind: InferencePool
- matches:
  - headers:
    - type: Exact
      name: x-target-inference-extension
      value: "no"
  backendRefs:
    # The name of the AIServiceBackend that this route will route to.
  - name: my-ai-service-backend
    # This is optional and defaults to AIServiceBackend.
    # kind: AIServiceBackend
```

When a request comes in with the header `x-target-inference-extension: yes`, it will be routed to the InferencePool named `inference-extension-example-pool`.
That eventually routes to an AIServiceBackend resource that are part of the pool.

On the other hand, if the request comes in with the header `x-target-inference-extension: no`, it will be routed to the AIServiceBackend named `my-ai-service-backend` without going through the InferencePool.
That means that the request will be sent directly to the backend service without any AI specific load balancing.

### Configure InferencePool

An InferencePool is defined to bundle multiple AIServiceBackend resources that can serve the same set of models.

The set of models is defined via [InferenceModel](https://gateway-api-inference-extension.sigs.k8s.io/reference/spec/#inferencemodel) that is part of the Inference Extension.
Multiple InferenceModel resources can reference the same InferencePool. Please refer to the [Inference Extension documentation](https://gateway-api-inference-extension.sigs.k8s.io/) for more details.

To specify multiple AIServiceBackend resources in an InferencePool, you can use the `spec.selector` field in the InferencePool resource.
For example,

```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: inference-extension-example-pool
spec:
  selector:
    # Select multiple AIServiceBackend objects to bind to the InferencePool.
    app: my-backend
```

this will select all AIServiceBackend resources with the label `app: my-backend` and bind them to the InferencePool.

## What's next?

We have a [full example configuration](https://github.com/envoyproxy/ai-gateway/blob/main/examples/inference_extension/inference_extension.yaml) that demonstrates how to use the Inference Extension with Envoy AI Gateway.
Feel free to check it out and modify it to suit your needs.

# Support Integration with Endpoint Picker (GIE)

+ author: [Xunzhuo](https://github.com/xunzhuo)

## Table of Contents

<!-- toc -->

-   [Summary](#summary)
-   [Goals](#goals)
-   [Background](#background)
    -   [How EPP works?](#how-epp-works)
    -   [How Envoy works with GIE](#how-envoy-works-with-gie)
-   [Design](#design)
    -   [Resource Relation](#resource-relation)
    -   [Configuration Generation](#configuration-generation)
    -   [Work with Envoy Gateway](#aigatewayroute)

<!-- /toc -->

## Summary

This propopal aims to land integration with other endpoint picker in Envoy AI Gateway, expand EAGW abilities with other EPP implementations, like Gateway API Inference Extension, AIBrix Plugin, semantic router etc.

This is a core functionality in EAGW`s vision, make the routing more intelligent.

![](http://liuxunzhuo.oss-cn-chengdu.aliyuncs.com/2025-06-25-090714.png)

## Goals
+ Integrate with EPP to expand the Envoy AI Gateway abilities
+ Integrate with the existing CRD and features well

## Background

Before starting the design of EAGW + GIE integration, let us figure out how GIE works with DP Envoy and CP.

### How EPP works?

Take the [Gateway API Inference Extension](https://gateway-api-inference-extension.sigs.k8s.io/) as an example:

![](http://liuxunzhuo.oss-cn-chengdu.aliyuncs.com/2025-06-25-090551.png)

When request goes to envoyproxy, it goes to the http filter chain, the ext-proc filter calls an ext-proc upstream cluster, which connects to an external gRPC service, we call that endpoint picker(EPP).

The gRPC service info is pre-defined in [InferencePool](https://gateway-api-inference-extension.sigs.k8s.io/api-types/inferencepool/) extensionRef, giving an example below:

```
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetPortNumber: 8000
  selector:
    app: vllm-llama3-8b-instruct
  extensionRef:
    name: vllm-llama3-8b-instruct-epp
    port: 9002
    failureMode: FailClose
```

The control plane will generate the corresponding ext proc config (filter + cluster) to envoy, Take the inferencePool above as an example, the destination would be `vllm-llama3-8b-instruct-epp:9002` in the same namespace with the InferencePool.

![](http://liuxunzhuo.oss-cn-chengdu.aliyuncs.com/2025-06-25-090607.png)

Based on the routing rules, the CP patches the ext-proc per-route config to the routes, and when request is matched with the rule, the request goes to the EPP(ext-proc). Take the HTTPRoute as an example, the CP will apply the per-route ext-proc filter according to the `/` matches rule.

```
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: llm-route
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: inference-gateway
  rules:
  - backendRefs:
    - group: inference.networking.x-k8s.io
      kind: InferencePool
      name: vllm-llama3-8b-instruct
    matches:
    - path:
        type: PathPrefix
        value: /
    timeouts:
      request: 300s
```

Then when requests go to EPP, it calculates the best match backend inference endpoint among the inference pool based on the the [InferencePool](https://gateway-api-inference-extension.sigs.k8s.io/api-types/inferencepool/) Selector.

Take the inferencePool above as an example, the pods are selected by label `app=vllm-llama3-8b-instruct`.

When EPP decides which endpoint (use `1.2.3.4` as an example) is the best, it patches the existing connections with some tricks like

1. adding headers: `x-gateway-destination-endpoint`:`1.2.3.4`
1. adding metadata: `envoy.lb`: `1.2.3.4`

Then everything the EPP can do is done, envoyproxy is going to work, the logics is the next section.

### How Envoy works with GIE?

EnvoyProxy is the data plane who actually forwards the request, unlike the ways we forward the traffic to the kubernetes service/endpoints, the destination is unknown for the control plane, so it cannot be pre-generated to EnvoyProxy.

Above section tells, the destination is chosen by EPP and the information is located in header and metadata, so the way envoy determines is to read the header or the metadata to pick the target endpoint.

There are two approaches envoy can work in this scenario:

#### Based on LoadBalancingPolicy of override_host

For more details see: [docs](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/load_balancing_policies/override_host/v3/override_host.proto#extensions-load-balancing-policies-override-host-v3-overridehost)

example:

```
  loadBalancingPolicy:
    policies:
    - typedExtensionConfig:
        name: envoy.load_balancing_policies.override_host
        typedConfig:
          '@type': type.googleapis.com/envoy.extensions.load_balancing_policies.override_host.v3.OverrideHost
          overrideHostSources:
          - header: x-gateway-destination-endpoint
```

#### Based on ORIGINAL_DST Cluster

For more details see: [docs](https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/original_dst#arch-overview-load-balancing-types-original-destination)

example:

```
        name: original_destination_cluster
        type: ORIGINAL_DST
        original_dst_lb_config:
          use_http_header: true
          http_header_name: x-gateway-destination-endpoint
        connect_timeout: 6s
        lb_policy: CLUSTER_PROVIDED
        dns_lookup_family: V4_ONLY
```

## Design

This section discusses how Envoy AI Gateway integrates with EPP.

### Resource Relation

Envoy AI Gateway currently provides a special xRoute called AIGatewayRoute, take a quick Look:

```
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
              value: gpt-4o-mini
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
```

The backendRef is default referred with the AIServiceBackend:

```
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: envoy-ai-gateway-basic-openai
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: envoy-ai-gateway-basic-openai
    kind: Backend
    group: gateway.envoyproxy.io
  backendSecurityPolicyRef:
    name: envoy-ai-gateway-basic-openai-apikey
    kind: BackendSecurityPolicy
    group: aigateway.envoyproxy.io
```

To integrate with the GIE, there are two options:

#### Option 1: Add InferencePool as an backendRef on AIGatewayRoute Level

This requires to expand the `AIGatewayRouteRuleBackendRef` with `BackendObjectReference`

##### Current

```
// AIGatewayRouteRuleBackendRef is a reference to a backend with a weight.
type AIGatewayRouteRuleBackendRef struct {
	// Name is the name of the AIServiceBackend.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Name of the model in the backend. If provided this will override the name provided in the request.
	ModelNameOverride string `json:"modelNameOverride,omitempty"`

	// Weight is the weight of the AIServiceBackend. This is exactly the same as the weight in
	// the BackendRef in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.BackendRef
	//
	// Default is 1.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	Weight *int32 `json:"weight,omitempty"`
	// Priority is the priority of the AIServiceBackend. This sets the priority on the underlying endpoints.
	// See: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/priority
	// Note: This will override the `faillback` property of the underlying Envoy Gateway Backend
	//
	// Default is 0.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority *uint32 `json:"priority,omitempty"`
}
```

##### Target

```
// AIGatewayRouteRuleBackendRef is a reference to a backend with a weight.
type AIGatewayRouteRuleBackendRef struct {

	gwapiv1.BackendObjectReference

	// Name of the model in the backend. If provided this will override the name provided in the request.
	ModelNameOverride string `json:"modelNameOverride,omitempty"`

	// Weight is the weight of the AIServiceBackend. This is exactly the same as the weight in
	// the BackendRef in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.BackendRef
	//
	// Default is 1.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	Weight *int32 `json:"weight,omitempty"`
	// Priority is the priority of the AIServiceBackend. This sets the priority on the underlying endpoints.
	// See: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/priority
	// Note: This will override the `faillback` property of the underlying Envoy Gateway Backend
	//
	// Default is 0.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority *uint32 `json:"priority,omitempty"`
}

```

##### Example

+ When it matches gpt-4o-mini goes to AIServiceBackend `envoy-ai-gateway-basic-openai`
+ When it matches vllm-llama3-8b-instruct goes to InferencePool `vllm-llama3-8b-instruct`

```
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetPortNumber: 8000
  selector:
    app: vllm-llama3-8b-instruct
  extensionRef:
    name: vllm-llama3-8b-instruct-epp
    port: 9002
    failureMode: FailClose
---
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
              value: gpt-4o-mini
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: vllm-llama3-8b-instruct
      backendRefs:
        - name: vllm-llama3-8b-instruct
        	group: inference.networking.x-k8s.io
          kind: InferencePool
```

#### Option 2: Add InferencePool as an backendRef on AIServiceBackend Level

This requires to expand the `AIServiceBackend` backendRef supports the InferencePool, considering current AIServiceBackend BackendRef is `gwapiv1.BackendObjectReference`, so we don't need any changes on it.

##### Current

```
type AIServiceBackendSpec struct {
	// APISchema specifies the API schema of the output format of requests from
	// Envoy that this AIServiceBackend can accept as incoming requests.
	// Based on this schema, the ai-gateway will perform the necessary transformation for
	// the pair of AIGatewayRouteSpec.APISchema and AIServiceBackendSpec.APISchema.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	APISchema VersionedAPISchema `json:"schema"`
	// BackendRef is the reference to the Backend resource that this AIServiceBackend corresponds to.
	//
	// A backend must be a Backend resource of Envoy Gateway. Note that k8s Service will be supported
	// as a backend in the future.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	BackendRef gwapiv1.BackendObjectReference `json:"backendRef"`

	// BackendSecurityPolicyRef is the name of the BackendSecurityPolicy resources this backend
	// is being attached to.
	//
	// +optional
	BackendSecurityPolicyRef *gwapiv1.LocalObjectReference `json:"backendSecurityPolicyRef,omitempty"`

	// TODO: maybe add backend-level LLMRequestCost configuration that overrides the AIGatewayRoute-level LLMRequestCost.
	// 	That may be useful for the backend that has a different cost calculation logic.
}
```

##### Example

+ When it matches gpt-4o-mini goes to AIServiceBackend `envoy-ai-gateway-basic-openai`
+ When it matches vllm-llama3-8b-instruct goes to AIServiceBackend `vllm-llama3-8b-instruct`

```
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetPortNumber: 8000
  selector:
    app: vllm-llama3-8b-instruct
  extensionRef:
    name: vllm-llama3-8b-instruct-epp
    port: 9002
    failureMode: FailClose
---
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
              value: gpt-4o-mini
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: vllm-llama3-8b-instruct
      backendRefs:
        - name: vllm-llama3-8b-instruct
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: vllm-llama3-8b-instruct
spec:
  schema:
    name: OpenAI
  backendRef:
    name: vllm-llama3-8b-instruct
    group: inference.networking.x-k8s.io
    kind: InferencePool
```

#### Configuration Generation

no matter which one we decide for the above two approaches, we need to figure out how/what configuration we need to generate.

based on the background, we need to generate such configurations:

##### ext-proc config

+ generate ext-proc cluster based on the InferencePool extensionRef
+ generate ext-proc http filter based on the InferencePool extensionRef

##### route level config

+ patch the ext-proc filter into the route configuration based on which route the InferencePool is linked with
+ add the cluster with loadbalancing policy or ORIGINAL_DST to understand the header and route  `x-gateway-destination-endpoint`

#### Resource Generation

This section is about how to manage the GIE kubernetes resource, in short, there are two approaches, static or dynamic.

+ static: end-user manage the GIE deployment, service etc resource.
+ dynamic: end-user do not care about the life cycle of GIE resources, Envoy AI Gateway take care of that.

#### Work with Envoy Gateway

There are two work-in-process PRs in upstream:

+ https://github.com/envoyproxy/gateway/pull/6271
+ https://github.com/envoyproxy/gateway/pull/6342

##### Backend + EEP

Reference: https://github.com/envoyproxy/gateway/pull/6271

The first one, introduces a new Backend Type called HostOverride, it can be referred by HTTPRoute:

```
 apiVersion: gateway.envoyproxy.io/v1alpha1
  kind: Backend
  metadata:
    name: backend-routing-based-on-header
    namespace: default
  spec:
    type: HostOverride
    hostOverrideSettings:
      overrideHostSources:
      - header: x-gateway-destination-endpoint
```

It adds the the cluster with override_host loadBalancingPolicy, we can add the host based routing strategy like above, routing based on the endpoint in header `x-gateway-destination-endpoint`

Take the configuration below as an example:

```
apiVersion: inference.networking.x-k8s.io/v1alpha2
kind: InferencePool
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetPortNumber: 8000
  selector:
    app: vllm-llama3-8b-instruct
  extensionRef:
    name: vllm-llama3-8b-instruct-epp
    port: 9002
    failureMode: FailClose
---
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
              value: vllm-llama3-8b-instruct
      backendRefs:
        - name: vllm-llama3-8b-instruct
        	group: inference.networking.x-k8s.io
          kind: InferencePool
```

When EAGW found this situation, it will generate HTTPRoute + Backend + EPP:

```
  apiVersion: gateway.networking.k8s.io/v1
  kind: HTTPRoute
  metadata:
    name: envoy-ai-gateway-basic
  spec:
    parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: envoy-ai-gateway-basic
      namespace: default
    rules:
    - backendRefs:
      - group: gateway.envoyproxy.io
        kind: Backend
        name: vllm-llama3-8b-instruct
        weight: 1
      filters:
      - extensionRef:
          group: gateway.envoyproxy.io
          kind: HTTPRouteFilter
          name: ai-eg-host-rewrite
        type: ExtensionRef
      matches:
      - headers:
        - name: x-ai-eg-selected-route
          type: Exact
          value: envoy-ai-gateway-basic-rule-0
        path:
          type: PathPrefix
          value: /
    - matches:
      - path:
          type: PathPrefix
          value: /
      name: unreachable
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: vllm-llama3-8b-instruct
spec:
  type: HostOverride
  hostOverrideSettings:
    overrideHostSources:
    - header: x-gateway-destination-endpoint
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyExtensionPolicy
metadata:
  name: vllm-llama3-8b-instruct
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: envoy-ai-gateway-basic
  extProc:
    - backendRefs:
        - name: vllm-llama3-8b-instruct-epp
          port: 9002
      processingMode:
        request:
          body: Buffered
        response:
          body: Streamed
      messageTimeout: 5s
```

This direction is to reuse the abilities of Envoy Gateway, and generate the Backend and EnvoyExtensionPolicy to deal with the InferencePool

##### EnvoyExtensionServer

The second one, introduces the abilities for define the custom BackendRef in Envoy Gateway, and send that with the gRPC call, and the extension server in Envoy AI Gateway, edits the cluster/route and send it back to Envoy Gateway xDS translator.

Reference: https://github.com/envoyproxy/gateway/pull/6342

Workflow is like:

**Envoy Gateway**

1. define the custom backend resource in Envoy Gateway configuration
2. Envoy Gateway watches that resource
3. If httproute refers any resource with the same GVK, carry it with BackendExtensionRefs IR
4. When EG doing xDS translation, checks if BackendExtensionRefs > 0, if so, it calls the PostRouteModifyHook and carry the unstructuredResources(InferencePool) to Envoy AI Gateway

**Envoy AI Gateway**

1. Implement the PostRouteModifyHook, iterates the unstructuredResources to group the inferencePool
2. Add the cluster  with override_host loadBalancingPolicy, and edits the route to link with the cluster
3. Send it back to Envoy Gateway
4. Envoy Gateway xDS Server pushes the config to EnvoyProxy

##### Question?

How do we manage the ext-proc configuration, since the PostRouteModifyHook don't carry the listener, then we cannot add ext-proc http filter over there? Solve by another Hook? Or generate the EnvoyExtensionPolicy, if so what is the advantages compared to the first one?


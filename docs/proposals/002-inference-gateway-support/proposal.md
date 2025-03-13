# Inference Gateway API Extension Support

## Background

[Gateway API Inference Extension](https://gateway-api-inference-extension.sigs.k8s.io) (GAIE) is a brand new extension of
the Gateway API that aims to solve the problem of serving large language models (LLMs) inside Kubernetes. Especially,
it specifies a way to make an intelligent load balancing decision etc.

Envoy AI Gateway (EAIG) 's initial goal was, in contrast, to provide a way to route traffic to different AI providers.
In other words, EAIG is closer to the application developers vs GAIE is closer to the AI platform team. Or, EAIG is
more focused on the "egress" vs GAIE is more focused on the "ingress" (especially k8s).

Even though, there's a clear difference between these two projects, some of the capability GAIE specifying can be useful for EAIG
users as well. For example, [Endpoint picker with fallback and subset](https://github.com/kubernetes-sigs/gateway-api-inference-extension/pull/445) is a nice feature that can be leveraged and one of the
oldest feature requests for EAIG.

This document is about two key points; how EAIG project's scope can be changed to support GAIE,
and how it integrates with the existing API.

## Changes to the scope of the project

Initially, the project was focused on routing traffic to different AI providers, namely "egress" traffic. However, with the
introduction of GAIE, we need to expand it to support "ingress" traffic as well. This means EAIG will evolve to support
"all AI traffic" instead of just "egress AI traffic".

However, this doesn't mean that we need to have completely different APIs for "ingress" and "egress". Instead, we will ensure
that the existing APIs nicely integrate with GAIE APIs.

## How it integrates with the existing API

We propose to allow the existing `AIServiceBackend.BackendRef` to reference an `InferencePool`. This will allow
the users to leverage the new features of GAIE without changing the existing APIs. Even the translation and security policy
related functionalities can be applied as is.

```diff
--- a/api/v1alpha1/api.go
+++ b/api/v1alpha1/api.go
@@ -334,7 +334,7 @@ type AIServiceBackendSpec struct {
        APISchema VersionedAPISchema `json:"schema"`
        // BackendRef is the reference to the Backend resource that this AIServiceBackend corresponds to.
        //
-       // A backend can be of either k8s Service or Backend resource of Envoy Gateway.
+       // A backend can be of either k8s Service, Backend resource of Envoy Gateway, or InferencePool of Gateway API Inference Extension.
        //
        // This is required to be set.
        //
```

This will also ensure that we have only one configuration API layer for both "ingress" and "egress" traffic. Otherwise, we would end up maintaining two different APIs without
for different types of traffic.

## Implementation Notes

* The "BackendRef" pointing to an InferencePool will utilize the endpoint picker mechanism as described [here](https://github.com/kubernetes-sigs/gateway-api-inference-extension/pull/445). In its implementation, this will rely on the new load balancing policy that uses dynamic metadata to make decisions.
* We will not change the abstraction where the extproc lives currently: it will be free of the k8s/control plane specific details and the `filterapi` layers should contain the enough configuration to support the load balancing policy for GAIE.
* We can conditionally disable the translation and buffering only when the AIGatewayRoute is referencing only one InferencePool based AIServiceBackend and the translation is not needed. This will be aligned with the reference implementation of GAIE, but that will be an optimization that can be done later.

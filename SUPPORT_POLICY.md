# Support Policy

This document focuses on compatibility concerns of those using Envoy AI Gateway.
It is important to note that the support policy is subject to change at any time. The support policy is as follows:

First of all, there are four areas of compatibility that we are concerned with:
* [Using envoyproxy/ai-gateway as a Go package](#public-go-package).
* [Deploying the Envoy AI Gateway controller through the Kubernetes Custom Resource Definition (CRD)](#Custom-Resource-Definitions).
* [Upgrading the Envoy AI Gateway controller](#Upgrading-the-Envoy-AI-Gateway-controller).
* [Envoy Gateway vs Envoy AI Gateway compatibility](#Envoy-Gateway-vs-Envoy-AI-Gateway-compatibility).

# Public Go package

Since we do not envision this repository ends up as a transitive dependency, i.e. only used as a direct dependency such as
in a custom control plane, etc., we assume that any consumer of the project should have the full control over the
source code depending on the project. This allows us to declare deprecation and introduce the breaking changes
in the version after the next one since they can migrate the code at their discretion. For example, any public API that is
marked as deprecated in the version N will be removed in the version N+2. We document how users should
migrate to the new API will be documented in the release notes if applicable, but we do not guarantee that the migration
path will be provided.

# Custom Resource Definitions

The Custom Resource Definitions (CRDs) are defined in api/${version}/*.go files. The CRDs are versioned as v1alpha1, v1alpha2, etc.
**For alpha versions**, we simply employ the same deprecation policy as the Go package. In other words, the APIs will be marked as
deprecated in the version N and will be removed in the version N+2 but without any guarantee of migration path.
Migration paths for alpha versions will be the best effort and will be documented in the release notes.
**For beta versions**, For beta versions, it is the same as the alpha versions, but we will provide a migration path in the release notes.
**For stable versions**, we will never break the APIs unless there is a critical security issue.
We will provide a migration path in the release notes in case we need to break the APIs.

# Upgrading the Envoy AI Gateway controller

We guarantee that simply upgrading the controller will not break the existing configuration assuming there's
no _un-migrated_ resources including breaking change left in the k8s API server. In other words, after the
proper use of the API and migration path described above, the user should be able to upgrade the controller
without any issue. However, this does mean that we do NOT guarantee that the existing configuration will work
across more than one version of the controller. For example if you are using the version N of the controller,
and you want to upgrade to the version N+2, you should first upgrade to the version N+1 while following the
migration path if applicable, and then upgrade to the version N+2.

# Envoy Gateway vs Envoy AI Gateway compatibility

Since Envoy AI Gateway is built on top of Envoy Gateway, the compatibility between the two is important.
We use the latest released version of Envoy Gateway as the base of the Envoy AI Gateway when we release a new version.
Since Envoy Gateway is a stable project and supposed to work across versions, we do not expect any compatibility issue
as long as the Envoy Gateway version is also up-to-date prior to the upgrade of the Envoy AI Gateway.

---
id: compatibility
title: Compatibility Matrix
sidebar_position: 6
---

# Compatibility Matrix

This document provides compatibility information for Envoy AI Gateway releases with their dependencies.

| AI Gateway | Envoy Gateway                 | Kubernetes | Gateway API | Support Status |
| ---------- | ----------------------------- | ---------- | ----------- | -------------- |
| main       | v1.6.x+ (Envoy Proxy v1.36.x) | v1.32+     | v1.4.x      | Development    |
| v0.5.x     | v1.6.x+ (Envoy Proxy v1.35.x) | v1.32+     | v1.4.x      | Supported      |
| others     | N/A                           | N/A        | N/A         | End of Life    |

Note that "compatibility" means that these specific combinations have been tested and verified to work together.
Other versions may work but are not officially supported.
Please refer to our [Support Policy](https://github.com/envoyproxy/ai-gateway/blob/main/RELEASES.md#support-policy) for more details
on how we manage releases and support for different versions.

To upgrade to a new Envoy AI Gateway version, make sure upgrade your dependencies accordingly to maintain compatibility, especially make sure that
Envoy Gateway and Gateway API versions are up-to-date as per the compatibility matrix above. Then, upgrade the AI Gateway using the standard helm upgrade process.

## Upgrading and Migration

### Helm Upgrade Commands

```bash
# 1. Upgrade CRDs
helm upgrade aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm \
  --version \
  envoy-ai-gateway-system < NEW_VERSION > -n

# 2. Upgrade application
helm upgrade aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version \
  envoy-ai-gateway-system < NEW_VERSION > -n
```

### Migrating from v1alpha1 to v1beta1

AIGatewayRoute, AIServiceBackend, BackendSecurityPolicy, and GatewayConfig support both v1alpha1 (deprecated) and v1beta1. When you upgrade:

- **Existing v1alpha1 resources** continue to work. The API server migrates stored data to v1beta1 automatically.
- **No user action required** for existing resources; they remain functional.
- **New resources** should use `apiVersion: aigateway.envoyproxy.io/v1beta1`.
- **MCPRoute and QuotaPolicy** remain v1alpha1-only (no v1beta1 available).

To adopt v1beta1 in your manifests, change the `apiVersion` field and re-apply:

```yaml
# Before (v1alpha1, deprecated)
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
# ...

# After (v1beta1, preferred)
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
# ...
```

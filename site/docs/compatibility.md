---
id: compatibility
title: Compatibility Matrix
sidebar_position: 6
---

# Compatibility Matrix

This document provides compatibility information for Envoy AI Gateway releases with their dependencies.

## Version Compatibility

The following table shows the compatibility between Envoy AI Gateway versions and their required dependencies:

| AI Gateway | Envoy Gateway | Kubernetes | Gateway API | API Version | Support Status |
| ---------- | ------------- | ---------- | ----------- | ----------- | -------------- |
| main       | v1.6.x+       | v1.32+     | v1.4.0      | v1alpha1    | Development    |
| v0.4.x     | v1.5.x+       | v1.29+     | v1.4.0      | v1alpha1    | Supported      |
| v0.3.x     | v1.5.x+       | v1.29+     | v1.4.0      | v1alpha1    | Supported      |
| v0.2.x     | v1.4.x+       | v1.29+     | v1.2.0      | v1alpha1    | End of Life    |
| v0.1.x     | v1.3.x+       | v1.29+     | v1.2.0      | v1alpha1    | End of Life    |

## Tested Configurations

The following configurations are actively tested in CI:

### Envoy Gateway Versions

- **v1.6.0**: Primary tested version for stable releases
- **latest (main)**: Tested to ensure forward compatibility

### Kubernetes Versions

- **v1.32.x**: Minimum supported version for main branch
- **v1.33.x**: Latest tested version

## Support Policy

### Supported Versions

We maintain support for the **two most recent minor versions** of Envoy AI Gateway. When a new minor version is released, the oldest supported version reaches End of Life (EOL).

For example:

- When v0.3.0 was released, v0.1.x reached EOL
- When v0.4.0 was released, v0.2.x reached EOL

### Upgrade Path

We guarantee that upgrading between consecutive versions will work without issues, provided you follow any migration steps documented in the release notes. For example:

- v0.1.x -> v0.2.x: Supported
- v0.2.x -> v0.3.x: Supported
- v0.1.x -> v0.3.x: Not directly supported (upgrade through v0.2.x first)

For detailed upgrade instructions, see the release notes for each version.

## Dependency Details

### Envoy Gateway

Envoy AI Gateway is built on top of [Envoy Gateway](https://gateway.envoyproxy.io/). Each AI Gateway release is tested against specific Envoy Gateway versions:

| AI Gateway | Recommended EG Version | Minimum EG Version |
| ---------- | ---------------------- | ------------------ |
| main       | latest                 | v1.6.0             |
| v0.4.x     | v1.5.4                 | v1.5.0             |
| v0.3.x     | v1.5.0                 | v1.5.0             |
| v0.2.x     | v1.4.1                 | v1.4.0             |
| v0.1.x     | v1.3.1                 | v1.3.0             |

### Gateway API

Envoy AI Gateway uses the Kubernetes Gateway API for routing configuration. The following Gateway API versions are supported:

| AI Gateway | Gateway API Version |
| ---------- | ------------------- |
| main       | v1.4.0              |
| v0.4.x     | v1.4.0              |
| v0.3.x     | v1.4.0              |
| v0.2.x     | v1.2.0              |
| v0.1.x     | v1.2.0              |

### Custom Resource Definitions (CRDs)

All current releases use the `v1alpha1` API version for AI Gateway CRDs. See [API Documentation](/docs/api/api) for details on available resources.

## Additional Notes

### Envoy Proxy

Envoy AI Gateway uses the Envoy Proxy version bundled with Envoy Gateway. You do not need to manage Envoy Proxy versions separately.

### Helm Charts

Helm chart versions are aligned with AI Gateway release versions. Always use matching versions for the CRD and controller Helm charts:

```shell
# For version 0.4.x
helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm --version v0.4.0
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm --version v0.4.0
```

### Development Builds

The `main` branch and `v0.0.0-latest` tags represent development builds that may include breaking changes. For production use, always use tagged releases.

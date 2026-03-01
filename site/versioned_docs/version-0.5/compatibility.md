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
| v0.5.x     | v1.6.x+ (Envoy Proxy v1.36.x) | v1.32+     | v1.4.x      | Supported      |
| others     | N/A                           | N/A        | N/A         | End of Life    |

Note that "compatibility" means that these specific combinations have been tested and verified to work together.
Other versions may work but are not officially supported.
Please refer to our [Support Policy](https://github.com/envoyproxy/ai-gateway/blob/main/RELEASES.md#support-policy) for more details
on how we manage releases and support for different versions.

To upgrade to a new Envoy AI Gateway version, make sure upgrade your dependencies accordingly to maintain compatibility, especially make sure that
Envoy Gateway and Gateway API versions are up-to-date as per the compatibility matrix above. Then, upgrade the AI Gateway using the standard helm upgrade process.

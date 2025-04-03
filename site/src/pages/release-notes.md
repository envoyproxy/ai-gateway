---
title: Release Notes
description: Release Notes for Envoy AI Gateway
---

# Release Notes
---
## v0.1.5
**Date:** 2025-04-03

Fixes case where ext proc image does not sync when ai-gateway route's filter config is not defined.

## Overview

This patch release v0.1.5 introduces a fix to syncing the ext proc image.

## Commits

extproc: set extProcImage before potentially returning #515

---
## v0.1.4
**Date:** 2025-03-20

Fixes case of empty content body from assistant content resulting in aws validation errors.

## Overview

This patch release `v0.1.4` introduces fix to aws validation error when assistant content is empty.

## Commits

- translator: skip adding content if assistant content string is empty (https://github.com/envoyproxy/ai-gateway/pull/508)

---
## v0.1.3
**Date:** 2025-03-14

Fixes streaming for chat completion, adds chat completion metrics, and openai assistant content type.

## Overview

This patch release `v0.1.3` includes fixes to chat completion streaming and openai assistant content type, and adds genai metrics.

## Commits

- extproc: add genai metrics to track token usage and latency (https://github.com/envoyproxy/ai-gateway/pull/459)
- extproc: properly stream chat completions (https://github.com/envoyproxy/ai-gateway/pull/468)
- feat: openai: allow assistant content to be string (https://github.com/envoyproxy/ai-gateway/pull/486)

---
## v0.1.2
**Date:** 2025-03-05

Rotator bug and extproc image tag fix! Docker ci changes as well.

## Overview

This patch release `v0.1.2` includes fixes to BackendSecurityPolicy requeue bug, docker hub changes, and extproc image tag sync.

## Commits

- controller: fix syncing extproc image tag (https://github.com/envoyproxy/ai-gateway/pull/447)
- controller: update rotate function to return expiration time (https://github.com/envoyproxy/ai-gateway/pull/455)
- ci: update dockerhub user (https://github.com/envoyproxy/ai-gateway/pull/441)

---
## v0.1.1
**Date:** 2025-02-28

AWS tooling bug fixes!

## Overview

This patch release `v0.1.1` includes fixes to AWS tooling call, new integration tests for AWS tools, and minor GitHub action validation.

## Commits

e2e: add agent test using tool - https://github.com/envoyproxy/ai-gateway/pull/426
chore: allows backport prefix in commit title - https://github.com/envoyproxy/ai-gateway/pull/443

---
## v0.1.0
**Date:** 2025-02-25

We're excited to announce the first official release of Envoy AI Gateway v0.1.0! This release marks a significant milestone in our journey to provide a secure, scalable, and efficient way to manage GenAI/LLM traffic using Envoy Proxy.

### Overview

Envoy AI Gateway is an open-source project part of the Envoy Ecosystem. It is built on top of [Envoy Gateway](https://gateway.envoyproxy.io/) and [Envoy Proxy](https://www.envoyproxy.io/), designed to simplify how application clients interact with Generative AI services.

It provides a unified layer for routing and managing LLM/AI traffic with built-in security, token based rate limiting, and policy control features.

For installation instructions, see our [Getting Started Guide](https://aigateway.envoyproxy.io/docs/getting-started/installation).

### Features

#### Unified API for LLM Providers
- **Standardized Interface**: Expose a unified API (currently OpenAI-compatible) to clients while routing to different AI service backends
- **Request/Response Transformation**: Automatically transform requests and responses between different provider formats
- **Provider Integration**: Initial support for OpenAI and AWS Bedrock

#### Upstream Authorization
- **API Key Management**: Securely manage API keys for upstream AI providers
- **AWS Credentials Support**: Built-in support for AWS request signing for Bedrock services
- **OIDC Token Exchange**: Support for OIDC-based authentication flows

#### Token-Based Rate Limiting
- **Token Usage Tracking**: Track and limit usage based on tokens rather than just request count
- **Flexible Rate Limit Policies**: Configure limits based on input tokens, output tokens, or total tokens
- **Per-User Limiting**: Apply rate limits per-client and/or user

#### Traffic Management
- **Intelligent Routing**: Route requests to appropriate AI backends based on model name and other criteria
- **Header-Based Routing**: Configure routing rules using HTTP headers and paths
- **Backend Selection**: Flexible backend selection with support for weighted traffic distribution

#### Kubernetes Native
- **Custom Resources**: Define your AI Gateway configuration using Kubernetes Custom Resources
- **Helm Installation**: Easy deployment using Helm charts
- **Integration with Gateway API**: Built on the Kubernetes Gateway API specification


Note that Envoy AI Gateway is built on top of Envoy Gateway and you benefit from all the features of Envoy Gateway and Envoy Proxy. See the [Envoy Gateway documentation](https://gateway.envoyproxy.io) for more information.

### Custom Resources

This release introduces three new Custom Resource Definitions (CRDs):

1. **AIGatewayRoute**: Defines the unified API schema and routing rules to AI service backends
2. **AIServiceBackend**: Specifies the AI service backend schema and connection details
3. **BackendSecurityPolicy**: Configures authentication for upstream AI services

### Documentation

Comprehensive documentation is available at [aigateway.envoyproxy.io](https://aigateway.envoyproxy.io/), including:
- [Getting Started Guide](https://aigateway.envoyproxy.io/docs/getting-started)
- [Architecture Overview](https://aigateway.envoyproxy.io/docs/concepts/architecture)
- [API Reference](https://aigateway.envoyproxy.io/docs/api/)
- [Configuration Examples](https://github.com/envoyproxy/ai-gateway/tree/main/examples)


### Community

Envoy AI Gateway is a community-driven project.
We welcome your contributions and feedback!

- Join our [Slack channel](https://envoyproxy.slack.com/archives/C07Q4N24VAA)
  - [Register for Envoy Slack](https://communityinviter.com/apps/envoyproxy/envoy)
- Attend our [weekly community meetings](https://docs.google.com/document/d/10e1sfsF-3G3Du5nBHGmLjXw5GVMqqCvFDqp_O65B0_w)
- Contribute on [GitHub](https://github.com/envoyproxy/ai-gateway)

### What's Next

We're already working on exciting features for upcoming releases:
- Google Gemini integration
- Provider and model fallback logic

### Acknowledgements

This release wouldn't be possible without the incredible contributions from our community members across Tetrate, Bloomberg, WSO2, RedHat, Google, and our independent contributors.

Thank you for your dedication and support!

---

For more information, visit [aigateway.envoyproxy.io](https://aigateway.envoyproxy.io/) or check out our [GitHub repository](https://github.com/envoyproxy/ai-gateway).

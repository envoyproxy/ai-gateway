# Envoy AI Gateway Examples

This directory contains various examples demonstrating different features and use cases of Envoy AI Gateway.

## Getting Started

### [Basic Setup](./basic/)
A comprehensive example showing how to set up Envoy AI Gateway with multiple providers including OpenAI, AWS Bedrock, and Azure OpenAI.

## Advanced Features

### [Provider Fallback](./provider_fallback/)
Shows how to configure automatic failover between multiple AI providers for high availability.

### [Token Rate Limiting](./token_ratelimit/)
Demonstrates usage-based rate limiting to control costs and prevent abuse.

### [Monitoring](./monitoring/)
Example setup for comprehensive monitoring and observability with Prometheus and Grafana.

## Custom Extensions

### [Custom Metrics](./extproc_custom_metrics/)
Example of implementing custom metrics collection using the external processor interface.

### [Custom Router](./extproc_custom_router/)
Shows how to implement custom routing logic using the external processor interface.

## Quick Start Guide

1. **Choose an example** based on your use case
2. **Follow the README** in each example directory
3. **Apply the configuration** to your Kubernetes cluster
4. **Test the setup** using the provided curl commands

## Prerequisites

All examples assume you have:
- Kubernetes cluster with Envoy Gateway installed
- `kubectl` configured to access your cluster
- Appropriate API keys for the providers you want to use

## Example Structure

Each example typically includes:
- `README.md` - Detailed instructions and explanation
- `*.yaml` - Kubernetes manifests for the configuration
- Test commands and expected responses

## Support

For questions or issues with these examples:
- Check the [documentation](../site/docs/)
- Join the [Envoy AI Gateway Slack channel](https://envoyproxy.slack.com/archives/C07Q4N24VAA)
- Open an issue on [GitHub](https://github.com/envoyproxy/ai-gateway/issues)

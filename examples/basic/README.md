This directory contains basic examples for setting up AI Gateway.

## Files in This Directory

- **`envoy-gateway-values.yaml`**: Minimal Envoy Gateway helm values for AI Gateway integration (without rate limiting). Use this file when installing Envoy Gateway: `helm upgrade -i eg ... -f envoy-gateway-values.yaml`.
- **`basic.yaml`**: Basic AIGatewayRoute example that routes traffic to multiple AI providers.
- **`openai.yaml`**, **`anthropic.yaml`**, **`aws.yaml`**, **`azure_openai.yaml`**, **`gcp_vertex.yaml`**: Provider-specific examples.

## Quick Start

1. Install Envoy Gateway with AI Gateway configuration:
   ```bash
   helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm \
     --version v0.0.0-latest \
     --namespace envoy-gateway-system \
     --create-namespace \
     -f envoy-gateway-values.yaml
   ```

2. Apply a basic example:
   ```bash
   kubectl apply -f basic.yaml
   ```

For detailed documentation, see the [installation guide](https://gateway.envoyproxy.io/ai-gateway/docs/getting-started/installation).

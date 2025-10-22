This contains the basic example manifest to create an Envoy Gateway that handles
the traffics for both OpenAI and AWS Bedrock at the same time.

## Files in This Directory

- **`basic.yaml`**: Basic AIGatewayRoute example that routes traffic to multiple AI providers.
- **`openai.yaml`**, **`anthropic.yaml`**, **`aws.yaml`**, **`azure_openai.yaml`**, **`gcp_vertex.yaml`**: Provider-specific examples.

## Quick Start

Apply any of the example routes:

```bash
kubectl apply -f basic.yaml
```

For detailed documentation, see the [installation guide](https://gateway.envoyproxy.io/ai-gateway/docs/getting-started/installation).

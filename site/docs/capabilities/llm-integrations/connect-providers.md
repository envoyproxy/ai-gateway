---
id: connect-providers
title: Connecting to AI Providers
sidebar_position: 10
---

import CodeBlock from '@theme/CodeBlock';
import BackendOpenAI from '!!raw-loader!./examples/backend-openai.yaml';
import SecurityPolicyAPIKey from '!!raw-loader!./examples/security-policy-apikey.yaml';
import SecurityPolicyAWSEKS from '!!raw-loader!./examples/security-policy-aws-eks.yaml';
import SecurityPolicyAWSStatic from '!!raw-loader!./examples/security-policy-aws-static.yaml';
import SecurityPolicyAzure from '!!raw-loader!./examples/security-policy-azure.yaml';
import SecurityPolicyGCPSA from '!!raw-loader!./examples/security-policy-gcp-sa.yaml';
import SecurityPolicyGCPWIF from '!!raw-loader!./examples/security-policy-gcp-wif.yaml';
import RouteBasic from '!!raw-loader!./examples/route-basic.yaml';
import RouteSingleProvider from '!!raw-loader!./examples/route-single-provider.yaml';
import RouteFallback from '!!raw-loader!./examples/route-fallback.yaml';
import RouteModelSpecific from '!!raw-loader!./examples/route-model-specific.yaml';
import RouteModelMetadata from '!!raw-loader!./examples/route-model-metadata.yaml';

# Connecting to AI Providers

Envoy AI Gateway provides a unified interface for connecting to multiple AI providers through a standardized configuration approach. This page explains the fundamental concepts, resources, and relationships required to establish connectivity with any supported AI provider.

## Overview

Establishing connectivity with an AI provider involves configuring three key Kubernetes resources that work together to enable secure, scalable access to AI services:

1. **AIServiceBackend** - Defines the backend service and its API schema
2. **BackendSecurityPolicy** - Configures authentication credentials
3. **AIGatewayRoute** - Routes client requests to the appropriate backends

These resources provide a consistent configuration model regardless of which AI provider you're connecting to, whether it's OpenAI, AWS Bedrock, Azure OpenAI, or any other [supported provider](./supported-providers).

## Core Resources for Provider Connectivity

### AIServiceBackend

The `AIServiceBackend` resource represents an individual AI service backend and serves as the bridge between your gateway and the AI provider's API.

#### Purpose and Configuration

- **API Schema Definition**: Specifies which API format the backend expects (OpenAI v1, AWS Bedrock, Azure OpenAI, etc.)
- **Backend Reference**: Points to the Envoy Gateway Backend resource
- **Security Integration**: Links to authentication policies for upstream services

#### Key Fields

<CodeBlock language="yaml">{BackendOpenAI}</CodeBlock>

#### Schema Configuration Examples

Different providers require different schema configurations:

| Provider                   | Schema Configuration                                      |
| -------------------------- | --------------------------------------------------------- |
| OpenAI                     | `{"name":"OpenAI","version":"v1"}`                        |
| AWS Bedrock                | `{"name":"AWSBedrock"}`                                   |
| Azure OpenAI               | `{"name":"AzureOpenAI","version":"2025-01-01-preview"}`   |
| GCP Vertex AI              | `{"name":"GCPVertexAI"}`                                  |
| GCP Anthropic on Vertex AI | `{"name":"GCPAnthropic", "version": "vertex-2023-10-16"}` |

:::tip
Many providers offer OpenAI-compatible APIs, which allows them to use the OpenAI schema configuration with provider-specific version paths.
:::

### BackendSecurityPolicy

The `BackendSecurityPolicy` resource configures authentication credentials needed to access upstream AI services securely.

#### Purpose and Configuration

- **Credential Management**: Stores API keys, cloud credentials, or other authentication mechanisms
- **Security Isolation**: Keeps sensitive credentials separate from routing configuration
- **Provider Flexibility**: Supports multiple authentication types for different providers

#### Authentication Types

##### API Key Authentication

Commonly used across many providers

<CodeBlock language="yaml">{SecurityPolicyAPIKey}</CodeBlock>

:::note
The secret must contain the API key with the key name `"apiKey"`.
:::

##### AWS Credentials

Used when connecting to AWS Bedrock. Supports three authentication methods:

**Option 1: EKS Pod Identity or IRSA (Recommended for production)**

When running on EKS, the AWS SDK automatically uses the default credential chain, which includes EKS Pod Identity and IRSA. Simply configure the region:

<CodeBlock language="yaml">{SecurityPolicyAWSEKS}</CodeBlock>

See the [Connect AWS Bedrock guide](../../getting-started/connect-providers/aws-bedrock.md) for detailed setup instructions.

**Option 2: Static Credentials (Development/Testing)**

<CodeBlock language="yaml">{SecurityPolicyAWSStatic}</CodeBlock>

:::note
When using static credentials, the secret must contain the AWS credentials file with the key name `"credentials"`.
:::

##### Azure Credentials

Used for connecting to Azure OpenAI

<CodeBlock language="yaml">{SecurityPolicyAzure}</CodeBlock>

:::note
The secret must contain the Azure client secret with the key name `"client-secret"`.
:::

##### GCP Credentials

Used for connecting to GCP Vertex AI and Anthropic on GCP

1. Service Account Key Files:
   A service account key file is a JSON file containing a private key that authenticates as a service account.
   You create a service account in GCP, generate a key file, download it, and then store it in the k8s secret referenced by BackendSecurityPolicy.
   Envoy AI Gateway uses this key file to generate an access token and authenticate with GCP Vertex AI.

<CodeBlock language="yaml">{SecurityPolicyGCPSA}</CodeBlock>

2. Workload Identity Federation:
   Workload Identity Federation is a modern, keyless authentication method that allows workloads running outside of GCP to impersonate a service account using their own native identity.
   It leverages a trust relationship between GCP and an external identity provider such as OIDC.

<CodeBlock language="yaml">{SecurityPolicyGCPWIF}</CodeBlock>

#### Security Best Practices

- **Store credentials in Kubernetes Secrets**: Never expose sensitive data in plain text
- **Use principle of least privilege**: Grant only necessary permissions
- **Rotate credentials regularly**: Implement credential rotation policies
- **Separate environments**: Use different credentials for development, staging, and production

### AIGatewayRoute

The `AIGatewayRoute` resource defines how client requests are routed to appropriate AI backends and manages the unified API interface.

#### Purpose and Configuration

- **Request Routing**: Directs traffic to specific backends based on model names or other criteria
- **API Unification**: Provides a consistent interface regardless of backend provider
- **Request Transformation**: Automatically converts between different API schemas
- **Load Balancing**: Distributes traffic across multiple backends

#### Basic Configuration

<CodeBlock language="yaml">{RouteBasic}</CodeBlock>

## Resource Relationships and Data Flow

Understanding how these resources work together is crucial for successful provider connectivity:

```mermaid
graph TD
    A[Client Request] --> B[AIGatewayRoute]
    B --> C{Model Header}
    C -->|gpt-4o-mini| D[OpenAI AIServiceBackend]
    C -->|claude-3-sonnet| E[Bedrock AIServiceBackend]
    D --> F[OpenAI BackendSecurityPolicy]
    E --> G[AWS BackendSecurityPolicy]
    F --> H[OpenAI API]
    G --> I[AWS Bedrock API]

    style A fill:#e1f5fe
    style B fill:#f3e5f5
    style D fill:#e8f5e8
    style E fill:#e8f5e8
    style F fill:#fff3e0
    style G fill:#fff3e0
```

### Data Flow Process

1. **Request Reception**: Client sends a request to the AI Gateway with the OpenAI-compatible format
2. **Route Matching**: AIGatewayRoute examines request headers (like `x-ai-eg-model`) to determine the target backend
3. **Backend Resolution**: The matching rule identifies the appropriate AIServiceBackend
4. **Authentication**: The AIServiceBackend's security policy provides credentials for upstream authentication
5. **Schema Transformation**: If needed, the request is transformed from the input schema to the backend's expected schema
6. **Provider Communication**: The request is forwarded to the actual AI provider with proper authentication
7. **Response Processing**: The provider's response is transformed back to the unified schema format
8. **Client Response**: The standardized response is returned to the client

## Common Configuration Patterns

### Single Provider Setup

For a simple single-provider setup:

<CodeBlock language="yaml">{RouteSingleProvider}</CodeBlock>

### Multi-Provider Setup with Fallback

For high availability with multiple providers:

<CodeBlock language="yaml">{RouteFallback}</CodeBlock>

### Model-Specific Routing

For routing different models to specialized providers:

<CodeBlock language="yaml">{RouteModelSpecific}</CodeBlock>

Configure model ownership and creation information for the `/models` endpoint:

<CodeBlock language="yaml">{RouteModelMetadata}</CodeBlock>

## Provider-Specific Considerations

### OpenAI-Compatible Providers

Many providers offer OpenAI-compatible APIs:

- Use OpenAI schema configuration
- Adjust the version field if the provider uses custom paths
- Standard API key authentication typically applies

### Cloud Provider Integration

Cloud providers like AWS Bedrock and Azure OpenAI require:

- Cloud-specific credential types (AWS IAM, Azure Service Principal, GCP Workload Identity)
- Region specification for multi-region services
- Custom schema configurations for native APIs

### Self-Hosted Models

Self-hosted models using frameworks like vLLM:

- Often compatible with OpenAI schema
- May not require authentication (internal networks)
- Custom endpoints through Envoy Gateway Backend resources

## Validation and Troubleshooting

### Configuration Validation

The AI Gateway validates configurations at deployment time:

- **Schema Compatibility**: Ensures input and output schemas are compatible
- **Resource References**: Validates that referenced resources exist
- **Credential Access**: Verifies that secrets are accessible

### Status Conditions

All resources provide status conditions to monitor their health:

- **Accepted**: Resource is valid and has been accepted by the controller
- **NotAccepted**: Resource has validation errors or configuration issues

### Common Issues and Solutions

**Authentication Failures (401/403)**

- Verify API keys and credentials are correct
- Check secret references and key names
- Ensure credentials have necessary permissions

**Schema Mismatch Errors**

- Confirm the backend schema matches the provider's API
- Check version specifications for provider-specific paths
- Review API documentation for schema requirements

**Routing Issues**

- Verify header matching rules in AIGatewayRoute
- Check that model names match expected values
- Ensure backend references point to existing AIServiceBackends

**Backend Reference Errors**

- Ensure backendRef points to Envoy Gateway Backend resources
- Verify the Backend resource exists and is properly configured
- Check that the group field is set to `gateway.envoyproxy.io`

## Next Steps

Now that you understand the connectivity fundamentals:

- **[Supported Providers](/docs/capabilities/llm-integrations/supported-providers)** - View the complete list of supported providers and their configurations
- **[Supported Endpoints](/docs/capabilities/llm-integrations/supported-endpoints)** - Learn about available API endpoints and their capabilities
- **[Getting Started Guide](/docs/getting-started/connect-providers)** - Follow hands-on tutorials for specific providers
- **[Traffic Management](/docs/capabilities/traffic)** - Configure advanced routing, rate limiting, and fallback strategies
- **[Security](/docs/capabilities/security)** - Implement comprehensive security policies for your AI traffic

## API Reference

For detailed information about resource fields and configuration options:

- [AIServiceBackend API Reference](../../api/api.mdx#aiservicebackend)
- [BackendSecurityPolicy API Reference](../../api/api.mdx#backendsecuritypolicy)
- [AIGatewayRoute API Reference](../../api/api.mdx#aigatewayroute)

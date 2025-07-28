# Vendor-Specific Fields Support

## Table of Contents

<!-- toc -->

-   [Summary](#summary)
-   [Background](#background)
-   [Schema Extensions](#schema-extensions)
-   [Examples](#examples)

<!-- /toc -->

## Summary

This proposal introduces support for vendor-specific fields in the Envoy AI Gateway, enabling users to specify backend-specific parameters through an `aigateway.envoy.io` field in OpenAI requests. This feature allows users to leverage advanced capabilities specific to different AI service backends while maintaining the unified OpenAI API interface.

The implementation extends the existing request translation pipeline to extract, validate, and apply vendor-specific fields to the translated request body based on the target backend's APISchemaName.

## Background

The Envoy AI Gateway currently provides a unified OpenAI API interface that translates requests to various AI service backends. While this approach offers excellent developer experience and simplicity, it limits access to backend-specific features that may be crucial for advanced use cases.

For example:
- GCP Vertex AI's `thinkingConfig` for advanced reasoning models.
- AWS Bedrock's `guardrail` parameters for content filtering.

## Schema Extensions
The `ChatCompletionRequest` struct is extended to include a new field for vendor-specific parameters

```go
type ChatCompletionRequest struct {
    // ...existing fields...
    VendorSpecificFields *VendorSpecificFields `json:"aigateway.envoy.io,omitempty"`
}

// VendorSpecificFields contains backend-specific fields for all supported backends.
type VendorSpecificFields struct {
    GCPVertexAI  *GCPVertexAIVendorFields  `json:"GCPVertexAI,omitempty"`
    GCPAnthropic *GCPAnthropicVendorFields `json:"GCPAnthropic,omitempty"`
}

// GCP Vertex AI vendor-specific fields.
type GCPVertexAIVendorFields struct {
    GenerationConfig *GCPVertexAIGenerationConfig `json:"generationConfig,omitempty"`
}

// AWS Bedrock vendor-specific fields.
type GCPAnthropicVendorFields struct {
    Thinking *anthropic.ThinkingConfigParamUnion `json:"thinking,omitzero"`
}

// Additional vendor field structs for other backends...
```

## Examples

### GCP Vertex AI with Thinking Config

```json
{
  "model": "gemini-1.5-pro",
  "messages": [
    {
      "role": "user",
      "content": "Explain quantum computing and show me a simple code example."
    }
  ],
  "temperature": 0.7,
  "max_tokens": 2000,
  "aigateway.envoy.io": {
    "GCPVertexAI": {
      "generationConfig": {
        "thinkingConfig": {
          "includeThoughts": true,
          "thinkingBudget": 1000
        }
      }
    },
    "GCPAnthropic": {
      "thinking": {
        "type": "enabled",
        "budget_tokens": 1000
      }
    }
  }
}
```

This proposal enables users to access the full capabilities of underlying AI services while maintaining the simplicity and consistency of the unified OpenAI API interface provided by the Envoy AI Gateway.

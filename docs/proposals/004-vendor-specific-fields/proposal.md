# Vendor-Specific Fields Support

## Table of Contents

<!-- toc -->

- [Summary](#summary)
- [Background](#background)
- [Schema Extensions](#schema-extensions)
- [Examples](#examples)

<!-- /toc -->

## Summary

This proposal introduces support for vendor-specific fields in the Envoy AI Gateway, enabling users to specify backend-specific parameters directly as inline fields in OpenAI requests. This feature allows users to leverage advanced capabilities specific to different AI service backends while maintaining the unified OpenAI API interface.

The implementation extends the existing request translation pipeline to extract, validate, and apply vendor-specific fields to the translated request body based on the target backend's APISchemaName.

## Background

The Envoy AI Gateway currently provides a unified OpenAI API interface that translates requests to various AI service backends. While this approach offers excellent developer experience and simplicity, it limits access to backend-specific features that may be crucial for advanced use cases.

For example:

- GCP Vertex AI's `thinkingConfig` for advanced reasoning models.
- GCP Anthropic's `thinking` parameters for enhanced reasoning capabilities.

## Schema Extensions

The `ChatCompletionRequest` struct is extended to include inline vendor-specific fields for supported backends:

```go
type ChatCompletionRequest struct {
	// ...existing fields...

	// Vendor-specific fields are added as inline fields
	*GCPVertexAIVendorFields `json:",inline,omitempty"`
	*AnthropicVendorFields   `json:",inline,omitempty"`
}

// GCPVertexAIVendorFields contains GCP Vertex AI (Gemini) vendor-specific fields.
type GCPVertexAIVendorFields struct {
	// GenerationConfig holds Gemini generation configuration options.
	GenerationConfig *GCPVertexAIGenerationConfig `json:"generationConfig,omitempty"`
}

// GCPVertexAIGenerationConfig represents Gemini generation configuration options.
type GCPVertexAIGenerationConfig struct {
	ThinkingConfig *genai.GenerationConfigThinkingConfig `json:"thinkingConfig,omitempty"`
}

// AnthropicVendorFields contains GCP Anthropic-specific fields.
type AnthropicVendorFields struct {
	Thinking *anthropic.ThinkingConfigParamUnion `json:"thinking,omitzero"`
}
```

## Examples

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
  "thinking": {
    "type": "enabled",
    "budget_tokens": 1000
  },
  "generationConfig": {
    "thinkingConfig": {
      "includeThoughts": true,
      "thinkingBudget": 1000
    }
  }
}
```

This proposal enables users to access the full capabilities of underlying AI services while maintaining the simplicity and consistency of the unified OpenAI API interface provided by the Envoy AI Gateway.

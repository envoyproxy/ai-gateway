---
id: vendor-specific-fields
title: Extension Fields
---

# Extension Fields

The AI Gateway supports extension fields that allow you to specify unified or backend-specific parameters directly as inline fields in your OpenAI-compatible requests. These fields are applied during the translation process to the target backend's native API format.

## Overview

Extension fields enable you to:

- Use advanced backend-specific features not available in the OpenAI API
- Use unified configuration fields that work across multiple providers not available in the OpenAI API

Extension fields are specified as inline fields in your OpenAI request and are applied after the standard OpenAI-to-backend translation.

### Unified Thinking Configuration

For thinking/reasoning capabilities, you can use a unified `thinking` field that automatically translates to the correct backend-specific format:

- **GCP Vertex AI (Gemini)**: Translates to `generationConfig.thinkingConfig`
- **GCP Anthropic**: Uses `thinking` field directly
- **AWS Bedrock**: Uses `thinking` field directly

This unified approach allows you to write provider-agnostic requests while still leveraging thinking capabilities.

## Supported Backends

The following backends support vendor-specific fields:

### GCP Vertex AI (Gemini)

- **API Schema Name**: `GCPVertexAI`
- **Supported Fields**:
  - `generationConfig.thinkingConfig`: Configure thinking process for reasoning models. [Gemini Docs](https://cloud.google.com/vertex-ai/docs/reference/rest/v1/GenerationConfig#ThinkingConfig)
- **Unified Fields**:
  - `thinking`: Unified thinking configuration that automatically translates to `generationConfig.thinkingConfig`

### GCP Anthropic

- **API Schema Name**: `GCPAnthropic`
- **Supported Fields**:
  - `thinking`: Configuration for enabling Claude's extended thinking. [Anthropic Docs](https://docs.anthropic.com/en/api/messages#body-thinking)
- **Unified Fields**:
  - `thinking`: Works directly with this provider (no translation needed)

### AWS Bedrock

- **API Schema Name**: `AWSBedrock`
- **Supported Fields**:
  - `thinking`: Configuration for enabling Anthropic Claude's extended thinking. [AWS Docs](https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-extended-thinking.html)
- **Unified Fields**:
  - `thinking`: Works directly with this provider (no translation needed)

## Usage

Add vendor-specific fields directly as inline fields in your OpenAI request:

### Using Unified Thinking Configuration

The simplest way to enable thinking capabilities across all providers is to use the unified `thinking` field:

```json
{
  "model": "claude-3-5-sonnet",
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
    "budget_tokens": 1000,
    "includeThoughts": true
  }
}
```

This configuration will work with any provider that supports thinking, automatically translating to the correct backend format.

### Using Provider-Specific Fields

For more fine-grained control or provider-specific features, you can use the vendor-specific fields:

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
  "safetySettings": [{
    "category": "HARM_CATEGORY_HARASSMENT",
    "threshold": "BLOCK_ONLY_HIGH"
  }]
}
```

### Field Conflicts

Vendor fields override translated fields when conflicts occur.

When using unified thinking configuration, the `thinking` field takes precedence over any provider-specific thinking configurations.

### Benefits of Unified Configuration

The unified thinking configuration provides several advantages:

1. **Provider Agnostic**: Write once, run on multiple providers
2. **Simplified Migration**: Easy to switch between providers without changing request format
3. **Consistent Interface**: Same field names and structure across all providers
4. **Automatic Translation**: The gateway handles provider-specific format conversion

### Unsupported Fields/Backends

Fields and Backends other than specified in [Supported Backends](#supported-backends) will be ignored.

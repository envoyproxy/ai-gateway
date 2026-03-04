---
id: vendor-specific-fields
title: Vendor-Specific Fields
---

# Vendor-Specific Fields

The AI Gateway supports vendor-specific fields that allow you to specify backend-specific parameters directly as inline fields in your OpenAI-compatible requests. These fields are applied during the translation process to the target backend's native API format.

## Overview

Vendor-specific fields enable you to:

- Use advanced backend-specific features not available in the OpenAI API

The vendor-specific fields are specified as inline fields in your OpenAI request and are applied after the standard OpenAI-to-backend translation.

## Supported Backends

The following backends support vendor-specific fields:

### GCP Vertex AI (Gemini)

- **API Schema Name**: `GCPVertexAI`
- **Supported Fields**:
  - `generationConfig.thinkingConfig`: Configure thinking process for reasoning models. [Gemini Docs](https://cloud.google.com/vertex-ai/docs/reference/rest/v1/GenerationConfig#ThinkingConfig)

### GCP Anthropic

- **API Schema Name**: `GCPAnthropic`
- **Supported Fields**:
  - `thinking`: Configuration for enabling Claude's extended thinking. [Anthropic Docs](https://docs.anthropic.com/en/api/messages#body-thinking)

### AWS Bedrock

- **API Schema Name**: `AWSBedrock`
- **Supported Fields**:
  - `thinking`: Configuration for enabling Anthropic Claude's extended thinking. [AWS Docs](https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-extended-thinking.html)

## Usage

Add vendor-specific fields directly as inline fields in your OpenAI request:

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

### Field Conflicts

Vendor fields override translated fields when conflicts occur.

### Unsupported Fields/Backends

Fields and Backends other than specified in [Supported Backends](#supported-backends) will be ignored.

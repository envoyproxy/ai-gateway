---
id: supported-endpoints
title: Supported OpenAI API Endpoints
sidebar_position: 9
---

The Envoy AI Gateway provides OpenAI-compatible API endpoints for routing and managing LLM/AI traffic. This page documents which OpenAI API endpoints are currently supported and their capabilities.

## Overview

The Envoy AI Gateway acts as a proxy that accepts OpenAI-compatible requests and routes them to various AI providers. While it maintains compatibility with the OpenAI API specification, it currently supports a subset of the full OpenAI API.

## Supported Endpoints

### Chat Completions

**Endpoint:** `POST /v1/chat/completions`

**Status:** ✅ Fully Supported

**Description:** Create a chat completion response for the given conversation.

**Features:**
- ✅ Streaming and non-streaming responses
- ✅ Function calling
- ✅ Response format specification (including JSON schema)
- ✅ Temperature, top_p, and other sampling parameters
- ✅ System and user messages
- ✅ Model selection via request body or `x-ai-eg-model` header
- ✅ Token usage tracking and cost calculation
- ✅ Provider fallback and load balancing

**Supported Providers:**
- OpenAI
- AWS Bedrock (with automatic translation)
- Azure OpenAI (with automatic translation)
- Any OpenAI-compatible provider (Groq, Together AI, Mistral, etc.)

**Example:**
```bash
curl -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "Hello, how are you?"
      }
    ]
  }' \
  $GATEWAY_URL/v1/chat/completions
```

### Embeddings

**Endpoint:** `POST /v1/embeddings`

**Status:** ✅ Fully Supported

**Description:** Create embeddings for the given input text.

**Features:**
- ✅ Single and batch text embedding
- ✅ Model selection via request body or `x-ai-eg-model` header
- ✅ Token usage tracking and cost calculation
- ✅ Provider fallback and load balancing

**Supported Providers:**
- OpenAI
- Any OpenAI-compatible provider that supports embeddings

## Provider-Endpoint Compatibility

The following table shows which providers support which endpoints:

| Provider | Chat Completions | Embeddings | Notes |
|----------|:----------------:|:----------:|-------|
| [OpenAI](https://platform.openai.com/docs/api-reference) | ✅ | ✅ | Full support for both endpoints |
| [AWS Bedrock](https://docs.aws.amazon.com/bedrock/latest/APIReference/) | ✅ | ❌ | Uses Converse API translation, embeddings not supported |
| [Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/reference) | ✅ | ❌ | Uses native Azure API translation, embeddings not supported |
| [Google Gemini](https://ai.google.dev/gemini-api/docs/openai) | ✅ | ✅ | Via OpenAI-compatible endpoint |
| [Groq](https://console.groq.com/docs/openai) | ✅ | ✅ | OpenAI-compatible API |
| [Grok](https://docs.x.ai/docs/api-reference) | ✅ | ✅ | OpenAI-compatible API |
| [Together AI](https://docs.together.ai/docs/openai-api-compatibility) | ✅ | ✅ | OpenAI-compatible API |
| [Cohere](https://docs.cohere.com/v2/docs/compatibility-api) | ✅ | ✅ | Via OpenAI-compatible endpoint |
| [Mistral](https://docs.mistral.ai/api/) | ✅ | ✅ | OpenAI-compatible API |
| [DeepInfra](https://deepinfra.com/docs/inference) | ✅ | ✅ | Via OpenAI-compatible endpoint |
| [DeepSeek](https://api-docs.deepseek.com/) | ✅ | ❌ | OpenAI-compatible API |
| [Hunyuan](https://cloud.tencent.com/document/product/1729/111007) | ✅ | ✅ | OpenAI-compatible API |
| [Tencent LLM Knowledge Engine](https://www.tencentcloud.com/document/product/1255/70381) | ✅ | ❌ | OpenAI-compatible API |

**Note:** Embeddings support requires the provider to use OpenAI-compatible API schema. Providers that use native API translation (AWS Bedrock, Azure OpenAI) currently only support chat completions.

**Example:**
```bash
curl -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-ada-002",
    "input": "The quick brown fox jumps over the lazy dog"
  }' \
  $GATEWAY_URL/v1/embeddings
```

### Models

**Endpoint:** `GET /v1/models`

**Status:** ✅ Fully Supported

**Description:** List available models configured in the AI Gateway.

**Features:**
- ✅ Returns models declared in AIGatewayRoute configurations
- ✅ OpenAI-compatible response format
- ✅ Model metadata (ID, owned_by, created timestamp)

**Example:**
```bash
curl $GATEWAY_URL/v1/models
```

**Response Format:**
```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4o-mini",
      "object": "model",
      "created": 1677610602,
      "owned_by": "openai"
    }
  ]
}
```


## What's Next

To learn more about configuring and using the Envoy AI Gateway with these endpoints:

- **[Supported Providers](./supported-providers.md)** - Complete list of supported AI providers and their configurations
- **[Usage-Based Rate Limiting](./capabilities/usage-based-ratelimiting.md)** - Configure token-based rate limiting and cost controls
- **[Provider Fallback](./capabilities/fallback.md)** - Set up automatic failover between providers for high availability
- **[Metrics and Monitoring](./capabilities/metrics.md)** - Monitor usage, costs, and performance metrics

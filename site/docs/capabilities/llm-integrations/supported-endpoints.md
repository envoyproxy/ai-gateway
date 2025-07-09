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

**Description:** Create embeddings for the given input text.

**Features:**
- ✅ Single and batch text embedding
- ✅ Model selection via request body or `x-ai-eg-model` header
- ✅ Token usage tracking and cost calculation
- ✅ Provider fallback and load balancing

**Supported Providers:**
- OpenAI
- Any OpenAI-compatible provider that supports embeddings

### Models

**Endpoint:** `GET /v1/models`

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

## Provider-Endpoint Compatibility Table

The following table summarizes which providers support which endpoints:

| Provider                                                                                              | Chat Completions | Embeddings | Notes                         |
|-------------------------------------------------------------------------------------------------------|:----------------:|:----------:|-------------------------------|
| [OpenAI](https://platform.openai.com/docs/api-reference)                                              |        ✅         |     ✅      |                               |
| [AWS Bedrock](https://docs.aws.amazon.com/bedrock/latest/APIReference/)                               |        ✅         |     🚧     | Via API translation           |
| [Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/reference)                  |        ✅         |     🚧     | Via API translation           |
| [Google Gemini](https://ai.google.dev/gemini-api/docs/openai)                                         |        ✅         |     ✅      | Via OpenAI-compatible API     |
| [Groq](https://console.groq.com/docs/openai)                                                          |        ✅         |     ❌      | Via OpenAI-compatible API     |
| [Grok](https://docs.x.ai/docs/api-reference)                                                          |        ✅         |     ❌      | Via OpenAI-compatible API     |
| [Together AI](https://docs.together.ai/docs/openai-api-compatibility)                                 |        ⚠️        |     ⚠️     | Via OpenAI-compatible API     |
| [Cohere](https://docs.cohere.com/v2/docs/compatibility-api)                                           |        ⚠️        |     ⚠️     | Via OpenAI-compatible API     |
| [Mistral](https://docs.mistral.ai/api/)                                                               |        ⚠️        |     ⚠️     | Via OpenAI-compatible API     |
| [DeepInfra](https://deepinfra.com/docs/inference)                                                     |        ✅         |     ✅      | Via OpenAI-compatible API     |
| [DeepSeek](https://api-docs.deepseek.com/)                                                            |        ⚠️        |     ❌      | Via OpenAI-compatible API     |
| [Hunyuan](https://cloud.tencent.com/document/product/1729/111007)                                     |        ⚠️        |     ⚠️     | Via OpenAI-compatible API     |
| [Tencent LLM Knowledge Engine](https://www.tencentcloud.com/document/product/1255/70381)              |        ⚠️        |     ❌      | Via OpenAI-compatible API     |
| [Google Vertex AI](https://cloud.google.com/vertex-ai/docs/reference/rest)                            |        🚧        |     🚧     | Work-in-progress: [issue#609] |
| [Anthropic on Vertex AI](https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude) |        🚧        |     🚧     | Work-in-progress: [issue#609] |
| [SambaNova](https://docs.sambanova.ai/sambastudio/latest/open-ai-api.html)                            |        ✅         |     ✅      | Via OpenAI-compatible API     |                                                                                                                                                        |
* ✅ - Supported and Tested on Envoy AI Gateway CI
* ⚠️️ - Expected to work based on provider documentation, but not tested on the CI.
* ❌ - Not supported according to provider documentation.
* 🚧 - Unimplemented, or under active development but planned for future releases

## What's Next

To learn more about configuring and using the Envoy AI Gateway with these endpoints:

- **[Supported Providers](./supported-providers.md)** - Complete list of supported AI providers and their configurations
- **[Usage-Based Rate Limiting](../traffic/usage-based-ratelimiting.md)** - Configure token-based rate limiting and cost controls
- **[Provider Fallback](../traffic/fallback.md)** - Set up automatic failover between providers for high availability
- **[Metrics and Monitoring](../observability/metrics.md)** - Monitor usage, costs, and performance metrics

[issue#609]: https://github.com/envoyproxy/ai-gateway/issues/609

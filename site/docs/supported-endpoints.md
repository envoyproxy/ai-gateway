---
id: supported-endpoints
title: Supported OpenAI API Endpoints
sidebar_position: 9
---

The Envoy AI Gateway provides OpenAI-compatible API endpoints for routing and managing LLM/AI traffic. This page documents which OpenAI API endpoints are currently supported and their capabilities.

## Overview

The AI Gateway acts as a proxy that accepts OpenAI-compatible requests and routes them to various AI providers. While it maintains compatibility with the OpenAI API specification, it currently supports a subset of the full OpenAI API.

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

## Unsupported Endpoints

The following OpenAI API endpoints are **not currently supported**:

### Audio
- `POST /v1/audio/speech` - Text-to-speech
- `POST /v1/audio/transcriptions` - Speech-to-text (Whisper)
- `POST /v1/audio/translations` - Audio translation

### Images
- `POST /v1/images/generations` - Image generation (DALL-E)
- `POST /v1/images/edits` - Image editing
- `POST /v1/images/variations` - Image variations

### Files
- `GET /v1/files` - List files
- `POST /v1/files` - Upload file
- `DELETE /v1/files/{file_id}` - Delete file
- `GET /v1/files/{file_id}` - Retrieve file
- `GET /v1/files/{file_id}/content` - Retrieve file content

### Fine-tuning
- `POST /v1/fine_tuning/jobs` - Create fine-tuning job
- `GET /v1/fine_tuning/jobs` - List fine-tuning jobs
- `GET /v1/fine_tuning/jobs/{fine_tuning_job_id}` - Retrieve fine-tuning job
- `POST /v1/fine_tuning/jobs/{fine_tuning_job_id}/cancel` - Cancel fine-tuning job

### Assistants (Beta)
- All `/v1/assistants/*` endpoints
- All `/v1/threads/*` endpoints
- All `/v1/vector_stores/*` endpoints

### Batch
- `POST /v1/batches` - Create batch
- `GET /v1/batches` - List batches
- `GET /v1/batches/{batch_id}` - Retrieve batch
- `POST /v1/batches/{batch_id}/cancel` - Cancel batch

### Moderation
- `POST /v1/moderations` - Content moderation

## Model Selection

The AI Gateway supports model selection through two methods:

1. **Request Body:** Specify the model in the request body (standard OpenAI format)
2. **Header-based:** Use the `x-ai-eg-model` header for routing decisions

The `x-ai-eg-model` header is particularly useful for:
- Load balancing between different providers for the same model
- Provider fallback scenarios
- Custom routing logic based on model names

## Provider Translation

The AI Gateway automatically translates OpenAI-format requests to provider-specific formats:

- **AWS Bedrock:** Converts to Bedrock's Converse API
- **Azure OpenAI:** Adjusts API paths and authentication
- **OpenAI-compatible providers:** Passes through with minimal modifications

## Rate Limiting and Cost Tracking

All supported endpoints include:
- Token usage tracking
- Cost calculation based on provider pricing
- Rate limiting capabilities
- Request/response metrics

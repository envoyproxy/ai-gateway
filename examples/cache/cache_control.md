# AWS Bedrock Cache Control Example

This example demonstrates how to use prompt caching with AWS Bedrock Claude models through Envoy AI Gateway using the standard `cache_control` API.

## Overview

Envoy AI Gateway supports provider-agnostic cache control, allowing you to use the same `cache_control` syntax across all Claude providers (Direct Anthropic, AWS Anthropic Messages, and AWS Bedrock). This example focuses on AWS Bedrock integration.

## Benefits

- **Cost Optimization**: Reduce token costs by caching repeated content
- **Performance**: Faster response times for cached content
- **Provider Consistency**: Same API across all Claude providers
- **Token Transparency**: Clear reporting of cache read/write tokens

## Prerequisites

- AWS Bedrock access with enabled Claude models
- Envoy AI Gateway configured for AWS Bedrock (see [AWS Bedrock setup guide](../../site/docs/getting-started/connect-providers/aws-bedrock.md))

## Example Requests

### Basic System Prompt Caching

Cache a system prompt that will be reused across multiple conversations:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "messages": [
      {
        "role": "system",
        "content": [
          {
            "type": "text",
            "text": "You are an expert assistant with access to a comprehensive knowledge base covering technology, science, and business. Always provide detailed, accurate, and well-sourced responses. When analyzing documents, break down complex concepts into digestible parts.",
            "cache_control": {"type": "ephemeral"}
          }
        ]
      },
      {
        "role": "user",
        "content": "What are the key principles of microservices architecture?"
      }
    ]
  }'
```

### Document Analysis with Caching

Cache a large document for analysis across multiple queries:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "messages": [
      {
        "role": "user",
        "content": [
          {
            "type": "text",
            "text": "Please analyze this technical specification document:\n\n[Large document content here - minimum 1024 tokens for effective caching]\n\nTechnical Specification: Distributed System Architecture\n\n1. Introduction\nThis document outlines the architecture for a distributed system designed to handle high-throughput data processing...\n[Continue with substantial content]",
            "cache_control": {"type": "ephemeral"}
          },
          {
            "type": "text",
            "text": "What are the main components described in this document?"
          }
        ]
      }
    ]
  }'
```

### Tool Definition Caching

Cache complex tool schemas that will be reused:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "messages": [
      {
        "role": "user",
        "content": "Help me search for information about cloud computing trends."
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "search_knowledge_base",
          "description": "Search through a comprehensive knowledge base containing technical articles, research papers, industry reports, and documentation covering cloud computing, distributed systems, microservices, containers, orchestration, serverless computing, and related technologies.",
          "parameters": {
            "type": "object",
            "properties": {
              "query": {
                "type": "string",
                "description": "Search query"
              },
              "category": {
                "type": "string",
                "enum": ["research", "tutorials", "documentation", "news"],
                "description": "Content category to search within"
              },
              "date_range": {
                "type": "string",
                "enum": ["last_week", "last_month", "last_year", "all_time"],
                "description": "Time range for results"
              }
            },
            "required": ["query"]
          },
          "cache_control": {"type": "ephemeral"}
        }
      }
    ]
  }'
```

### Multiple Cache Points

Use multiple cache points strategically (max 4 per request):

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
    "messages": [
      {
        "role": "system",
        "content": [
          {
            "type": "text",
            "text": "You are a technical documentation assistant specialized in API design and software architecture.",
            "cache_control": {"type": "ephemeral"}
          }
        ]
      },
      {
        "role": "user",
        "content": [
          {
            "type": "text",
            "text": "Here is our API specification:\n\n[Large API spec content - cache point 2]",
            "cache_control": {"type": "ephemeral"}
          },
          {
            "type": "text",
            "text": "Here is our database schema:\n\n[Large schema content - cache point 3]",
            "cache_control": {"type": "ephemeral"}
          },
          {
            "type": "text",
            "text": "Please analyze the API design and suggest improvements."
          }
        ]
      }
    ]
  }'
```

## Response Format

Cached token usage is reported in the response:

```json
{
  "choices": [...],
  "usage": {
    "prompt_tokens": 2000,
    "completion_tokens": 150,
    "total_tokens": 2150,
    "prompt_tokens_details": {
      "cached_tokens": 1800
    }
  }
}
```

- `cached_tokens`: Number of tokens read from cache (reduced cost)
- Cache write tokens are tracked internally for billing

## Best Practices

1. **Cache Content ≥1024 Tokens**: AWS Bedrock requires minimum token counts for effective caching
2. **Strategic Placement**: Cache content that will be reused across multiple requests
3. **Stay Within Limits**: Maximum 4 cache points per request
4. **Monitor Usage**: Track `cached_tokens` in responses to measure cost savings

## Error Handling

### Cache Limit Exceeded

```json
{
  "error": {
    "message": "AWS Bedrock supports a maximum of 4 cache checkpoints per request, found 5"
  }
}
```

### Insufficient Tokens

AWS Bedrock may reject cache requests with content below the minimum token threshold.

## Implementation Notes

- Cache control fields are automatically translated to AWS Bedrock `cachePoint` format
- Only `"ephemeral"` cache type is currently supported
- Works with all Claude models available on AWS Bedrock
- Backward compatible - existing requests without cache control continue to work

## Related Examples

- [Basic AWS Bedrock Setup](../examples/basic/)
- [Provider Fallback](../examples/provider_fallback/)
- [Token Rate Limiting](../examples/token_ratelimit/)

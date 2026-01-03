# Claude Prompt Caching Examples

This example demonstrates how to use prompt caching with Claude models across all supported providers through Envoy AI Gateway using the standard `cache_control` API.

## Overview

Envoy AI Gateway supports **provider-agnostic** cache control, allowing you to use the same `cache_control` syntax across all Claude providers:

- **Direct Anthropic** (via Anthropic Messages API)
- **GCP Vertex AI** (Anthropic Claude models)
- **AWS Bedrock** (Anthropic Claude models via Converse API)

This unified approach ensures your caching implementation works consistently regardless of the backend provider.

## Benefits

- **Cost Optimization**: Reduce token costs by caching repeated content
- **Performance**: Faster response times for cached content
- **Provider Consistency**: Same API across all Claude providers
- **Token Transparency**: Clear reporting of cache read/write tokens
- **Easy Migration**: Switch providers without code changes

## Prerequisites

Choose your preferred provider and ensure you have:

### For Direct Anthropic

- Anthropic API key
- Envoy AI Gateway configured for Anthropic (see [Anthropic setup guide](../../site/docs/getting-started/connect-providers/anthropic.md))

### For GCP Vertex AI

- GCP credentials with Vertex AI access
- Enabled Claude models in your GCP project
- Envoy AI Gateway configured for GCP Vertex AI (see [GCP Vertex AI setup guide](../../site/docs/getting-started/connect-providers/gcp-vertexai.md))

### For AWS Bedrock

- AWS credentials with Bedrock access
- Enabled Claude models in AWS Bedrock
- Envoy AI Gateway configured for AWS Bedrock (see [AWS Bedrock setup guide](../../site/docs/getting-started/connect-providers/aws-bedrock.md))

## Provider-Specific Considerations

| Feature             | Anthropic Direct                                                                                                       | GCP Vertex AI | AWS Bedrock                                                                               |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------------- | ----------------------------------------------------------------------------------------- |
| Max Cache Points    | [4 per request](https://platform.claude.com/docs/en/build-with-claude/prompt-caching#when-to-use-multiple-breakpoints) | 4 per request | [4 per request](https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html) |
| Min Token Threshold | 1,024+ tokens                                                                                                          | 1,024+ tokens | 1,024+ tokens                                                                             |
| Billing Integration | Native                                                                                                                 | Native        | Native                                                                                    |
| Cache Types         | ephemeral                                                                                                              | ephemeral     | ephemeral                                                                                 |

## Example Requests

### Basic System Prompt Caching

This example works identically across all providers - just change the model name:

```bash
# Direct Anthropic
curl -X POST http://localhost:8080/v1/messages -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
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

# GCP Vertex AI
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet@20241022",
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

# AWS Bedrock
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
    "model": "claude-3-5-sonnet-20241022",
    "messages": [
      {
        "role": "user",
        "content": [
          {
            "type": "text",
            "text": "Please analyze this technical specification document:\n\n# Technical Specification: Distributed System Architecture\n\n## 1. Introduction\nThis document outlines the architecture for a distributed system designed to handle high-throughput data processing across multiple geographic regions. The system incorporates microservices patterns, event-driven architecture, and cloud-native technologies to ensure scalability, reliability, and maintainability.\n\n## 2. System Overview\nThe distributed system consists of the following core components:\n- API Gateway Layer\n- Service Mesh Infrastructure  \n- Event Streaming Platform\n- Data Processing Pipeline\n- Storage Layer\n- Monitoring and Observability Stack\n\n## 3. Architecture Principles\n### 3.1 Microservices Design\nEach service follows the single responsibility principle and communicates through well-defined APIs. Services are independently deployable and scalable.\n\n### 3.2 Event-Driven Communication\nAsynchronous communication between services uses Apache Kafka for event streaming, ensuring loose coupling and high throughput.\n\n### 3.3 Cloud-Native Technologies\nThe system leverages Kubernetes for orchestration, Istio for service mesh, and cloud provider managed services for storage and networking.\n\n[Continue with substantial content to reach minimum 1024 tokens for effective caching...]",
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

Cache complex tool schemas (works across all providers):

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
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
          "description": "Search through a comprehensive knowledge base containing technical articles, research papers, industry reports, and documentation covering cloud computing, distributed systems, microservices, containers, orchestration, serverless computing, machine learning platforms, data engineering tools, and related technologies. The search supports semantic matching and can find relevant content across multiple domains including AWS, GCP, Azure, Kubernetes, Docker, Terraform, and emerging cloud technologies.",
          "parameters": {
            "type": "object",
            "properties": {
              "query": {
                "type": "string",
                "description": "Natural language search query describing the information needed"
              },
              "category": {
                "type": "string",
                "enum": ["research_papers", "tutorials", "documentation", "industry_reports", "case_studies", "best_practices"],
                "description": "Content category to focus the search within"
              },
              "technology_stack": {
                "type": "array",
                "items": {
                  "type": "string",
                  "enum": ["aws", "gcp", "azure", "kubernetes", "docker", "terraform", "serverless", "ml_platforms", "data_engineering"]
                },
                "description": "Specific technology stacks to include in search results"
              },
              "date_range": {
                "type": "string",
                "enum": ["last_week", "last_month", "last_quarter", "last_year", "all_time"],
                "description": "Time range for results to ensure relevance"
              },
              "result_format": {
                "type": "string",
                "enum": ["summary", "detailed", "technical_deep_dive"],
                "description": "Level of detail required in the search results"
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

You can use multiple cache points in a single request:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
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
            "text": "Here is our API specification: [Large API spec content]",
            "cache_control": {"type": "ephemeral"}
          },
          {
            "type": "text",
            "text": "Here is our database schema: [Large schema content]",
            "cache_control": {"type": "ephemeral"}
          },
          {
            "type": "text",
            "text": "Please analyze these components and suggest improvements."
          }
        ]
      }
    ]
  }'
```

## Response Format

Cached token usage is reported consistently across all providers:

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

1. **Cache Content ≥1024 Tokens**: All providers require minimum token counts for effective caching
2. **Strategic Placement**: Cache content that will be reused across multiple requests
3. **Monitor Usage**: Track `cached_tokens` in responses to measure cost savings
4. **Provider Considerations**: Be aware of provider-specific limits when designing your cache strategy

## Error Handling

### Insufficient Tokens

Providers may reject cache requests with content below the minimum token threshold.

### Provider-Specific Limits

Each provider may have its own limitations. Refer to provider documentation for specific requirements and error messages.

## Migration Between Providers

To switch between providers while maintaining caching:

1. **Update model names** in your requests
2. **Review provider-specific limitations** if applicable
3. **No other code changes required** - the cache_control syntax is identical

## Implementation Notes

- **Anthropic Direct**: Native cache_control support
- **OpenAI API with GCP Vertex AI**: Cache control translated to Anthropic format
- **OpenAI API AWS Bedrock**: Cache control translated to Bedrock cachePoint format
- All providers support `"ephemeral"` cache type
- Backward compatible - existing requests without cache control continue to work

## Related Examples

- [Basic Provider Setup](../basic/)
- [Provider Fallback](../provider_fallback/)
- [Token Rate Limiting](../token_ratelimit/)

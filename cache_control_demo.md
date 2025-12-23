# AWS Bedrock Cache Control Implementation

## Overview

This implementation enables **provider-agnostic** cache control for AWS Bedrock Claude models using the existing `cache_control` API. Users can now use the same cache syntax across all Claude deployments (Direct Anthropic, AWS Anthropic Messages, and AWS Bedrock Converse).

## Key Features

✅ **Provider Agnostic**: Same `cache_control` syntax works across all Claude providers
✅ **Comprehensive Coverage**: Supports caching for Messages, System prompts, and Tools
✅ **AWS Bedrock Translation**: Automatically translates `cache_control` to Bedrock `cachePoint`
✅ **Validation**: Enforces AWS Bedrock limits (max 4 cache checkpoints)
✅ **Token Reporting**: Reports cached read/write tokens in responses

## Usage Example

```json
{
  "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
  "messages": [
    {
      "role": "system",
      "content": [
        {
          "type": "text",
          "text": "You are an expert assistant with access to a large knowledge base.",
          "cache_control": {"type": "ephemeral"}
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Here is a large document to analyze...",
          "cache_control": {"type": "ephemeral"}
        }
      ]
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "search_knowledge_base",
        "description": "Search the knowledge base for relevant information",
        "parameters": {"type": "object", "properties": {}},
        "cache_control": {"type": "ephemeral"}
      }
    }
  ]
}
```

## Implementation Details

### Schema Extensions

**AWS Bedrock Schema** (`internal/apischema/awsbedrock/awsbedrock.go`):
- Added `CachePoint *CachePointBlock` to `ContentBlock`
- Added `CachePoint *CachePointBlock` to `SystemContentBlock`
- Added `CachePoint *CachePointBlock` to `Tool`
- Added `CachePointBlock` struct with `Type string` field

### Translation Logic

**OpenAI to Bedrock Translator** (`internal/translator/openai_awsbedrock.go`):
- `applyCacheControlToContentBlock()`: Applies cache points to message content
- `applyCacheControlToSystemBlock()`: Applies cache points to system messages
- `applyCacheControlToTool()`: Applies cache points to tool definitions
- `validateCachePointCount()`: Ensures max 4 cache checkpoints per request

### Validation Rules

- **Maximum Cache Points**: 4 per request (AWS Bedrock limit)
- **Cache Type**: Only "ephemeral" type supported
- **Token Minimums**: Validated by AWS Bedrock (1,024+ tokens for Claude)

### Response Token Reporting

Cache token usage is already supported:
- `CacheReadInputTokens`: Tokens read from cache
- `CacheWriteInputTokens`: Tokens written to cache (new)
- Mapped to OpenAI `PromptTokensDetails.CachedTokens`

## Testing

Comprehensive test coverage includes:
- ✅ User message cache control
- ✅ System message cache control
- ✅ Tool definition cache control
- ✅ Cache point validation (>4 limit)
- ✅ Helper function testing
- ✅ Integration with existing tests

Run tests:
```bash
go test ./internal/translator -v -run "CacheControl"
```

## Benefits

1. **Cost Optimization**: Reduced token costs for repeated content
2. **Performance**: Faster response times for cached content
3. **Consistency**: Same API across all Claude providers
4. **Transparency**: Clear reporting of cache read/write tokens

## Backward Compatibility

- ✅ No breaking changes
- ✅ Existing requests work unchanged
- ✅ Cache control is optional
- ✅ Works with all existing Claude models on AWS Bedrock
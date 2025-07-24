# JSON Patch Support for Backend-Specific Extensions

author: [Sukumar Gaonkar](https://github.com/sukumargaonkar)

## Table of Contents

<!-- toc -->

-   [Summary](#summary)
-   [Goals](#goals)
-   [Non-Goals](#non-goals)
-   [Background](#background)
    -   [Current Limitations](#current-limitations)
    -   [Use Cases](#use-cases)
-   [Design](#design)
    -   [Schema Extensions](#schema-extensions)
    -   [Processing Pipeline](#processing-pipeline)
-   [Examples](#examples)
    -   [GCP Vertex AI Example](#gcp-vertex-ai-example)
    -   [AWS Bedrock Example](#aws-bedrock-example)
    -   [Generic ANY Example](#generic-any-example)
-   [Open Questions](#open-questions)

<!-- /toc -->

## Summary

This proposal introduces JSON Patch support for the AI Gateway to enable backend-specific field additions that are not natively supported in the OpenAI API schema. The implementation allows users to specify arbitrary JSON patches via OpenAI's `extra_body` parameter, which are applied after request translation but before sending to the backend provider.

The feature supports backend-specific patches matching the APISchemaName (GCPVertexAI, AWSBedrock, etc.) and a generic "ANY" key for patches that apply regardless of backend, enabling fine-grained control over request modifications while maintaining security and compatibility.

## Goals

+ Enable backend-specific field additions through JSON patches in the `extra_body` parameter.
+ Support both schema-specific and universal ("ANY") patch operations.
+ Limit patch operations add and replace.

## Non-Goals

+ Supporting arbitrary request transformation beyond JSON patch operations.
+ Supporting patches at a provider level i.e. patches that apply to every request to the provider.
+ Supporting patches that modify response payloads.

## Background

### Current Limitations

The AI Gateway currently translates OpenAI-compatible requests to various backend providers (GCP Vertex AI, AWS Bedrock, Azure OpenAI, etc.). However, each backend provider often supports additional parameters and features that are not part of the OpenAI API specification.

For example:
- VLLM (models hosted on-premises) supports `guided_decoding`, `guided_regex`, etc parameters.
- GCP Vertex AI supports `gemini_safety_settings`, `cachedContent`, and `ThinkingConfig.thinkingBudget`.
- AWS Bedrock supports `guardrailConfig`.
- Anthropic supports `thinking.budget_tokens`.

Currently, users cannot access these backend-specific features through the AI Gateway, limiting the functionality available when using non-OpenAI providers.

### Use Cases

1. **Advanced Parameter Configuration**: Users need to set provider-specific parameters like GCP's `cachedContent`.
2. **Safety and Content Filtering**: Configuring provider-specific safety settings and content filters.
3. **Experimental Features**: Support request translations for accessing beta or experimental features offered by specific providers.

## Design

### Schema Extensions

The implementation extends the OpenAI ChatCompletionRequest schema with an `extra_body` field that follows OpenAI's convention for additional parameters:

```go
type ChatCompletionRequest struct {
    // ...existing fields...
    AIGateway *AIGatewayExtensions `json:"aigateway.envoy.io,omitempty"`
}

type AIGatewayExtensions struct {
    JSONPatches map[string]JSONPatchSet `json:"json_patches,omitempty"`
}

type JSONPatchSet []JSONPatch

type JSONPatch struct {
    Op    string      `json:"op"`
    Path  string      `json:"path"`
    Value interface{} `json:"value,omitempty"`
    From  string      `json:"from,omitempty"`
}
```

The `json_patches` map uses keys that correspond to:
- Backend APISchemaName values (e.g., "GCPVertexAI", "AWSBedrock")
- "ANY" for patches that apply to all backends

### Processing Pipeline

The JSON patch processing follows this pipeline:

1. **Request Parsing**: Extract `extra_body.aigateway.envoy.io.json_patches` during request body parsing in the router filter.
2. **Backend Selection**: Determine the target backend and its APISchemaName (no change).
3. **Translation**: Apply standard request translation using existing translators (no change).
4. **Patch Application**: Apply relevant JSON patches in order:
   - First apply "ANY" patches (universal patches)
   - Then apply backend-specific patches matching APISchemaName
5. **Request Forwarding**: Send the patched request to the backend (no change).

## Examples

### API specific Example

```json
{
  "model": "gemini-2.5-flash-001",
  "messages": [{"role": "user", "content": "Hello"}],
  "aigateway.envoy.io": {
    "json_patches": {
      "GCPVertexAI": [
        {
          "op": "add",
          "path": "/safety_settings/category",
          "value": "HARM_CATEGORY_DANGEROUS_CONTENT"
        },
        {
          "op": "add",
          "path": "/safety_settings/threshold",
          "value": "BLOCK_MEDIUM_AND_ABOVE"
        }
      ]
    }
  }
}
```

### Generic ANY Example

```json
{
  "model": "gemini-2.5-flash-001",
  "messages": [{"role": "user", "content": "Hello"}],
  "aigateway.envoy.io": {
    "json_patches": {
      "ANY": [
        {
          "op": "add",
          "path": "/metadata",
          "value": {
            "requestId": "custom-12345",
            "source": "ai-gateway"
          }
        }
      ]
    }
  }
}
```

## Open Questions

1. **Path Validation**:
   - Should we allow modifications to any field in the request body?
   - Should modifications to specific sections (listed in below table) be blocked/allowed?

   **Anatomy of a Request**

    | Section/API                          | [OpenAI] ChatCompletion         | [Gemini] GenerateContent | [AWS] Converse  |
    |--------------------------------------|---------------------------------|--------------------------|-----------------|
    | User/Assistant Messages              | messages                        | contents                 | messages        |
    | systemInstruction                    | messages                        | systemInstruction        | system          |
    | Cache                                | NA                              | cachedContent            | NA              |
    | tools/toolConfig                     | tools                           | tools, toolConfig        | toolConfig      |
    | safetySettings                       | NA                              | safetySettings           | guardrailConfig |
    | GenerationConfig (top_p, top_k, etc) | individual fields at root level | generationConfig         | inferenceConfig |


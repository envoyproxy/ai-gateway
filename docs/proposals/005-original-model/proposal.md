# Original Model Tracking

## Table of Contents

<!-- toc -->

- [Summary](#summary)
- [Background](#background)
- [Design](#design)
  - [Model Tracking Flow](#model-tracking-flow)
  - [Example Scenario](#example-scenario)
- [Implementation](#implementation)
  - [Naming Rationale](#naming-rationale)
  - [OpenTelemetry Integration](#opentelemetry-integration)
- [Prior Art](#prior-art)

<!-- /toc -->

## Summary

This proposal introduces `OriginalModel` as the model name extracted from the incoming request body before any overrides are applied. This allows metrics, such as `gen_ai.server.token.usage`, to be aggregated by either the original model requested by the client (`OriginalModel`) or the overridden model sent to the backend (`RequestModel`), offering improved visibility into model usage patterns and client behavior.

## Background

The Envoy AI Gateway currently tracks two model identifiers:

- **RequestModel**: The model name sent to the backend (which may be overridden)
- **ResponseModel**: The actual model used by the backend

When model name overrides are configured, however, the original client request is not preserved. This creates challenges in:

- Understanding which models clients are requesting versus what is actually served
- Monitoring adoption patterns for new model versions
- Auditing model usage for compliance and billing
- Debugging issues related to model routing and overrides

## Design

### Model Tracking Flow

The gateway will track three distinct model identifiers during request processing:

1. **OriginalModel**: Extracted from the request body upon receipt
2. **RequestModel**: Derived from OriginalModel or an override configuration
3. **ResponseModel**: Extracted from the provider's response

```
Client Request → Router Filter → Upstream Filter → Backend
     ↓               ↓                ↓              ↓
{"model":"gpt-5"}   Extract      Override to    Response:
                  OriginalModel   "gpt-5-nano"   "gpt-5-nano-2025-08-07"
                    ="gpt-5"      RequestModel    ResponseModel
```

### Example Scenario

Consider a client requesting `gpt-5`, which the gateway overrides to `gpt-5-nano` based on configuration, and OpenAI responds with the specific version `gpt-5-nano-2025-08-07`:

1. **OriginalModel**: `gpt-5` (from the client request body)
2. **RequestModel**: `gpt-5-nano` (after applying ModelNameOverride)
3. **ResponseModel**: `gpt-5-nano-2025-08-07` (from the OpenAI response)

## Implementation

### Naming Rationale

The term "original" aligns with established Envoy conventions:

- Envoy uses `x-envoy-original-path` for the unmodified request path
- The AI Gateway employs `x-ai-eg-original-path` internally
- Documentation frequently refers to unmodified values as "the original" in comments

```go
// originalPathHeader is the header used to pass the original path to the processor.
// This is used in the upstream filter level to determine the original path of the request on retry.
const originalPathHeader = "x-ai-eg-original-path"
```

Alternatives considered but rejected include:

**"client"**: Intuitive in a client-server context, but less precise than "original" in emphasizing the unmodified state before processing; it could also be confused with post-override requests.

**"requested"**: Used in production systems like LiteLLM, but already assigned in existing contexts, potentially causing confusion. It is more action-oriented but does not as clearly convey the unmodified status as "original."

"Original" was selected for its:

- Consistency with HTTP proxy standards (e.g., X-Forwarded-\* headers)
- Emphasis on the unmodified state prior to gateway processing
- Applicability in both simple and complex routing scenarios
- Minimal risk of conflicting with existing OpenTelemetry attributes

### OpenTelemetry Integration

In OpenTelemetry Generative AI Metrics, the original model will be exposed as an attribute on metrics like `gen_ai.server.token.usage`:

```
gen_ai.original.model = "gpt-5"
gen_ai.request.model = "gpt-5-nano"
gen_ai.response.model = "gpt-5-nano-2025-08-07"
```

#### Metric Attribute Naming

The attribute `gen_ai.original.model` adheres to these principles:

- Placement under the `gen_ai` namespace, consistent with other model attributes
- Clear, unambiguous semantic meaning
- Flexibility for renaming via an OTEL collector if required
- Foundation for a potential future semantic convention proposal

No OpenTelemetry specification currently exists for an original model attribute. This implementation provides real-world testing before proposing a formal semantic convention to the OpenTelemetry community.

#### Collector-Based Customization

For organizations preferring to track only two model attributes, the OpenTelemetry Collector's [transform processor](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/transformprocessor/README.md) can customize the metrics pipeline. For instance, to replace `gen_ai.request.model` with the original model:

```yaml
receivers:
  otlp:
    protocols:
      grpc:

processors:
  transform:
    metric_statements:
      - context: datapoint
        statements:
          # Replace request.model with original.model value
          - delete_key(attributes, "gen_ai.request.model")
          - set(attributes["gen_ai.request.model"], attributes["gen_ai.original.model"])
          - delete_key(attributes, "gen_ai.original.model")

exporters:
  logging:

service:
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [transform]
      exporters: [logging]
```

This enables tailored observability strategies while ensuring complete data capture at the source.

## Prior Art

Analysis of existing AI gateways shows diverse approaches to model tracking:

| Gateway              | Metric Name                                               | Original Model         | Request Model          | Response Model          | Override Support                            |
| -------------------- | --------------------------------------------------------- | ---------------------- | ---------------------- | ----------------------- | ------------------------------------------- |
| **AgentGateway**     | [`gen_ai_client_token_usage`][agentgateway-metrics]       | `gen_ai_request_model` | N/A                    | `gen_ai_response_model` | Config file per-provider                    |
| **Envoy AI Gateway** | [`gen_ai.client.token.usage`][ai-gateway-metrics]         | `gen_ai.request.model` | N/A                    | `gen_ai.response.model` | Backend config override                     |
| **Bifrost**          | [`bifrost_upstream_requests_total`][bifrost-metrics]      | N/A                    | `model`                | N/A                     | N/A                                         |
| **Higress**          | [`ai-statistics`][higress-metrics]                        | N/A                    | `gen_ai.request.model` | N/A                     | Provider-specific in ai-proxy               |
| **Kong AI Gateway**  | [`ai.*.meta`][kong-metrics]                               | N/A                    | `request_model`        | `response_model`        | Provider-specific                           |
| **Labring AI Proxy** | [`summary_minutes`][labring-metrics]                      | N/A                    | `model`                | N/A                     | Channel-based model mapping                 |
| **LiteLLM**          | [`litellm_deployment_success_responses`][litellm-metrics] | `requested_model`      | `litellm_model_name`   | N/A                     | Config file aliasing and deployment mapping |
| **Llama Stack**      | [`prompt_tokens`][llama-stack-metrics]                    | N/A                    | `model_id`             | N/A                     | N/A                                         |

Key observations:

- Most gateways do not differentiate between original and overridden models
- LiteLLM stands out as the primary proxy tracking both (`requested_model` vs. `litellm_model_name`)
- Attribute naming lacks standardization across implementations

This proposal fills the gap by enabling full-lifecycle model tracking.

[agentgateway-metrics]: https://github.com/agentgatewayai/agentgateway/blob/main/crates/agentgateway/src/telemetry/metrics.rs#L31
[ai-gateway-metrics]: https://github.com/envoyproxy/ai-gateway/blob/main/internal/metrics/genai.go#L14
[bifrost-metrics]: https://github.com/maximhq/bifrost/blob/main/plugins/telemetry/main.go#L37
[higress-metrics]: https://github.com/alibaba/higress/blob/main/plugins/wasm-go/extensions/ai-statistics/main.go#L78
[kong-metrics]: https://github.com/Kong/kong/blob/master/kong/llm/plugin/shared-filters/serialize-analytics.lua#L53
[labring-metrics]: https://github.com/labring/aiproxy/blob/main/core/model/summary-minute.go#L15
[litellm-metrics]: https://github.com/BerriAI/litellm/blob/main/litellm/proxy/proxy_server.py#L1180
[llama-stack-metrics]: https://github.com/meta-llama/llama-stack/blob/main/llama_stack/core/routers/inference.py#L150

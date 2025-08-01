# Custom Metrics External Processor (ExtProc) Example

This example shows how to replace the default metrics implementation. This
involves building your own External Processor (ExtProc) which assigns
`x.NewCustomChatCompletionMetrics` to your requirements.

## Default request/response flow

By default, the AI Gateway:

1. **Records OpenTelemetry GenAI metrics** for each request:
    - `gen_ai.client.token.usage` - Input/output/total tokens
    - `gen_ai.server.request.duration` - Total request time
    - `gen_ai.server.time_to_first_token` - Time to first token (streaming)
    - `gen_ai.server.time_per_output_token` - Inter-token latency (streaming)

2. **Exports metrics** via Prometheus on the `/metrics` endpoint

3. **Adds performance data to dynamic metadata** for downstream use:
    - `token_latency_ttft` - Time to first token in milliseconds (streaming)
    - `token_latency_itl` - Inter-token latency in milliseconds (streaming)

In summary, metrics are exported for external APM use, and some added as Envoy
dynamic metadata. Envoy dynamic metadata allows downstream services access to
real-time performance data for analysis, routing decisions, or client-side
observability.

## What this example does

This example **replaces** the default `x.NewCustomChatCompletionMetrics`
with one that:

1. **Logs each metrics event** to stdout for debugging
2. **Returns fixed test values** for TTFT (1234ms) and ITL (5678ms)
3. **Skips recording actual metrics** (no-op implementations)

The fixed values make it easy to verify the integration is working - you'll see
`TTFT=1234 ITL=5678` in Envoy access logs.

## What this feature was designed for

`x.NewCustomChatCompletionMetrics` was designed to allow advanced mon

1. **Enhanced performance tracking**:
    - Track P50/P90/P99 latencies per model
    - Monitor token generation speed trends
    - Detect performance degradation

2. **Cost optimization**:
    - Track actual vs estimated token costs
    - Monitor cost per request/user/model
    - Alert on cost anomalies

3. **Custom observability**:
    - Send metrics to proprietary monitoring systems
    - Add business-specific attributes
    - Correlate with application metrics

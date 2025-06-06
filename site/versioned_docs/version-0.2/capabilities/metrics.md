---
id: metrics
title: AI/LLM Metrics
sidebar_position: 6
---

When using the Envoy AI Gateway, it will collect AI specific metrics and expose them to Prometheus for monitoring by default.
This guide provides an overview of the metrics collected by the AI Gateway and how to monitor them using Prometheus.

## Overview

Envoy AI Gateway is designed to intercept and process AI/LLM requests, that enables it to collect metrics for monitoring and observability.
Currently, it collects metrics and exports them to Prometheus for monitoring in the OpenTelemetry format as specified by the [OpenTelemetry Gen AI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/attributes-registry/gen-ai/).
Not all metrics are supported yet, but the Envoy AI Gateway will continue to add more metrics in the future.

For example, the Envoy AI Gateway collects metrics such as:
* [**`gen_ai.client.token.usage`**](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiclienttokenusage): Number of tokens processed. The label `gen_ai_token_type` can be used to differentiate between input, output, and total tokens.
* [**`gen_ai.server.request.duration`**](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiserverrequestduration): Measured from the start of the received request headers in the Envoy AI Gateway filter to the end of the processed response body processing.
* [**`gen_ai.server.time_to_first_token`**](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_to_first_token): Measured from the start of the received request headers in the Envoy AI Gateway filter to the receiving of the first token in the response body handling.
* [**`gen_ai.server.time_per_output_token`**](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_per_output_token): The latency between consecutive tokens, if supported, or by chunks/tokens otherwise.

Each metric comes with some default labels such as `gen_ai_request_model` that contains the model name, etc.

## Trying it out

Before you begin, you'll need to complete the basic setup from the [Basic Usage](../getting-started/basic-usage.md) guide.

Then, you can install the prometheus using the following commands:

```shell
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/monitoring/monitoring.yaml
```

Let's wait for a while until the Prometheus is up and running.
```shell
kubectl wait --for=condition=ready pod -l app=prometheus -n monitoring
```

To access the Prometheus dashboard, you need to port-forward the Prometheus service to your local machine like this:

```shell
kubectl port-forward -n monitoring svc/prometheus 9090:9090
```

Now open your browser and navigate to `http://localhost:9090` to access the Prometheus dashboard to explore the metrics.

Alternatively, you can make the following requests to see the raw metrics:

```shell
curl http://localhost:9090/api/v1/query --data-urlencode \
  'query=sum(gen_ai_client_token_usage_sum{gateway_envoyproxy_io_owning_gateway_name = "envoy-ai-gateway-basic"}) by (gen_ai_request_model, gen_ai_token_type)' \
    | jq '.data.result[]'
```

and then you would get the response like this, assuming you have made some requests with the model `gpt-4o-mini`:

```json lines
{
  "metric": {
    "gen_ai_request_model": "gpt-4o-mini",
    "gen_ai_token_type": "input"
  },
  "value": [
    1743105857.684,
    "12"
  ]
}
{
  "metric": {
    "gen_ai_request_model": "gpt-4o-mini",
    "gen_ai_token_type": "output"
  },
  "value": [
    1743105857.684,
    "13"
  ]
}
{
  "metric": {
    "gen_ai_request_model": "gpt-4o-mini",
    "gen_ai_token_type": "total"
  },
  "value": [
    1743105857.684,
    "25"
  ]
}
```

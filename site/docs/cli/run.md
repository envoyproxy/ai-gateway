---
id: aigwrun
title: aigw run
sidebar_position: 2
---

# `aigw run`

## Overview

This command runs the Envoy AI Gateway locally as a standalone proxy with a given configuration file without any dependencies such as docker or Kubernetes.
Since the project is primarily focused on the Kubernetes environment, this command is useful for testing the configuration locally before deploying it to a Kubernetes cluster.
Not only does it help in testing the configuration, but it is also useful in a local development environment of the provider-agnostic AI applications.

:::warning
Currently, `aigw run` supports Linux and macOS.
:::

## Quick Start

The simplest way to get started is to have `aigw` generate a configuration for
you given OpenAI environment variables of your backend. Here are some examples:

```bash
# OpenAI
OPENAI_API_KEY=sk-your-key aigw run
# Tetrate Agent Router Service (TARS)
OPENAI_BASE_URL=https://api.router.tetrate.ai/v1 OPENAI_API_KEY=sk-your-key aigw run
# Ollama running locally
OPENAI_BASE_URL=http://localhost:11434/v1 OPENAI_API_KEY=unused aigw run
```

Now, the AI Gateway is running locally with the default configuration serving at `localhost:1975`.

Then, open a new terminal and run the following curl commands to test the AI Gateway.
For example, use `qwen2.5:0.5b` model to route to Ollama, assuming Ollama is running locally and `ollama pull qwen2.5:0.5b` is executed to pull the model.

```shell
curl -H "Content-Type: application/json" -XPOST http://localhost:1975/v1/chat/completions \
    -d '{"model": "qwen2.5:0.5b","messages": [{"role": "user", "content": "Say this is a test!"}]}'
```

### Supported OpenAI Environment Variables

| Variable          | Required | Default                     | Description                                          |
|-------------------|----------|-----------------------------|------------------------------------------------------|
| `OPENAI_API_KEY`  | Yes      | -                           | API key for authentication (use "unused" for Ollama) |
| `OPENAI_BASE_URL` | No       | `https://api.openai.com/v1` | Base URL of your OpenAI-compatible backend           |


## Custom Configuration

To run the AI Gateway with a custom configuration, provide the path to the configuration file as an argument to the `aigw run` command.
For example, to run the AI Gateway with a custom configuration file named `config.yaml`, run the following command:

```shell
aigw run config.yaml
```

The configuration uses the same API as the Envoy AI Gateway custom resources definitions. See [API Reference](../api/) for more information.

The best way to start customizing the configuration is to start with an [example configuration](https://github.com/envoyproxy/ai-gateway/tree/main/examples) and modify it as needed.

### Modify an Example Configuration

First, download an example configuration to use as a starting point:

```shell
curl -o ollama.yaml https://raw.githubusercontent.com/envoyproxy/ai-gateway/refs/heads/main/examples/aigw/ollama.yaml
```

Next, let's say change the model matcher from `.*` (match all) to specifically match `deepseek-r1:1.5b` and save the configuration to `custom.yaml`:

```diff
--- ollama.yaml
+++ custom.yaml
@@ -88,7 +88,7 @@
         - headers:
             - type: RegularExpression
               name: x-ai-eg-model
-              value: .*
+              value: deepseek-r1:1.5b
```

You can also use environment variable substitution (`envsubst`) to allow small
changes without needing to copy a file. For example, you could use this syntax
instead, to default the model to the `CHAT_MODEL` variable.

```diff
--- ollama.yaml
+++ custom.yaml
@@ -88,7 +88,7 @@
         - headers:
             - type: RegularExpression
               name: x-ai-eg-model
-              value: .*
+              value: ${CHAT_MODEL:=deepseek-r1:1.5b}
```

### Run with the Custom Configuration

Now, run the AI Gateway with the custom configuration by running the following command:

```shell
aigw run custom.yaml
```

Now, the AI Gateway is running locally with the custom configuration serving at `localhost:1975`.

```shell
curl -H "Content-Type: application/json" -XPOST http://localhost:1975/v1/chat/completions \
    -d '{"model": "deepseek-r1:1.5b","messages": [{"role": "user", "content": "Say this is a test!"}]}'
```

### Prometheus Metrics

While running, `aigw` serves prometheus metrics at `localhost:1064/metrics` by default. You can scrape this for [LLM/AI metrics](../capabilities/observability/metrics.md).

## OpenTelemetry

Envoy AI Gateway's router joins and records distributed traces when supplied
with an [OpenTelemetry](https://opentelemetry.io/) collector endpoint.

Requests to the OpenAI Chat Completions endpoint are recorded as Spans which
include typical timing and request details. In addition, there are GenAI
attributes representing the LLM call including full request and response
details, defined by the [OpenInference semantic conventions][openinference].

OpenInference attributes default to include full chat completion request and
response data. This can be toggled with configuration, but when enabled allows
systems like [Arize Phoenix][phoenix] to perform LLM evaluations of production
requests captured in OpenTelemetry spans.

### OpenTelemetry configuration

`aigw run` supports OpenTelemetry tracing via environment variables:

- **[OTEL SDK][otel-env]**: OTLP exporter configuration that controls span
  and metrics export such as:
    - `OTEL_EXPORTER_OTLP_ENDPOINT`: Collector endpoint (e.g., `http://phoenix:6006`)
    - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`: Override traces endpoint separately
    - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`: Override metrics endpoint separately
    - `OTEL_TRACES_EXPORTER`: Traces exporter type (e.g., `console`, `otlp`, `none`)
    - `OTEL_METRICS_EXPORTER`: Metrics exporter type (e.g., `console`, `otlp`, `none`)
    - `OTEL_BSP_SCHEDULE_DELAY`: Batch span processor delay (default: 5000ms)
    - `OTEL_METRIC_EXPORT_INTERVAL`: Metrics export interval (default: 60000ms)

- **[OpenInference][openinference-config]**: Control sensitive data redaction,
  such as:
    - `OPENINFERENCE_HIDE_INPUTS`: Hide input messages/prompts (default: `false`)
    - `OPENINFERENCE_HIDE_OUTPUTS`: Hide output messages/completions (default: `false`)

See [docker-compose-otel.yaml][docker-compose-otel.yaml] for a complete example
configuration.

---
[openinference]: https://github.com/Arize-ai/openinference/tree/main/spec
[phoenix]: https://docs.arize.com/phoenix
[otel-env]: https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
[openinference-config]: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
[docker-compose-otel.yaml]: https://github.com/envoyproxy/ai-gateway/blob/main/cmd/aigw/docker-compose-otel.yaml

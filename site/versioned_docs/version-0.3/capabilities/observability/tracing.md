---
id: tracing
title: GenAI Distributed Tracing
sidebar_position: 7
---

Envoy AI Gateway's router joins and records distributed traces when supplied
with an [OpenTelemetry](https://opentelemetry.io/) collector endpoint.

This guide provides an overview of the spans recorded by the AI Gateway and how
export them to your choice of OpenTelemetry collector.

## Overview

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

## Trying it out

Before you begin, you'll need to complete the basic setup from the
[Basic Usage](/docs/getting-started/basic-usage) guide, which includes
installing Envoy Gateway and AI Gateway.

### Install Phoenix for LLM observability

```shell
# Install Phoenix using PostgreSQL storage.
helm install phoenix oci://registry-1.docker.io/arizephoenix/phoenix-helm \
  --namespace envoy-ai-gateway-system \
  --set auth.enableAuth=false \
  --set server.port=6006
```

### Configure AI Gateway with OpenTelemetry

Upgrade your AI Gateway installation with [OpenTelemetry configuration][otel-config]:

```shell
helm upgrade ai-eg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.3.0 \
  --namespace envoy-ai-gateway-system \
  --set "extProc.extraEnvVars[0].name=OTEL_EXPORTER_OTLP_ENDPOINT" \
  --set "extProc.extraEnvVars[0].value=http://phoenix-svc:6006"
# OTEL_SERVICE_NAME defaults to "ai-gateway" if not set
```

Wait for the gateway pod to be ready:

```shell
kubectl wait --for=condition=Ready -n envoy-gateway-system \
  pods -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic
```

### Generate traces

Make requests to your AI Gateway to generate traces. Follow the instructions
from [Testing the Gateway](/docs/getting-started/basic-usage#testing-the-gateway)
in the Basic Usage guide to:

1. Set up port forwarding (if needed)
2. Test the chat completions endpoint

Each request will generate traces that are sent to Phoenix.

### Check Phoenix is receiving traces

```bash
kubectl logs -n envoy-ai-gateway-system deployment/phoenix | grep "POST /v1/traces"
```

### Access Phoenix UI

Port-forward to access the Phoenix dashboard:

```shell
kubectl port-forward -n envoy-ai-gateway-system svc/phoenix 6006:6006
```

Then open http://localhost:6006 in your browser to explore the traces.

## Privacy Configuration

Control sensitive data in traces by adding
[OpenInference configuration][openinference-config] to Helm values when you
reconfigure the AI Gateway:

For example, if you are using a `values.yaml` file instead of command line
arguments, you can add the following to control redaction:

```yaml
extProc:
  extraEnvVars:
    # Base OTEL configuration...
    # Hide sensitive data (all default to false)
    - name: OPENINFERENCE_HIDE_INPUTS
      value: "true" # Hide input messages to the LLM
    - name: OPENINFERENCE_HIDE_OUTPUTS
      value: "true" # Hide output messages from the LLM
```

Note: Hiding inputs/outputs prevents human or LLM-as-a-Judge evaluation of your
LLM requests, such as done with the [Phoenix Evals library][phoenix-evals].

## Cleanup

To remove Phoenix and disable tracing:

```shell
# Uninstall Phoenix
helm uninstall phoenix -n envoy-ai-gateway-system

# Disable tracing in AI Gateway
helm upgrade ai-eg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.3.0 \
  --namespace envoy-ai-gateway-system \
  --reuse-values \
  --unset extProc.extraEnvVars
```

## See Also

- [OpenInference Specification][openinference] - GenAI Semantic conventions for traces
- [OpenTelemetry Configuration][otel-config] - Environment variable reference
- [Arize Phoenix Documentation][phoenix] - LLM observability platform

---

[openinference]: https://github.com/Arize-ai/openinference/tree/main/spec
[openinference-config]: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
[otel-config]: https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
[phoenix]: https://docs.arize.com/phoenix
[phoenix-evals]: https://arize.com/docs/phoenix/evaluation/llm-evals

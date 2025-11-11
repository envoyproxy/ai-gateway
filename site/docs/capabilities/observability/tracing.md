---
id: tracing
title: GenAI Distributed Tracing
sidebar_position: 7
---

import CodeBlock from '@theme/CodeBlock';
import vars from '../../_vars.json';

Envoy AI Gateway's router joins and records distributed traces when supplied
with an [OpenTelemetry](https://opentelemetry.io/) collector endpoint.

This guide provides an overview of the spans recorded by the AI Gateway and how
export them to your choice of OpenTelemetry collector.

## Overview

Envoy AI Gateway's router joins and records distributed traces when supplied
with an [OpenTelemetry](https://opentelemetry.io/) collector endpoint.

Requests to the OpenAI Chat Completions, Completions (legacy), and Embeddings
endpoints are recorded as Spans which include typical timing and request
details. In addition, there are GenAI attributes representing the LLM or
Embeddings call including full request and response details, defined by
[OpenInference semantic conventions][openinference].

OpenInference attributes default to include full request and response data for
both chat completions and embeddings. This can be toggled with configuration,
but when enabled allows systems like [Arize Phoenix][phoenix] to perform
evaluations of production requests captured in OpenTelemetry spans.

For chat completions, this includes traditional LLM metrics such as correctness
and hallucination detection. For embeddings, it enables agentic RAG evaluations
focused on retrieval and semantic analysis.

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

<CodeBlock language="shell">
{`helm upgrade ai-eg oci://docker.io/envoyproxy/ai-gateway-helm \\
    --version v${vars.aigwVersion} \\
    --namespace envoy-ai-gateway-system \\
    --set "extProc.extraEnvVars[0].name=OTEL_EXPORTER_OTLP_ENDPOINT" \\
    --set "extProc.extraEnvVars[0].value=http://phoenix-svc:6006" \\
    --set "extProc.extraEnvVars[1].name=OTEL_METRICS_EXPORTER" \\
    --set "extProc.extraEnvVars[1].value=none"
# OTEL_SERVICE_NAME defaults to "ai-gateway" if not set
# OTEL_METRICS_EXPORTER=none because Phoenix only supports traces, not metrics`}
</CodeBlock>

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
reconfigure the AI Gateway. There is [similar config][openinference-embeddings]
for embeddings:

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
    # Reduce volume for embeddings (all default to false)
    - name: OPENINFERENCE_HIDE_EMBEDDINGS_TEXT
      value: "true" # Hide embeddings input
    - name: OPENINFERENCE_HIDE_EMBEDDINGS_VECTORS
      value: "true" # Hide embeddings output
```

Note: Hiding inputs/outputs prevents human or LLM-as-a-Judge evaluation of your
LLM requests, such as done with the [Phoenix Evals library][phoenix-evals].

## Session Tracking

Sessions help track and organize related traces across multi-turn conversations
with your AI app. Maintaining context between interactions is key for
observability.

With sessions, you can:

- Track a conversation's full history in one thread.
- View inputs/outputs for a given agent.
- Monitor token usage and latency per conversation.

By tagging spans with a consistent session ID, you get a connected view of
performance across a user's journey.

The challenge is that requests to the gateway may not send traces, making
grouping difficult. Many GenAI frameworks allow you to set custom HTTP headers
when sending traffic to an LLM. Propagating sessions this way is simpler than
instrumenting applications with tracing code and can still achieve grouping.

There's no standard name for session ID headers, but there is a common attribute
in OpenTelemetry, [session.id][otel-session], which has special handling in some
OpenTelemetry platforms such as [Phoenix][phoenix-session].

To bridge this gap, Envoy AI Gateway has two configurations to map HTTP request
headers to OpenTelemetry attributes, one for spans and one for metrics.

- `controller.spanRequestHeaderAttributes`
- `controller.metricsRequestHeaderAttributes`

Both of these use the same value format: a comma-separated list of
`<http-header>:<otel-attribute>` pairs. For example, if your session ID header
is `x-session-id`, you can map it to the standard OpenTelemetry attribute
`session.id` like this: `x-session-id:session.id`.

Some metrics systems will be able to do fine-grained aggregation, but not all.
Here's an example of setting the session ID header for spans, but not metrics:

<CodeBlock language="shell">
{`helm upgrade ai-eg oci://docker.io/envoyproxy/ai-gateway-helm \\
    --version v${vars.aigwVersion} \\
    --namespace envoy-ai-gateway-system \\
    --reuse-values \\
    --set "controller.metricsRequestHeaderAttributes=x-user-id:user.id" \\
    --set "controller.spanRequestHeaderAttributes=x-session-id:session.id,x-user-id:user.id"`}
</CodeBlock>

## Cleanup

To remove Phoenix and disable tracing:

<CodeBlock language="shell">
{`# Uninstall Phoenix
helm uninstall phoenix -n envoy-ai-gateway-system

# Disable tracing in AI Gateway
helm upgrade ai-eg oci://docker.io/envoyproxy/ai-gateway-helm \\
    --version v${vars.aigwVersion} \\
    --namespace envoy-ai-gateway-system \\
    --reuse-values \\
    --unset extProc.extraEnvVars`}
</CodeBlock>

## See Also

- [OpenInference Specification][openinference] - GenAI Semantic conventions for traces
- [OpenTelemetry Configuration][otel-config] - Environment variable reference
- [Arize Phoenix Documentation][phoenix] - LLM observability platform

---

[openinference]: https://github.com/Arize-ai/openinference/tree/main/spec
[openinference-config]: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
[openinference-embeddings]: https://github.com/Arize-ai/openinference/blob/main/spec/embedding_spans.md
[otel-config]: https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
[phoenix]: https://docs.arize.com/phoenix
[phoenix-evals]: https://arize.com/docs/phoenix/evaluation/llm-evals
[otel-session]: https://opentelemetry.io/docs/specs/semconv/registry/attributes/session/
[phoenix-session]: https://arize.com/docs/phoenix/tracing/llm-traces/sessions

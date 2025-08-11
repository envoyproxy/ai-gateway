# Envoy AI Gateway CLI (aigw)

## Quick Start with Docker Compose

[docker-compose.yml](docker-compose.yaml) builds and runs `aigw`, targeting
Ollama and listening for OpenAI chat completion requests on port 1975.

1. **Start Ollama** on your host machine:

   ```bash
   OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. **Run the stack**:

   ```bash
   # Start the stack (from this directory)
   docker compose up --wait -d

   # Send a test request
   docker compose run --rm openai-client

   # Stop everything
   docker compose down -v
   ```

## OpenTelemetry

The AI Gateway uses [OpenTelemetry](https://opentelemetry.io/) for distributed
tracing. [OpenInference semantic conventions][openinference] define the
attributes recoded in the spans sent to your choice of OpenTelemetry collector.

OpenInference attributes default to include full chat completion request and
response data. This can be toggled with configuration, but when enabled allows
systems like [Arize Phoenix][phoenix] to perform LLM evaluations of production
requests captured in OpenTelemetry spans.

### OpenTelemetry configuration

The Envoy AI Gateway supports OpenTelemetry tracing via environment variables:

- **[OTEL SDK][otel-env]**: OTLP exporter configuration that controls span
  export such as:
    - `OTEL_EXPORTER_OTLP_ENDPOINT`: Collector endpoint (e.g., `http://phoenix:6006`)
    - `OTEL_BSP_SCHEDULE_DELAY`: Batch span processor delay (default: 5000ms)

- **[OpenInference][openinference-config]**: Control sensitive data redaction,
  such as:
    - `OPENINFERENCE_HIDE_INPUTS`: Hide input messages/prompts (default: `false`)
    - `OPENINFERENCE_HIDE_OUTPUTS`: Hide output messages/completions (default: `false`)

See [docker-compose-otel.yaml](docker-compose-otel.yaml) for a complete example configuration.

### OpenTelemetry Quick Start with Docker Compose

[docker-compose-otel.yaml](docker-compose-otel.yaml) includes OpenTelemetry tracing,
visualized with [Arize Phoenix](https://phoenix.arize.com), an open-source
OpenTelemetry LLM tracing and evaluation system. It has UX features for LLM
spans formatted with [OpenInference semantics][openinference].

- **aigw** (port 1975): Envoy AI Gateway CLI (standalone mode) with OTEL tracing
- **Phoenix** (port 6006): OpenTelemetry trace viewer UI for LLM observability
- **openai-client**: OpenAI Python client instrumented with OpenTelemetry

1. **Start Ollama** on your host machine:
   ```bash
   OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. **Run the stack with OpenTelemetry and Phoenix**:
   ```bash
   # Start the stack with Phoenix (from this directory)
   docker compose -f docker-compose-otel.yaml up --wait -d

   # Send a test request
   docker compose -f docker-compose-otel.yaml run --build --rm openai-client

   # Verify traces are being sent
   docker compose -f docker-compose-otel.yaml logs phoenix | grep "POST /v1/traces"

   # View traces in Phoenix UI
   open http://localhost:6006

   # Stop everything
   docker compose -f docker-compose-otel.yaml down -v
   ```

---
[openinference]: https://github.com/Arize-ai/openinference/tree/main/spec
[phoenix]: https://phoenix.arize.com
[otel-env]: https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
[openinference-config]: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md

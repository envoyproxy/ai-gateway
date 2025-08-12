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

### OpenTelemetry Quick Start with Docker Compose

[docker-compose-otel.yaml](docker-compose-otel.yaml) includes OpenTelemetry
tracing, visualized with [Arize Phoenix][phoenix], an open-source LLM tracing
and evaluation system. It has UX features for LLM spans formatted with
[OpenInference semantics][openinference].

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
[phoenix]: https://docs.arize.com/phoenix

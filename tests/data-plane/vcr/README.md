# AI Gateway ExtProc Tests

Integration tests for AI Gateway's external processor. Tests here build
`extproc` on demand and run Envoy using ephemeral ports to avoid conflicts.

## Running Tests

```bash
# From the current directory - no need for make
cd tests/data-plane/vcr
go test -v

# Run specific tests
go test -v -run TestChatCompletions/chat-basic

# To use a specific extproc binary (optional)
EXTPROC_BIN=/path/to/extproc go test -v
```

### OpenTelemetry Tests

The test suite includes OpenTelemetry tracing tests that verify:

- Proper recording of LLM attribute layout (OpenInference semantics)
- Trace context propagation
- Error handling and span status for API failures

```bash
# Run OpenTelemetry tests
go test -v -run TestOtel_*
```

## Recording HTTP Sessions

The tests use the [testopenai](../../internal/testopenai) package to
simulate OpenAI API responses. This package provides a test OpenAI server that
replays pre-recorded API interactions (cassettes), ensuring tests are fast,
deterministic, and can run without API keys.

## Ad Hoc Tests

You can run the ExtProc with Envoy and Ollama using Docker Compose to debug
issues found in automated tests.

### Basic Setup

[docker-compose.yml](docker-compose.yaml) sets up the following:

- **Envoy** (port 1062): Ingress proxy with ExtProc filter that routes OpenAI requests to Ollama
- **ExtProc**: Adds OpenInference tracing (internal ports: gRPC :1063, admin :1064 for /metrics and /health)

#### Quick Start with Docker Compose

For manual testing with real Ollama, you can use Docker Compose:

1. **Start Ollama** on your host machine:

   ```bash
   OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. **Run the stack**:

   ```bash
   # Start the stack (from this directory)
   docker compose up --force-recreate --wait -d
   
   # Send a test request
   docker compose run --rm openai-client
   
   # Stop everything
   docker compose down -v
   ```

### OpenTelemetry Setup with Phoenix

[docker-compose-otel.yaml](docker-compose-otel.yaml) includes OpenTelemetry tracing,
visualized with [Arize Phoenix](https://phoenix.arize.com), an Open-source
OpenTelemetry LLM tracing and evaluation system. It has UX features for LLM
spans formatted with [OpenInference semantics][openinference].

- **Envoy** (port 1975): Ingress proxy with ExtProc filter that routes OpenAI requests to Ollama
- **ExtProc**: Adds OpenInference tracing with OTLP export to Phoenix
- **Phoenix** (port 6006): OpenTelemetry trace viewer UI

#### Quick Start with OpenTelemetry

For manual testing with OpenTelemetry tracing and Phoenix:

1. **Start Ollama** on your host machine:

   ```bash
   OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. **Run the stack with OpenTelemetry and Phoenix**:

   ```bash
   # Start the stack with Phoenix (from this directory)
   docker compose -f docker-compose-otel.yaml up --force-recreate --wait -d
   
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

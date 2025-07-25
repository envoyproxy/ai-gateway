# AI Gateway ExtProc Tests

Integration tests for AI Gateway's external processor. Tests here build
`extproc` on demand and run Envoy using ephemeral ports to avoid conflicts.

## Running Tests

```bash
# From the current directory - no need for make
cd tests/extproc/vcr
go test -v

# Run specific tests
go test -v -run TestChatCompletions/chat-basic

# To use a specific extproc binary (optional)
EXTPROC_BIN=/path/to/extproc go test -v
```

## Recording HTTP Sessions

The tests use the [fakeopenai](../../internal/fakeopenai) package to
simulate OpenAI API responses. This package provides a fake OpenAI server that
replays pre-recorded API interactions (cassettes), ensuring tests are fast,
deterministic, and can run without API keys.

## Ad Hoc Tests

You can run the ExtProc with Envoy and Ollama using Docker Compose to debug
issues found in automated tests.

[docker-compose.yml](docker-compose.yaml) sets up the following:

- **Envoy** (port 1062): Ingress proxy with ExtProc filter that routes OpenAI requests to Ollama
- **ExtProc**: Adds OpenInference tracing (internal ports: gRPC :1063, metrics :1064, health :1065)

### Quick Start with Docker Compose

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

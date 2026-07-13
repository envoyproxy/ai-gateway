# LLM Arbitrary Routing Examples

This directory contains examples for per-request LLM backend routing via the `x-ai-eg-routing-plan` header.

For full specification and architecture details, see [LLM_ARBITRARY_ROUTING.md](../../LLM_ARBITRARY_ROUTING.md).

## Overview

The routing plan allows a custom ext_proc to control which backends are tried and in what order. Useful for:
- **Provider fallback** — Try Azure OpenAI, fall back to GCP VertexAI, then AWS Bedrock
- **Cost optimization** — Route to cheaper providers first
- **Capacity management** — Direct traffic based on backend availability

## Contents

### Configuration Files

- **`filter-config.yaml`** — Backend configuration with three providers (Azure, GCP, AWS)

### Client Examples

#### Python
```bash
pip install openai
python clients/example_python.py
```

#### JavaScript
```bash
node clients/example_javascript.js
```

#### Go
```bash
go run clients/example_go.go
```

## How It Works

1. **Custom ext_proc** sets the `x-ai-eg-routing-plan` header with a base64-encoded JSON payload
2. **AI Gateway router filter** parses the header and stores the plan
3. **Envoy routes** to a single DFP cluster
4. **AI Gateway upstream filter** picks the first backend, sets `:authority` and `:path` for DFP
5. **On failure**, Envoy retries and the upstream filter picks the next backend in the plan

## Routing Plan Format

```json
{
  "backends": ["azure-primary", "gcp-ptu", "aws-bedrock"],
  "fallbackEnabled": true
}
```

- **`backends`** — Ordered list of backend names from `filter-config.yaml`
- **`fallbackEnabled`** — When `false`, only `backends[0]` is used (default: `true`)

## Example: Azure → GCP Fallback

```
Client Request
    ↓
Custom ext_proc sets header:
  x-ai-eg-routing-plan: eyJiYWNrZW5kcyI6WyJhenVyZS1wcmltYXJ5IiwiZ2NwLXB0dSJdLCJmYWxsYmFja0VuYWJsZWQiOnRydWV9
    (decodes to: {"backends":["azure-primary","gcp-ptu"],"fallbackEnabled":true})
    ↓
AI Gateway router filter parses and stores plan
    ↓
Attempt 1: :authority=snc-oai-llmproxy-dev-eastus2.openai.azure.com
    → DFP resolves to Azure → 503 Service Unavailable
    ↓
Envoy retries
    ↓
Attempt 2: :authority=us-central1-aiplatform.googleapis.com
    → DFP resolves to GCP → 200 OK ✓
    ↓
Response to client
```

## Requirements

- AI Gateway configured with single DFP cluster
- Envoy retry policy with sufficient `num_retries`
- Custom ext_proc setting the routing plan header

See [Envoy Config Template](../../LLM_ARBITRARY_ROUTING.md#2-envoy-config-envoyaml) for details.

## Security

The routing plan header should only be set by **trusted ext_proc filters**, not external clients.
The gateway validates backend names against the configured backends before routing.

## Testing Locally

Start AI Gateway with the example config:

```bash
aigw -configPath ./examples/llm-arbitrary-routing/filter-config.yaml
```

Then run any client example:

```bash
python clients/example_python.py
```

Monitor logs for routing plan activation:

```
routing plan activated backends=[azure-primary gcp-ptu]
```

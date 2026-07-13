# LLM Arbitrary Routing

Enables arbitrary per-request backend selection and fallback for LLM requests via the `x-ai-eg-routing-plan` header.

## Overview

A custom ext_proc can set a routing plan header to control which backends are tried and in what order. Useful for:
- **Provider fallback** — Try Azure OpenAI, fall back to GCP VertexAI, then AWS Bedrock
- **Cost optimization** — Route to cheaper providers first
- **Capacity management** — Direct traffic based on backend availability

## Architecture

```
Client Request
    │
    ▼
┌─────────────────────────────────────────────────────┐
│  Your ext_proc (runs first in filter chain)          │
│  Sets: x-ai-eg-routing-plan: <base64 JSON>          │
│  {"backends":["azure-primary","gcp-ptu"],            │
│   "fallbackEnabled":true}                            │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (router filter)                 │
│  • Parses body, extracts model                       │
│  • Reads + stores routing plan                       │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
           Envoy routes to single DFP cluster
               │
               ▼
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 1   │
│  • Picks backends[0] from plan                       │
│  • Translates body + applies auth for that backend   │
│  • Sets :authority + :path for DFP                   │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
        DFP resolves hostname → sends request
               │
               ▼ (on failure, Envoy retry triggers)
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 2   │
│  • Picks backends[1] from plan                       │
│  • Re-translates original body for new backend       │
│  • Sets new :authority + :path                       │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
        DFP resolves new hostname → sends request → response to client
```

## Routing Plan Header

```
x-ai-eg-routing-plan: <base64-encoded JSON>
```

### Format

```json
{
  "backends": ["azure-primary", "gcp-ptu", "aws-bedrock"],
  "fallbackEnabled": true
}
```

- **`backends`** — Ordered list of backend names matching backend config
- **`fallbackEnabled`** — When `false`, only `backends[0]` is used (default: `true`)
- Requests without this header use standard Envoy routing (fully backward compatible)

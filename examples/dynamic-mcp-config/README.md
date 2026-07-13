# Dynamic MCP Proxy Config

Enables per-request MCP backend configuration overrides via the `x-ai-eg-mcp-dynamic-route-config` header.

## Overview

Override static filter-config.yaml with per-request backend configuration using a base64-encoded JSON header. Useful for:
- **Dynamic backend routing** — Route to different MCP servers based on request context
- **Multi-tenant deployments** — Different backends per user/tenant
- **Canary testing** — Route traffic to new backends for validation

## Architecture

```
Client Request
    │
    ▼
┌─────────────────────────────────────┐
│  Custom ext_proc                    │
│  Sets: x-ai-eg-mcp-dynamic-route-config: <base64 JSON>
│  {"name": "route1", "backends": [{...}]}
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│  MCP Proxy                          │
│  • Parses dynamic route header      │
│  • Overrides static config          │
│  • Routes to dynamic backends       │
└─────────────┬───────────────────────┘
              │
              ▼
         MCP Backend(s)
```

## Dynamic Route Config Header

```
x-ai-eg-mcp-dynamic-route-config: <base64-encoded JSON>
```

### Format

```json
{
  "name": "route1",
  "backends": [
    {
      "name": "backend1",
      "host": "mcp-server-1.example.com",
      "backendPath": "/mcp",
      "auth": {
        "apiKey": "secret-key"
      }
    }
  ],
  "forwardHeaders": ["x-user-id", "x-tenant-id"]
}
```

- **`name`** — Route identifier
- **`backends`** — List of backend configurations with Host and BackendPath
- **`forwardHeaders`** — Headers to forward to backends
- Requests without this header use static filter-config.yaml (fully backward compatible)

## Backend Configuration

Each backend can override:
- **`host`** — Upstream hostname for DFP routing
- **`backendPath`** — Custom API path on the backend
- **`auth`** — Backend authentication (API key, credentials, etc.)

## Implementation Details

| File | Change |
|---|---|
| `internalapi/internalapi.go` | `MCPDynamicRouteConfigHeader` constant |
| `filterapi/mcpconfig.go` | `Host`, `BackendPath` fields on `MCPBackend` |
| `mcpproxy/config.go` | Parse dynamic route config header |
| `mcpproxy/handlers.go` | Apply backend overrides in request handling |

### Key Behaviors

- **Backward compatible** — Requests without the header use static config
- **Per-request overrides** — Dynamic config takes precedence over static config
- **Full re-translation** — Each backend gets correct auth and headers
- **Minimal overhead** — Base64 decode + JSON unmarshal only when header is present

## Security

- The dynamic route config header should only be set by **trusted ext_proc filters**
- Backend names and configurations are validated before routing
- Unknown backend names result in an error

## Observability

Dynamic route configuration is logged when:
- Header is parsed successfully
- Backend is selected for a request
- Configuration errors occur

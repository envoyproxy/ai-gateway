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
│  Custom ext_proc (or other filter)  │
│  (Optional)                         │
│  Sets: x-ai-eg-mcp-dynamic-route... │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│  MCP Proxy                          │
│                                     │
│  ┌───────────────────────────────┐  │
│  │ Does dynamic header exist?    │  │
│  └───────┬───────────────┬───────┘  │
│         YES              NO         │
│          │               │          │
│          ▼               ▼          │
│  ┌───────────────┐ ┌─────────────┐  │
│  │ Parse base64  │ │ Load static │  │
│  │ JSON header   │ │ config from │  │
│  │               │ │ filter-config  │  │
│  └───────┬───────┘ └──────┬──────┘  │
│          │                │         │
│          └───────┬────────┘         │
│                  ▼                  │
│   Applies Host, Path, Auth headers  │
└──────────────────┬──────────────────┘
                   │ 
      (HTTP Request to backendListenerAddr)
                   │
                   ▼
┌─────────────────────────────────────┐
│  Envoy Proxy (backendListenerAddr)  │
│  • Matches Host/Path                │
│  • Routes to Static Cluster OR      │
│  • Routes to DFP Cluster            │
└─────────────┬───────────────────────┘
              │
              ▼
         MCP Backend(s)
```

### Hybrid Routing: Static + Dynamic Forward Proxy (DFP)

The MCP Proxy seamlessly supports a hybrid model of both statically defined backends and dynamic external backends on the **exact same Envoy listener** (`backendListenerAddr`).

1. **Config Resolution:**
   - **Dynamic:** If the `x-ai-eg-mcp-dynamic-route-config` header is present, the proxy decodes the JSON and uses it for the request.
   - **Static (Fallback):** If the header is missing, the proxy gracefully falls back to the in-memory route map loaded from `filter-config.yaml` at startup.

2. **HTTP Request Construction:**
   Regardless of whether the backend config came from the dynamic header or static config, the proxy applies overrides (`Host`, `BackendPath`, `Authorization`) to the outbound HTTP request and sends it to Envoy (`backendListenerAddr`).

3. **Envoy Routing (Smart Traffic Cop):**
   Envoy evaluates the request top-to-bottom:
   - **Static Routes:** If the `Host` matches a statically defined route (e.g., a local Kubernetes service), it routes to that static `Cluster`.
   - **DFP Route (Catch-all):** If the `Host` is an external SaaS API (e.g., `api.githubcopilot.com`) and matches no static route, it falls through to the Envoy **Dynamic Forward Proxy (DFP)** cluster. DFP resolves the DNS on the fly and proxies the request.

This decoupling means the MCP Proxy doesn't care if a backend is static or dynamic—it just formats the HTTP request, and Envoy handles the rest!

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
      "name": "github",
      "host": "api.githubcopilot.com",
      "backendPath": "/mcp",
      "auth": "Bearer ghp_secret",
      "toolSelector": {
        "include": ["list_issues", "search_code"]
      }
    }
  ],
  "authorization": {
    "defaultAction": "Deny",
    "rules": [
      {
        "action": "Allow",
        "source": { "jwt": { "scopes": ["mcp:admin"] } },
        "target": { "tools": [{ "backend": "github", "tool": "*" }] }
      }
    ]
  },
  "forwardHeaders": ["x-user-id", "x-tenant-id"]
}
```

- **`name`** — Route identifier
- **`backends`** — List of backend configurations (can include `toolSelector` to whitelist/blacklist tools)
- **`authorization`** — Role-based access control rules for the route (e.g., verifying JWT scopes before allowing tool execution)
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

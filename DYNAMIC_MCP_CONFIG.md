# Dynamic MCP Proxy Configuration

This document describes the changes made to support dynamic configuration of the MCP proxy,
enabling fully dynamic route and backend resolution without relying solely on the static
`filter-config.yaml` file.

## Overview

Two features were added:

1. **Dynamic Route Config via Header** — Pass an entire `MCPRoute` (backends, tool selectors,
   authorization, forward headers) per-request through an HTTP header.
2. **Backend Overrides (Host / Path / Auth)** — Each `MCPBackend` can specify a hostname, URL
   path, and authorization header, enabling Envoy's Dynamic Forward Proxy for TLS and DNS
   without static cluster definitions.

---

## 1. Dynamic Route Config via Header

### How it works

The MCP proxy checks for the `x-ai-eg-mcp-dynamic-route-config` header on every incoming
request. If present, its value is interpreted as a **base64-encoded JSON** representation of
an `MCPRoute` object. This dynamic config takes precedence over the statically loaded route
from `filter-config.yaml`.

If the header is missing or malformed, the proxy falls back to the static config silently
(with an error log on parse failure).

### Header name

```
x-ai-eg-mcp-dynamic-route-config
```

### Example: constructing the header value

Given this route config:

```json
{
  "name": "my-route",
  "backends": [
    {
      "name": "github-copilot",
      "host": "api.githubcopilot.com",
      "backendPath": "/mcp/readonly",
      "auth": "Bearer ghp_xxxxxxxxxxxx",
      "toolSelector": {
        "include": ["search_code", "list_repos"]
      }
    },
    {
      "name": "internal-tools",
      "toolSelector": {
        "exclude": ["dangerous_tool"]
      }
    }
  ],
  "authorization": {
    "defaultAction": "Allow"
  },
  "forwardHeaders": ["X-Tenant-Id"]
}
```

Base64-encode the JSON and set it as the header:

```bash
CONFIG=$(echo -n '<json above>' | base64)
curl -H "x-ai-eg-mcp-dynamic-route-config: $CONFIG" \
     -H "x-ai-eg-mcp-route: my-route" \
     http://localhost:8080/mcp
```

### Files changed

| File | Change |
|---|---|
| `internal/internalapi/internalapi.go` | Added `MCPDynamicRouteConfigHeader` constant |
| `internal/mcpproxy/config.go` | Added `parseDynamicRouteConfig()` and `buildConfigRoute()` functions |
| `internal/mcpproxy/mcpproxy.go` | Added `routeConfig()` method with lazy parsing and per-request caching |
| `internal/mcpproxy/mcpproxy.go` | Replaced all `m.routes[routeName]` lookups with `m.routeConfig(routeName)` |
| `internal/mcpproxy/handlers.go` | Replaced `m.routes[...]` lookups with `m.routeConfig(...)` |

---

## 2. Backend Overrides (Host / Path / Auth)

### How it works

Three new fields were added to `MCPBackend`:

| Field | Type | Description |
|---|---|---|
| `host` | `string` | Sets the `:authority`/`Host` header on the backend request. Used by Envoy's Dynamic Forward Proxy to resolve DNS and handle TLS. |
| `backendPath` | `string` | Rewrites the URL path on the backend request (e.g., `/mcp/readonly`). |
| `auth` | `string` | Injects the `Authorization` header on the backend request (e.g., `Bearer ghp_xxx`). |

All three are optional. When not set, the request is sent to `backendListenerAddr` unchanged
(original behavior).

The overrides are applied in **all 3 code paths** that send requests to backends:

1. `invokeJSONRPCRequest` in `mcpproxy.go` — POST for initialize/notifications
2. `sendRequestPerBackend` in `session.go` — POST/GET for tools/call, tools/list, SSE
3. `session.Close()` in `session.go` — DELETE for session cleanup

### Files changed

| File | Change |
|---|---|
| `internal/filterapi/mcpconfig.go` | Added `Host`, `BackendPath`, `Auth` fields to `MCPBackend` |
| `internal/mcpproxy/mcpproxy.go` | Added `applyBackendOverrides()` method |
| `internal/mcpproxy/mcpproxy.go` | Applied overrides in `invokeJSONRPCRequest` |
| `internal/mcpproxy/session.go` | Applied overrides in `sendRequestPerBackend` and `Close()` |

### Unchanged components

- `addMCPHeaders` — still sets `x-ai-eg-mcp-backend` and `x-ai-eg-mcp-route`
- `MCPConfig`, `MCPRoute` structs — unchanged (except `BackendListenerAddr` doc update)
- File watcher — unchanged (still watches `filter-config.yaml`)
- Tool aggregation, session management — unchanged

---

## Usage Examples

### Example 1: Static config with DFP backends (`filter-config.yaml`)

Use `backendListenerAddr` pointing to a DFP-enabled Envoy listener. Each backend specifies
its hostname, path, and auth:

```yaml
mcpConfig:
  backendListenerAddr: "http://127.0.0.1:10089"   # DFP-enabled listener
  routes:
    - name: "production"
      backends:
        - name: "github-copilot"
          host: "api.githubcopilot.com"
          backendPath: "/mcp/readonly"
          auth: "Bearer ghp_xxxxxxxxxxxx"
          toolSelector:
            include:
              - "search_code"
              - "list_repos"
        - name: "internal-mcp"
          host: "mcp.internal.corp.com"
          backendPath: "/mcp"
          auth: "Bearer internal-token"
      authorization:
        defaultAction: "Allow"
      forwardHeaders:
        - "X-Tenant-Id"
```

**What happens at request time:**

1. MCP proxy creates `POST http://127.0.0.1:10089` (DFP listener)
2. Sets `Host: api.githubcopilot.com` → Envoy DFP resolves DNS + handles TLS
3. Sets `URL.Path = /mcp/readonly`
4. Sets `Authorization: Bearer ghp_xxxxxxxxxxxx`
5. Envoy forwards to `api.githubcopilot.com:443` over TLS

### Example 2: Mixed static + dynamic backends

Some backends are pre-configured in Envoy (static clusters), others use DFP:

```yaml
mcpConfig:
  backendListenerAddr: "http://127.0.0.1:10088"   # standard backend listener
  routes:
    - name: "mixed-route"
      backends:
        - name: "static-backend"
          # No host/path/auth → routed via standard Envoy header-based routing
        - name: "dynamic-backend"
          host: "remote-mcp.example.com"
          backendPath: "/v1/mcp"
          auth: "Bearer dynamic-token"
```

### Example 3: Fully dynamic config via header

No static config needed for the route. Everything is passed at request time:

```bash
# Build the route config JSON
ROUTE_CONFIG='{
  "name": "dynamic-route",
  "backends": [
    {
      "name": "copilot",
      "host": "api.githubcopilot.com",
      "backendPath": "/mcp/readonly",
      "auth": "Bearer ghp_xxxxxxxxxxxx",
      "toolSelector": {
        "include": ["search_code"]
      }
    }
  ]
}'

# Base64-encode and send
CONFIG=$(echo -n "$ROUTE_CONFIG" | base64)

curl -X POST http://localhost:8080/mcp \
  -H "x-ai-eg-mcp-route: dynamic-route" \
  -H "x-ai-eg-mcp-dynamic-route-config: $CONFIG" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2025-06-18","capabilities":{}}}'
```

### Example 4: Envoy DFP listener configuration

For the DFP-based approach, configure Envoy with a Dynamic Forward Proxy listener:

```yaml
static_resources:
  listeners:
    - name: dfp_listener
      address:
        socket_address:
          address: 127.0.0.1
          port_value: 10089
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: dfp
                route_config:
                  virtual_hosts:
                    - name: dfp
                      domains: ["*"]
                      routes:
                        - match: { prefix: "/" }
                          route:
                            cluster: dynamic_forward_proxy_cluster
                http_filters:
                  - name: envoy.filters.http.dynamic_forward_proxy
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig
                      dns_cache_config:
                        name: dynamic_forward_proxy_dns_cache
                        dns_lookup_family: V4_ONLY
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

  clusters:
    - name: dynamic_forward_proxy_cluster
      lb_policy: CLUSTER_PROVIDED
      cluster_type:
        name: envoy.clusters.dynamic_forward_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig
          dns_cache_config:
            name: dynamic_forward_proxy_dns_cache
            dns_lookup_family: V4_ONLY
      transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
          common_tls_context:
            validation_context:
              trusted_ca:
                filename: /etc/ssl/certs/ca-certificates.crt
```

Then set `backendListenerAddr: "http://127.0.0.1:10089"` in your `filter-config.yaml`.

---

## Request Flow Diagram

```
Client Request
    │
    ▼
┌─────────────────────────┐
│   Envoy ext_proc filter │
│   (adds route/backend   │
│    headers)             │
└──────────┬──────────────┘
           │
           ▼
┌─────────────────────────┐
│      MCP Proxy          │
│                         │
│  1. routeConfig()       │  ◄── checks x-ai-eg-mcp-dynamic-route-config header
│     (dynamic or static) │      falls back to filter-config.yaml
│                         │
│  2. applyBackendOverrides│  ◄── sets Host, Path, Auth from MCPBackend
│     (host/path/auth)    │
│                         │
│  3. POST to             │
│     backendListenerAddr │
└──────────┬──────────────┘
           │
           ▼
┌─────────────────────────┐
│  Envoy Backend Listener │
│  (DFP or static routing)│
│                         │
│  - Reads Host header    │
│  - Resolves DNS         │
│  - Handles TLS          │
└──────────┬──────────────┘
           │
           ▼
   Backend MCP Server
```

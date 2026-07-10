# Arbitrary Per-Request LLM Backend Routing

This document analyzes how to achieve **truly arbitrary per-request routing decisions**
for LLM requests — i.e., deciding at request time exactly which backends to try, in what
order, with full control over fallback chains.

## Why Approaches A & B From `LLM_DYNAMIC_BACKEND_SELECTION.md` Are Insufficient

Both approaches select from a **pre-defined menu of route rules**. The route table is
static (set at xDS push time). At request time, you can only pick which rule matches via
headers — you cannot:

- Compose an arbitrary fallback chain (e.g., "try A, then C, skip B")
- Change weights or priorities per-request
- Add/remove backends from a rule at runtime

For N backends, covering all possible orderings would require O(2^N × N!) rules.

---

## The Core Problem

The LLM ext_proc is a **gRPC stream processor**. It does not make HTTP calls — it tells
Envoy "mutate these headers/body" and Envoy does the routing. The routing decision
(which cluster, which endpoint) is entirely Envoy's based on the xDS config.

```
┌────────────────────────────────────────────────────────────┐
│ Today's flow:                                              │
│                                                            │
│  Client → Envoy route table → Backend cluster → Upstream   │
│              ↑                     ↑                       │
│         (static xDS)         (static xDS)                  │
│                                                            │
│  Ext_proc only mutates headers/body, cannot choose backend │
└────────────────────────────────────────────────────────────┘
```

---

## How the MCP Proxy Solves This

The MCP proxy achieves arbitrary routing because it **makes HTTP calls directly**:

```go
// MCP proxy controls routing in Go code:
req, _ := http.NewRequest("POST", m.backendListenerAddr, body)
req.Host = backend.Host          // ← decides WHERE to send
req.URL.Path = backend.BackendPath
req.Header.Set("Authorization", backend.Auth)
client.Do(req)                   // ← sends via DFP listener
```

It sends all requests to a DFP-enabled Envoy listener, sets the `Host` header to the
desired backend, and Envoy's Dynamic Forward Proxy resolves DNS + TLS dynamically.

---

## Approach C: Ext_proc-Controlled Routing via DFP + Routing Plan Header

### Idea

Bring the MCP proxy's DFP approach into the LLM ext_proc. The ext_proc uses
`SetBackend` + header mutations to control which backend each attempt goes to,
using a **routing plan** provided by your custom ext_proc.

### How It Would Work

```
Client Request
    │
    ▼
┌──────────────────────────────────────────────────────────┐
│  Your Custom ext_proc                                     │
│  Sets header: x-ai-eg-routing-plan                       │
│  Value: base64 JSON:                                      │
│  {                                                        │
│    "backends": ["openai-backend", "anthropic-backend"],   │
│    "fallbackEnabled": true                                │
│  }                                                        │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (router filter)                      │
│  1. Parses body, extracts model                          │
│  2. Reads x-ai-eg-routing-plan header                    │
│  3. Stores routing plan in routerProcessor state         │
│  4. ClearRouteCache: true                                │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│  Envoy routes to DFP cluster (single cluster for all     │
│  LLM backends)                                            │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 1       │
│  1. upstreamFilterCount = 1 (first attempt)              │
│  2. Picks backends[0] = "openai-backend" from plan       │
│  3. Looks up RuntimeBackend config for "openai-backend"  │
│  4. Translates body: OpenAI schema                       │
│  5. Applies auth: OpenAI API key                         │
│  6. Sets :authority = api.openai.com (for DFP)           │
│  7. Sets :path = /v1/chat/completions                    │
│  8. Returns header + body mutations                      │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
        DFP resolves api.openai.com → sends request
               │
               ▼ (if OpenAI returns 5xx / 429)
┌──────────────────────────────────────────────────────────┐
│  Envoy retry policy triggers                              │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 2       │
│  1. upstreamFilterCount = 2 (retry)                      │
│  2. Picks backends[1] = "anthropic-backend" from plan    │
│  3. Looks up RuntimeBackend config for "anthropic-backend"│
│  4. Re-translates body: Anthropic schema (from original) │
│  5. Applies auth: Anthropic API key                      │
│  6. Sets :authority = api.anthropic.com (for DFP)        │
│  7. Sets :path = /v1/messages                            │
│  8. Returns header + body mutations                      │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
        DFP resolves api.anthropic.com → sends request
               │
               ▼
          Response back to client
```

### Key Mechanism: How the Ext_proc Controls the Backend on Retry

Today, `setBackend` resolves the backend name from **Envoy xDS metadata** (attributes):

```go
// server.go — current implementation
func (s *Server) setBackend(ctx, p, internalReqID, isEndpointPicker, req) error {
    attributes := req.GetAttributes()["envoy.filters.http.ext_proc"]
    backendName, _ := resolveBackendName(isEndpointPicker, attributes)  // ← Envoy decides
    backend := s.config.Backends[backendName]
    p.SetBackend(ctx, backend, routeName, routerProcessor)
}
```

With Approach C, `setBackend` would **also check the routing plan**:

```go
// server.go — proposed change (pseudocode)
func (s *Server) setBackend(ctx, p, internalReqID, isEndpointPicker, req) error {
    routerProcessor := s.routerProcessorsPerReqID[internalReqID]

    // Check if this request has a routing plan
    if plan := routerProcessor.GetRoutingPlan(); plan != nil {
        attemptIndex := routerProcessor.upstreamFilterCount  // 0-indexed
        if attemptIndex < len(plan.Backends) {
            backendName = plan.Backends[attemptIndex]
        } else {
            // Exhausted all backends in the plan
            return status.Error(codes.Unavailable, "all backends exhausted")
        }
    } else {
        // No routing plan — fall back to current behavior (Envoy decides)
        backendName, _ = resolveBackendName(isEndpointPicker, attributes)
    }

    backend := s.config.Backends[backendName]
    p.SetBackend(ctx, backend, routeName, routerProcessor)
}
```

### What Already Works (No Changes Needed)

| Component | Status | Why |
|---|---|---|
| Body re-translation on retry | ✅ Works | `upstreamProcessor.ProcessRequestHeaders` re-translates from `originalRequestBody` when `onRetry()` is true |
| Auth re-application on retry | ✅ Works | `u.handler.Do()` runs on every upstream attempt |
| Header mutation on retry | ✅ Works | `headerMutator.Mutate()` restores original + applies new headers |
| Body mutation on retry | ✅ Works | `bodyMutator` operates on `originalRequestBodyRaw` |
| Schema translator selection | ✅ Works | `SetBackend` calls `eh.GetTranslator(backend.Schema, ...)` per attempt |
| Upstream filter re-invocation | ✅ Works | Envoy calls the upstream ext_proc for each retry attempt |

### What Needs to Change

#### 1. Envoy Config: DFP Cluster for LLM Backends

All LLM backends route through a single DFP cluster instead of individual clusters:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: llm-dfp-cluster
spec:
  endpoints:
    # DFP resolves dynamically based on :authority header
    - fqdn:
        hostname: "dynamic"   # placeholder, DFP overrides this
        port: 443
```

Or configure the DFP cluster directly:

```yaml
# Envoy Bootstrap or EnvoyPatchPolicy
name: llm-dfp-cluster
type: STRICT_DNS
lb_policy: CLUSTER_PROVIDED
cluster_type:
  name: envoy.clusters.dynamic_forward_proxy
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig
    dns_cache_config:
      name: llm_dns_cache
      dns_lookup_family: V4_ONLY
transport_socket:
  name: envoy.transport_sockets.tls
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
    sni: "%REQ(:authority)%"
```

#### 2. Routing Plan Header Format

```
x-ai-eg-routing-plan: <base64-encoded JSON>
```

```json
{
  "backends": ["openai-backend", "anthropic-backend", "cohere-backend"],
  "fallbackEnabled": true
}
```

- `backends`: Ordered list of backend names (matching `filterapi.Backend.Name`)
- `backends[0]` is tried first, `backends[1]` on first retry, etc.
- `fallbackEnabled`: If false, only `backends[0]` is tried (no retries)

#### 3. AI Gateway Code Changes

##### a. Routing plan struct (`internal/filterapi/` or `internal/internalapi/`)

```go
// RoutingPlan represents a per-request routing plan provided by a custom ext_proc.
type RoutingPlan struct {
    // Backends is the ordered list of backend names to try.
    // backends[0] is the primary, backends[1] is the first fallback, etc.
    Backends []string `json:"backends"`
    // FallbackEnabled controls whether retries to subsequent backends are allowed.
    FallbackEnabled *bool `json:"fallbackEnabled,omitempty"`
}
```

##### b. Header constant (`internal/internalapi/internalapi.go`)

```go
LLMRoutingPlanHeader = EnvoyAIGatewayHeaderPrefix + "routing-plan"
```

##### c. Store routing plan in routerProcessor (`internal/extproc/processor_impl.go`)

In `routerProcessor`:
```go
type routerProcessor[...] struct {
    // ... existing fields ...
    routingPlan *RoutingPlan  // parsed from x-ai-eg-routing-plan header
}
```

Parse in `ProcessRequestBody` (where headers are available):
```go
if planHeader := r.requestHeaders[internalapi.LLMRoutingPlanHeader]; planHeader != "" {
    decoded, _ := base64.StdEncoding.DecodeString(planHeader)
    var plan RoutingPlan
    json.Unmarshal(decoded, &plan)
    r.routingPlan = &plan
}
```

##### d. Override backend selection in `setBackend` (`internal/extproc/server.go`)

When a routing plan is present, use it instead of xDS attributes to select the backend:

```go
func (s *Server) setBackend(ctx, p, internalReqID, isEndpointPicker, req) error {
    routerProcessor := s.routerProcessorsPerReqID[internalReqID]

    var backendName string
    if plan := getRoutingPlan(routerProcessor); plan != nil {
        attemptIndex := getUpstreamFilterCount(routerProcessor) - 1
        if attemptIndex >= len(plan.Backends) {
            return status.Error(codes.Unavailable, "routing plan exhausted")
        }
        backendName = plan.Backends[attemptIndex]
    } else {
        // Original behavior
        attributes := req.GetAttributes()["envoy.filters.http.ext_proc"]
        backendName, _ = resolveBackendName(isEndpointPicker, attributes)
    }

    backend := s.config.Backends[backendName]
    if backend == nil {
        return status.Errorf(codes.Internal, "unknown backend in routing plan: %s", backendName)
    }
    p.SetBackend(ctx, backend, routeName, routerProcessor)
}
```

##### e. Set `:authority` for DFP (`internal/extproc/processor_impl.go`)

In `upstreamProcessor.ProcessRequestHeaders`, when a routing plan is active, emit an
`:authority` mutation so DFP routes to the correct host:

```go
// In ProcessRequestHeaders, after translator and auth:
if u.parent.routingPlan != nil {
    // Backend.Host must be populated in filterapi.Backend for DFP routing
    host := backend.Host  // e.g., "api.openai.com"
    headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
        Header: &corev3.HeaderValue{Key: ":authority", RawValue: []byte(host)},
    })
}
```

This requires adding a `Host` field to `filterapi.Backend` (similar to `MCPBackend.Host`).

##### f. Add `Host` and `BackendPath` to `filterapi.Backend`

```go
type Backend struct {
    Name              string                        `json:"name"`
    // Host is the upstream hostname used for DFP routing (e.g., "api.openai.com").
    // Only needed when using routing plans with DFP.
    Host              string                        `json:"host,omitempty"`
    // BackendPath is the base path for this backend's API (e.g., "/v1/chat/completions").
    // Only needed when using routing plans with DFP.
    BackendPath       string                        `json:"backendPath,omitempty"`
    // ... existing fields ...
}
```

#### 4. Retry Policy

Attach a `BackendTrafficPolicy` with enough retries to cover the longest fallback chain:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: llm-retry
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: llm-route
  retry:
    numRetries: 4       # Max backends in any routing plan minus 1
    retryOn:
      httpStatusCodes: [429, 500, 502, 503]
```

When `fallbackEnabled: false` in the routing plan, the ext_proc could return an
`ImmediateResponse` with the error instead of letting Envoy retry. Or, it could
set all remaining plan entries to the same backend (effectively no fallback).

---

### Complete Request Flow

```
1. Client sends:  POST /v1/chat/completions
                  x-ai-eg-routing-plan: eyJiYWNrZW5kcyI6WyJvcGVuYWktYmFja2VuZCIsImFudGhyb3BpYy1iYWNrZW5kIl19

2. Your ext_proc: (already set the header above — passes through)

3. AI Gateway router ext_proc:
   - Parses body → extracts model "gpt-4"
   - Decodes routing plan → ["openai-backend", "anthropic-backend"]
   - Stores plan + original body in routerProcessor
   - Sets x-ai-eg-model header
   - ClearRouteCache: true → Envoy routes to DFP cluster

4. AI Gateway upstream ext_proc (attempt 1):
   - setBackend reads plan → backends[0] = "openai-backend"
   - Loads RuntimeBackend for "openai-backend" (schema=OpenAI, auth=APIKey)
   - Translates body to OpenAI format
   - Auth: sets Authorization: Bearer sk-...
   - Sets :authority = api.openai.com
   - DFP connects to api.openai.com:443

5. OpenAI returns 503 → Envoy retry triggers

6. AI Gateway upstream ext_proc (attempt 2):
   - setBackend reads plan → backends[1] = "anthropic-backend"
   - Loads RuntimeBackend for "anthropic-backend" (schema=Anthropic, auth=AnthropicKey)
   - Re-translates original body to Anthropic format
   - Auth: sets x-api-key: sk-ant-...
   - Sets :authority = api.anthropic.com
   - DFP connects to api.anthropic.com:443

7. Anthropic returns 200 → response flows back to client
```

---

### Comparison with MCP Proxy Approach

| Aspect | MCP Proxy | LLM Ext_proc (Approach C) |
|---|---|---|
| **Who makes HTTP calls** | Go code (`http.Client`) | Envoy (via DFP) |
| **Who decides backend** | Go code (iterates backends) | Ext_proc (via routing plan) + Envoy retry |
| **Fallback mechanism** | Go loop over backends | Envoy retry policy triggers next attempt |
| **Body translation** | N/A (JSON-RPC passthrough) | Full schema translation (OpenAI↔Anthropic etc.) |
| **Auth injection** | `req.Header.Set("Authorization", ...)` | `BackendAuthHandler.Do()` |
| **DFP routing** | `req.Host = backend.Host` | `:authority` header mutation |
| **Config source** | `MCPBackend.Host/BackendPath/Auth` | `Backend.Host/BackendPath` + existing `Backend.Auth` |

---

### Risks and Considerations

1. **DFP cluster setup complexity** — Requires Envoy configuration for DFP, TLS SNI, and
   DNS caching. This is the same pattern used by the MCP proxy but applied to LLM traffic.

2. **Retry count must cover max plan length** — If the retry policy allows only 2 retries
   but the plan has 5 backends, only the first 3 will be tried. The ext_proc cannot
   force Envoy to retry.

3. **Mixed mode** — Requests without a routing plan should fall back to normal Envoy
   routing (current behavior). The routing plan is opt-in.

4. **Backend validation** — The ext_proc must validate that all backend names in the
   routing plan exist in `s.config.Backends`. Invalid names should return an error.

5. **Security** — The routing plan header should be validated/sanitized. Malicious plans
   could reference backends the caller shouldn't access. Consider authorization checks.

6. **Observability** — Log which backend was selected on each attempt, and whether it
   was from a routing plan or from Envoy's default routing.

---

### Implementation Effort Estimate

| Change | Scope | Risk |
|---|---|---|
| Add `Host`, `BackendPath` to `filterapi.Backend` | Small (2 fields) | Low |
| Add `LLMRoutingPlanHeader` constant | Trivial | None |
| Add `RoutingPlan` struct | Small | Low |
| Parse routing plan in `routerProcessor.ProcessRequestBody` | Small (~15 lines) | Low |
| Override backend in `server.setBackend` | Medium (~20 lines) | Medium — must handle edge cases |
| Set `:authority` in `upstreamProcessor.ProcessRequestHeaders` | Small (~10 lines) | Low |
| DFP cluster Envoy config | Medium (ops/infra) | Medium — TLS, DNS, timeouts |
| Controller changes to populate `Host`/`BackendPath` | Medium | Medium — needs AIServiceBackend mapping |
| Tests | Medium (~100-200 lines) | Low |

**Total: ~250-300 lines of Go code + Envoy config changes.**

---

### Alternative: Approach D — Ext_proc as HTTP Client (MCP-Style)

If the DFP complexity is too high, another option is to make the ext_proc behave more
like the MCP proxy — have it make HTTP calls directly instead of letting Envoy route.

This would mean:
- The router ext_proc, upon receiving a routing plan, makes HTTP calls to each backend
  in sequence (with translation + auth)
- Returns an `ImmediateResponse` with the successful backend's response
- Completely bypasses Envoy's upstream routing

**Pros:** Full control, simpler than DFP setup.
**Cons:** Loses Envoy's connection pooling, circuit breaking, observability, TLS management.
         Essentially re-implements what Envoy does, but worse. Not recommended unless DFP
         is not viable.

---

### Recommendation

**Approach C (DFP + Routing Plan)** is the right path for arbitrary per-request routing.
It reuses the proven MCP proxy pattern, leverages Envoy's retry mechanism for fallback,
and keeps the ext_proc's role focused on translation + auth + backend selection — while
Envoy handles the actual HTTP transport.

Start with:
1. Prototype the DFP cluster config for one LLM backend
2. Add routing plan parsing to the ext_proc
3. Override `setBackend` to use the plan
4. Set `:authority` for DFP routing
5. Test with a 2-backend fallback chain (OpenAI → Anthropic)

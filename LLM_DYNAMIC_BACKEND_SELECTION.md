# Dynamic LLM Backend Selection & Fallback Control

This document describes two approaches for dynamically enabling/disabling LLM providers
and controlling fallback behavior per-request using a custom ext_proc that runs before the
AI gateway's ext_proc.

## Context

- All backends are **statically configured** in Envoy (known providers like OpenAI, Anthropic, etc.)
- A custom ext_proc runs **before** the AI gateway ext_proc and sets headers to control routing
- The AI gateway ext_proc already calls `ClearRouteCache: true` after body parsing, so any
  header set by the upstream ext_proc will be used by Envoy for route re-evaluation

---

## Approach A: Header-Based Route Rules (No Code Changes)

Use `AIGatewayRouteRule.Matches` with header matching to route to different backend
combinations. Your custom ext_proc sets a header (e.g., `x-provider-mode`) and Envoy
matches the appropriate rule.

### Architecture

```
Client Request
    │
    ▼
┌─────────────────────────────┐
│  Custom ext_proc (yours)    │
│  Sets: x-provider-mode      │
│  e.g. "openai-with-fallback"│
└──────────┬──────────────────┘
           │
           ▼
┌─────────────────────────────┐
│  AI Gateway ext_proc        │
│  - Parses body, extracts    │
│    model name                │
│  - Sets x-ai-eg-model       │
│  - ClearRouteCache: true     │
└──────────┬──────────────────┘
           │
           ▼
┌─────────────────────────────┐
│  Envoy re-evaluates route   │
│  Matches on:                │
│  - x-provider-mode header   │
│  - x-ai-eg-model header     │
│  Selects backendRefs with   │
│  weight + priority           │
└──────────┬──────────────────┘
           │
           ▼
   Selected Backend (OpenAI / Anthropic / etc.)
```

### Example: AIGatewayRoute Configuration

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: llm-route
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  rules:
    # ──────────────────────────────────────────────
    # Rule 1: OpenAI only — no fallback
    # Your ext_proc sets: x-provider-mode: openai-only
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-provider-mode
              value: openai-only
      backendRefs:
        - name: openai-backend
          weight: 1

    # ──────────────────────────────────────────────
    # Rule 2: Anthropic only — no fallback
    # Your ext_proc sets: x-provider-mode: anthropic-only
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-provider-mode
              value: anthropic-only
      backendRefs:
        - name: anthropic-backend
          weight: 1

    # ──────────────────────────────────────────────
    # Rule 3: OpenAI primary + Anthropic fallback
    # Your ext_proc sets: x-provider-mode: openai-with-fallback
    # Priority 0 = primary, Priority 1 = fallback
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-provider-mode
              value: openai-with-fallback
      backendRefs:
        - name: openai-backend
          weight: 1
          priority: 0
        - name: anthropic-backend
          weight: 1
          priority: 1

    # ──────────────────────────────────────────────
    # Rule 4: Anthropic primary + OpenAI fallback
    # Your ext_proc sets: x-provider-mode: anthropic-with-fallback
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-provider-mode
              value: anthropic-with-fallback
      backendRefs:
        - name: anthropic-backend
          weight: 1
          priority: 0
        - name: openai-backend
          weight: 1
          priority: 1

    # ──────────────────────────────────────────────
    # Rule 5: All providers, equal weight, no fallback
    # Your ext_proc sets: x-provider-mode: load-balance
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-provider-mode
              value: load-balance
      backendRefs:
        - name: openai-backend
          weight: 50
        - name: anthropic-backend
          weight: 50

    # ──────────────────────────────────────────────
    # Default rule: catch-all (e.g., OpenAI)
    # When x-provider-mode is not set
    # ──────────────────────────────────────────────
    - matches:
        - headers:
            - name: x-ai-eg-model
              value: gpt-4
      backendRefs:
        - name: openai-backend
          weight: 1
```

### Enabling Retry/Fallback via BackendTrafficPolicy

For fallback to actually work (retry on failure), attach a `BackendTrafficPolicy` to the
generated `HTTPRoute`:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: llm-retry-policy
  namespace: default
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: llm-route    # same name as AIGatewayRoute
  retry:
    numRetries: 2
    retryOn:
      httpStatusCodes:
        - 429   # rate limited
        - 500   # internal server error
        - 502   # bad gateway
        - 503   # service unavailable
```

### Disabling Fallback for Specific Modes

For rules without `priority` (e.g., `openai-only`), there's only one backend — no fallback
will occur even with the retry policy attached, because there are no lower-priority endpoints
to fail over to.

For rules with `priority`, Envoy will use the retry policy to attempt fallback to the
lower-priority backend on failure.

### Your Custom ext_proc Logic (Pseudocode)

```python
def process_request_headers(headers):
    tenant = headers.get("x-tenant-id")
    model = headers.get("x-ai-eg-model", "")

    # Your business logic to determine provider mode
    config = get_tenant_config(tenant)

    if config.openai_enabled and config.anthropic_enabled and config.fallback_enabled:
        mode = "openai-with-fallback"
    elif config.openai_enabled and config.anthropic_enabled:
        mode = "load-balance"
    elif config.openai_enabled:
        mode = "openai-only"
    elif config.anthropic_enabled:
        mode = "anthropic-only"
    else:
        mode = "openai-only"  # default

    # Set the header — AI gateway ext_proc will ClearRouteCache after body parsing
    return set_header("x-provider-mode", mode)
```

### Pros & Cons

| Pros | Cons |
|---|---|
| Zero code changes to AI gateway | Combinatorial explosion with many providers |
| Uses existing Envoy routing primitives | Retry/fallback policy is per-HTTPRoute, not per-rule |
| Easy to understand and debug | Adding a new provider requires adding new rules |
| Your ext_proc stays simple (just sets a header) | Cannot dynamically change weights per-request |

---

## Approach B: Dynamic Backend Config via Header (Requires AI Gateway Changes)

The AI gateway ext_proc reads a header containing per-request backend preferences and
emits header mutations or dynamic metadata to influence routing.

### Architecture

```
Client Request
    │
    ▼
┌──────────────────────────────────┐
│  Custom ext_proc (yours)         │
│  Sets: x-ai-eg-backend-config   │
│  Value: base64-encoded JSON      │
│  e.g. {                          │
│    "backends": ["openai"],       │
│    "fallback": false             │
│  }                               │
└──────────┬───────────────────────┘
           │
           ▼
┌──────────────────────────────────┐
│  AI Gateway ext_proc             │
│  1. Parses body (model, stream)  │
│  2. Reads x-ai-eg-backend-config │
│  3. Emits header mutations:      │
│     - Sets backend weights to 0  │
│       for disabled backends      │
│     - Sets retry metadata for    │
│       fallback control           │
│  4. ClearRouteCache: true        │
└──────────┬───────────────────────┘
           │
           ▼
┌──────────────────────────────────┐
│  Envoy re-evaluates route        │
│  Uses mutated headers/metadata   │
│  to select active backends       │
└──────────┬───────────────────────┘
           │
           ▼
   Selected Backend
```

### Header Format

```
x-ai-eg-backend-config: <base64-encoded JSON>
```

JSON payload:

```json
{
  "activeBackends": ["openai-backend", "anthropic-backend"],
  "disabledBackends": ["cohere-backend"],
  "fallbackEnabled": true,
  "primaryBackend": "openai-backend"
}
```

### How It Would Work

#### Option B1: Weight Override via Dynamic Metadata

The AI gateway ext_proc reads the config header in `ProcessRequestBody` and emits dynamic
metadata that a Lua filter or custom route configuration uses to set weights to 0 for
disabled backends:

```go
// In routerProcessor.ProcessRequestBody:
if configHeader := r.requestHeaders["x-ai-eg-backend-config"]; configHeader != "" {
    config := parseBackendConfig(configHeader)
    for _, disabled := range config.DisabledBackends {
        // Emit metadata to signal this backend should be skipped
        dynamicMetadata["ai_gateway.disabled_backend."+disabled] = "true"
    }
}
```

A Lua filter or Envoy route config would then use this metadata to adjust routing.

#### Option B2: Route Header Injection

Simpler: the AI gateway ext_proc translates the config into a `x-provider-mode` header
(similar to Approach A) but does the logic internally:

```go
// In routerProcessor.ProcessRequestBody:
if configHeader := r.requestHeaders["x-ai-eg-backend-config"]; configHeader != "" {
    config := parseBackendConfig(configHeader)
    mode := resolveProviderMode(config)
    // Inject the mode header for Envoy route matching
    headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
        Header: &corev3.HeaderValue{Key: "x-provider-mode", RawValue: []byte(mode)},
    })
}
```

This is essentially Approach A but with the mode resolution happening inside the AI gateway
instead of in your custom ext_proc.

#### Option B3: Per-Request Retry Override via Dynamic Metadata

To control fallback per-request, the ext_proc could emit dynamic metadata that overrides
the retry policy:

```go
if !config.FallbackEnabled {
    // Emit metadata to disable retries for this request
    dynamicMetadata["envoy.retry_policy.num_retries"] = 0
}
```

However, **Envoy does not currently support per-request retry override via dynamic metadata**.
This would require a custom Envoy filter or an Envoy feature request.

### Implementation Plan (If Pursuing B2)

1. **Define header constant** in `internal/internalapi/internalapi.go`:
   ```go
   LLMBackendConfigHeader = EnvoyAIGatewayHeaderPrefix + "llm-backend-config"
   ```

2. **Define config struct** in `internal/filterapi/filterconfig.go`:
   ```go
   type LLMBackendConfig struct {
       ActiveBackends   []string `json:"activeBackends,omitempty"`
       DisabledBackends []string `json:"disabledBackends,omitempty"`
       FallbackEnabled  *bool    `json:"fallbackEnabled,omitempty"`
       PrimaryBackend   string   `json:"primaryBackend,omitempty"`
   }
   ```

3. **Parse and apply in `ProcessRequestBody`** in `internal/extproc/processor_impl.go`:
   - Read the `x-ai-eg-llm-backend-config` header
   - Base64-decode and parse as `LLMBackendConfig`
   - Translate into a `x-provider-mode` header value
   - Add to `headerMutation.SetHeaders`

4. **Define route rules** in `AIGatewayRoute` that match on the generated `x-provider-mode`

5. **Tests**: Unit tests for config parsing and mode resolution

### Required AIGatewayRoute Config (Same as Approach A)

The `AIGatewayRoute` rules would be identical to Approach A — the difference is that the
mode resolution happens inside the AI gateway ext_proc instead of in your custom ext_proc.

### Pros & Cons

| Pros | Cons |
|---|---|
| Richer API — structured JSON instead of enum header | Requires AI gateway code changes |
| Can evolve to support weights, priorities dynamically | Still limited by Envoy's routing model |
| Single header carries all config | More complex debugging (logic split across ext_procs) |
| Your ext_proc doesn't need to know about route rules | Per-request retry override not natively supported by Envoy |

---

## Comparison Summary

| Feature | Approach A | Approach B |
|---|---|---|
| **Code changes** | None | AI gateway ext_proc changes |
| **Complexity** | Config-only | Code + config |
| **Flexibility** | Finite set of modes | Structured JSON, extensible |
| **Per-request fallback control** | Via separate rules (no priority = no fallback) | Same, plus potential metadata-based override |
| **Scalability (many providers)** | O(2^n) rules for n providers | O(1) header, but still needs matching rules |
| **Debugging** | Simple — header → rule → backend | Header → ext_proc logic → rule → backend |
| **Envoy compatibility** | 100% native | Mostly native, retry override needs custom work |

## Recommendation

**Start with Approach A.** It requires no code changes and leverages Envoy's native routing.
For most use cases (2-4 providers with enable/disable + fallback toggle), the number of rules
is manageable.

If the number of providers grows large or you need features like per-request weight
adjustment, consider Approach B2 as an incremental addition — it builds on top of Approach A's
route rules and just moves the mode-resolution logic into the AI gateway.

---

## Quick Start: Approach A with 2 Providers

### Step 1: Define AIServiceBackends

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIServiceBackend
metadata:
  name: openai-backend
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    group: gateway.envoyproxy.io
    kind: Backend
    name: openai-upstream
---
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIServiceBackend
metadata:
  name: anthropic-backend
  namespace: default
spec:
  schema:
    name: Anthropic
  backendRef:
    group: gateway.envoyproxy.io
    kind: Backend
    name: anthropic-upstream
```

### Step 2: Define AIGatewayRoute with Mode-Based Rules

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: llm-route
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  rules:
    # OpenAI only
    - matches:
        - headers:
            - name: x-provider-mode
              value: openai-only
      backendRefs:
        - name: openai-backend

    # Anthropic only
    - matches:
        - headers:
            - name: x-provider-mode
              value: anthropic-only
      backendRefs:
        - name: anthropic-backend

    # OpenAI primary, Anthropic fallback
    - matches:
        - headers:
            - name: x-provider-mode
              value: openai-primary
      backendRefs:
        - name: openai-backend
          priority: 0
        - name: anthropic-backend
          priority: 1

    # Anthropic primary, OpenAI fallback
    - matches:
        - headers:
            - name: x-provider-mode
              value: anthropic-primary
      backendRefs:
        - name: anthropic-backend
          priority: 0
        - name: openai-backend
          priority: 1
```

### Step 3: Attach Retry Policy

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: llm-retry
  namespace: default
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: llm-route
  retry:
    numRetries: 1
    retryOn:
      httpStatusCodes: [429, 500, 502, 503]
```

### Step 4: Your ext_proc Sets the Header

Your ext_proc inspects the request (tenant config, feature flags, etc.) and sets:

```
x-provider-mode: openai-primary
```

The AI gateway ext_proc processes the body, calls `ClearRouteCache`, and Envoy
re-evaluates against the rules above, selecting the matching backend configuration.

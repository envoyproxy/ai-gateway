# Router-Phase Request Mutation via a Generic External Service

## Table of Contents

<!-- toc -->

- [Summary](#summary)
- [Background](#background)
  - [How an AIGatewayRoute becomes an HTTPRoute](#how-an-aigatewayroute-becomes-an-httproute)
  - [The router filter and `x-ai-eg-model`](#the-router-filter-and-x-ai-eg-model)
- [Problem Statement](#problem-statement)
  - [The two-route problem](#the-two-route-problem)
  - [The landing-route problem (no headers on the first pass)](#the-landing-route-problem-no-headers-on-the-first-pass)
  - [Why injecting a second ext_proc via EnvoyPatchPolicy is problematic](#why-injecting-a-second-ext_proc-via-envoypatchpolicy-is-problematic)
- [Proposal](#proposal)
  - [Overview](#overview)
  - [Request flow](#request-flow)
  - [The mutation-service contract](#the-mutation-service-contract)
  - [Header-based gating and configuration](#header-based-gating-and-configuration)
  - [Behavior in the router processor](#behavior-in-the-router-processor)
  - [Failure handling](#failure-handling)
- [Why this avoids both problems](#why-this-avoids-both-problems)
- [Alternatives Considered](#alternatives-considered)
- [Open Questions](#open-questions)

<!-- /toc -->

## Summary

Today, inserting a request-rewriting step (for example, a semantic-router that selects
the model from the prompt) *before* the AI Gateway makes its routing decision requires
wiring a **second `ext_proc` filter** ahead of the AI Gateway filter. On a single Gateway
this can only be done with `EnvoyPatchPolicy` (or composite `ExtensionWithMatcher`)
patches, which are brittle to maintain and risky to the whole listener.

This proposal makes that step a capability of the **existing router-phase AI Gateway
filter**: during request-body processing, and **gated purely by a request header**, the
filter calls a **generic external service**, forwarding the request **to the same endpoint
URL and in the same schema it arrived in**. The service mutates the body (e.g., model
selection, message/param rewriting) and returns it in that same schema. The gateway then
re-derives `x-ai-eg-model` from the returned body and lets native routing proceed —
reusing the existing route-cache-clear mechanism. The gateway does **not** impose any
particular request schema (e.g., chat-completions); whatever the client sent is what the
service receives and returns.

There is **no new CRD field** and no change to how routes are generated. The mutation
service endpoint is part of the filter's static configuration; whether the call fires for
a given request is decided entirely by the presence of a **gating header** — which can be
set per route, listener, or tenant using header-manipulation primitives that already exist
in the data path. No second filter, no filter ordering, and no `EnvoyPatchPolicy` are
needed.

## Background

### How an AIGatewayRoute becomes an HTTPRoute

The controller renders each `AIGatewayRoute` into a single `HTTPRoute` whose rules are:

- **One rule per `AIGatewayRoute` rule** — a path-prefix match (the configured root
  prefix, e.g. `/v1`) plus any header matches the user declared. Model-based routing is
  expressed as a header match on `x-ai-eg-model`.
- **One controller-injected catch-all rule** — named `route-not-found`, matching only the
  root path prefix with no header match. It is appended last and exists so that a request
  always matches *something* on the prefix even before the model is known.

```
                   HTTPRoute  (generated from a single AIGatewayRoute)
  ┌─────────┬────────────┬─────────────────────────────┬──────────────────┬─────────────┐
  │ rule    │ path match │ header match                │ destination      │ kind        │
  ├─────────┼────────────┼─────────────────────────────┼──────────────────┼─────────────┤
  │ rule[0] │ /v1        │ x-ai-eg-model == "gpt-4o"    │ backend A        │ header-keyed│
  │ rule[1] │ /v1        │ x-ai-eg-model == "llama"     │ backend B        │ header-keyed│
  │  ...    │  ...       │  ...                         │  ...             │  ...        │
  │ rule[N] │ /v1        │ (none)                       │ route-not-found  │ catch-all   │
  │         │            │                              │ filter           │ ◄ always    │
  │         │            │                              │                  │   matches   │
  └─────────┴────────────┴─────────────────────────────┴──────────────────┴─────────────┘
   Header-keyed rules are listed first; the catch-all is appended last.
```

See `internal/controller/ai_gateway_route.go` (`newHTTPRoute`): the per-rule loop appends
the header-keyed rules, then a final `route-not-found` rule matching only the prefix.

### The router filter and `x-ai-eg-model`

Clients send a normal OpenAI-style request (`{"model": "...", "messages": [...]}`); they
do **not** send `x-ai-eg-model`. That header is produced **server-side** by the AI Gateway
router-phase `ext_proc` filter, which:

1. buffers and parses the request body,
2. extracts the model name,
3. sets the `x-ai-eg-model` request header, and
4. returns `ClearRouteCache: true` so Envoy re-evaluates routing with the header present.

See `internal/extproc/processor_impl.go` (`routerProcessor.ProcessRequestBody`): it sets
`x-ai-eg-model` from the parsed model and returns a `CommonResponse` with
`ClearRouteCache: true`.

## Problem Statement

### The two-route problem

Every model-routed destination is reached through **two** routing artifacts: the
**header-keyed rule** (`x-ai-eg-model == <model>`) that is the real destination, and the
**catch-all rule** that every request matches first. The header-keyed rule is unreachable
on the first routing pass because the matching header does not yet exist.

This is benign for a single `AIGatewayRoute`, but it compounds across routes. When
multiple `AIGatewayRoute`s are attached to the **same Gateway**, each contributes its own
catch-all rule on the **same** root path prefix. These catch-alls are indistinguishable
(same prefix, no headers), so Gateway API conflict resolution keeps only one of them and
all prefix traffic is funneled through that single surviving catch-all — regardless of
which `AIGatewayRoute` "owns" the request.

```
                  Single Gateway · one listener · path prefix "/v1"
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │                                                                                │
  │  AIGatewayRoute-1 ──► HTTPRoute-1 :  [header rules…]  +  catch-all(/v1) ──┐     │
  │  AIGatewayRoute-2 ──► HTTPRoute-2 :  [header rules…]  +  catch-all(/v1) ──┤     │
  │  AIGatewayRoute-3 ──► HTTPRoute-3 :  [header rules…]  +  catch-all(/v1) ──┤     │
  │                                                                          │     │
  │                          all catch-alls are identical                    │     │
  │                          (same prefix, no header match)                  ▼     │
  │                          Gateway-API conflict resolution                       │
  │                          keeps exactly ONE (the oldest)                        │
  │                                          │                                     │
  │   every first-pass request on /v1        ▼                                     │
  │   (no x-ai-eg-model yet)  ──────────────►  one surviving catch-all rule        │
  │                                            (owns ALL prefix traffic)           │
  └──────────────────────────────────────────────────────────────────────────────┘
```

Consequence: you cannot reliably attach a *per-destination-route* behavior (such as a
per-route semantic-router filter) and expect a given request to traverse it on the first
pass, because on the first pass the request is on the shared catch-all, not on its eventual
destination route.

### The landing-route problem (no headers on the first pass)

The deeper, single-route version of the same issue: because `x-ai-eg-model` is only
populated **after** the body is parsed, the **only** rule a fresh request can match is a
**header-less** rule — i.e., the catch-all. Routing therefore always proceeds in two
phases:

```
  PHASE 1 — request has no x-ai-eg-model              PHASE 2 — after ClearRouteCache
  ════════════════════════════════════              ═══════════════════════════════

  client                                             Envoy re-evaluates the route
    │  POST /v1/chat/completions                     table, x-ai-eg-model now present
    │  {"model":"gpt-4o", ...}                                     │
    ▼                                                              ▼
  ┌──────────────────┐  matches   ┌─────────────────────┐  clear  ┌────────────────────────┐
  │ catch-all rule   │ ─────────► │ router ext_proc      │  route  │ header-keyed rule       │
  │ (no header match)│            │ • parse body         │ ──────► │ x-ai-eg-model == gpt-4o │
  │                  │            │ • set x-ai-eg-model  │  cache  │      → backend A        │
  └──────────────────┘            │ • ClearRouteCache    │         └────────────────────────┘
                                  └─────────────────────┘
  Anything that must run "before routing is final" runs HERE — while the request is
  still on the catch-all rule, not on its eventual destination rule.
```

Any processing you want to happen "before routing is finalized" actually executes while
the request is sitting on the **catch-all** route, not on the destination route. This is
exactly why a second, route-attached `ext_proc` cannot be slotted in front of the AI
Gateway filter on a single Gateway without restructuring the topology: at the only moment
such a filter could run, the request is not yet on the route it is attached to.

### Why injecting a second ext_proc via EnvoyPatchPolicy is problematic

The natural workaround is to add the request-rewriting service as its **own `ext_proc`
filter** placed *before* the AI Gateway filter, and to force that ordering with
`EnvoyPatchPolicy`. The generated resources are explicitly documented as an
"implementation detail subject to change" (see the `AIGatewayRoute` type doc), and
`EnvoyPatchPolicy` is a raw patch against exactly those generated artifacts. This raises
serious **maintenance** and **stability** concerns:

**Maintenance**

- **Index/name pinning.** The patch targets the HCM filter chain by position or by the
  generated filter name. The set, order, and naming of filters is produced by the AI
  Gateway and Envoy Gateway controllers; any version bump that changes them silently
  invalidates the patch.
- **No schema, no typed validation.** Patches are free-form JSON/JSON-Patch. They bypass
  CRD validation, so mistakes are not caught at admission — only at translation or
  runtime.
- **Per-route/per-model churn.** Each new route or model can require editing the patch
  (or a slot allocator to manage indices), so the patch surface grows with the
  configuration instead of staying constant.
- **Ordering must be hand-maintained.** The new filter must be kept strictly before the
  AI Gateway filter; this relationship is asserted by hand in the patch rather than
  expressed declaratively.

**Stability**

- **Blast radius is the whole listener.** A malformed or stale patch can break the entire
  HCM/listener — not just one route — taking down unrelated traffic on the same Gateway.
- **Hard to test and reason about.** Because patches operate post-translation, their
  effect can only be validated against a fully rendered config, making them fragile across
  upgrades and difficult to cover in CI.
- **Composability.** Patches interact poorly with `mergeGateways`/GatewayClass-scoped
  setups and with other policies that touch the same filter chain.

```
                       HCM filter chain on a single Gateway listener
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │                                                                                │
  │             must run BEFORE ──────────────┐                                    │
  │                                           │                                    │
  │           ┌────────────────────┐          ▼     ┌────────────────────┐  ┌──────┐
  │ client ──►│ ext_proc: REWRITER │ ───────────────│ ext_proc:          │─►│router│─► backend
  │           │ (the new filter)   │                │ AI Gateway         │  │      │
  │           └────────────────────┘                └────────────────────┘  └──────┘
  │                     ▲                                                            │
  │                     └── inserted AND ordered ONLY via EnvoyPatchPolicy           │
  │                         · pinned to filter index/name → breaks on upgrades       │
  │                         · free-form JSON → no schema validation                  │
  │                         · a bad patch can break the WHOLE listener               │
  └──────────────────────────────────────────────────────────────────────────────┘
```

## Proposal

### Overview

Instead of adding a second filter, let the **existing router-phase AI Gateway filter**
make a synchronous, third-party **HTTP call to a generic mutation service** during
request-body processing — *before* it derives `x-ai-eg-model` and clears the route cache.
The call is **gated by a request header**: it fires only when a configured gating header
is present, and is skipped otherwise, so the existing behavior is completely unchanged for
traffic that is not opted in.

The mutation service is intentionally **schema-agnostic**. The gateway forwards the
request to the service **on the same endpoint URL (the inbound request path) and in the
same schema/body it received from the client**. The service may change anything in the
body (most importantly the model, but also messages, parameters, injected fields, etc.)
and returns it in **that same schema**. It is a pure **"body in → body out"**
transformation and is unaware of Envoy, routes, or the gateway internals. The gateway does
not parse or impose a fixed request schema for this call. A general semantic-router is one
example of such a service, but any conforming service works.

### Request flow

```
  ┌────────┐        ┌────────────────────────────┐     ┌──────────┐     ┌─────────┐
  │ Client │        │ Router ext_proc (AI Gateway)│     │ Mutation │     │  Envoy  │
  │        │        │     ProcessRequestBody      │     │ Service  │     │ Router  │
  └───┬────┘        └──────────────┬──────────────┘     └────┬─────┘     └────┬────┘
      │  POST /v1/chat/completions │                         │                │
      │  x-ai-eg-route-mutate: on  │                         │                │
      │  {"model":"auto", ...}     │                         │                │
      │───────────────────────────►│                         │                │
      │                            │ (1) parse body          │                │
      │                            │ (2) gating header set?  │                │
      │                            │        → yes            │                │
      │                            │ (3) POST chat body ────►│                │
      │                            │                         │ rewrite body   │
      │                            │                         │ (select model, │
      │                            │                         │  edit params…) │
      │                            │◄──── 200 {"model":      │                │
      │                            │      "gpt-4o", ...} ────│                │
      │                            │ (4) working body =      │                │
      │                            │     mutated body        │                │
      │                            │ (5) x-ai-eg-model =     │                │
      │                            │     "gpt-4o"            │                │
      │                            │ (6) BodyMutation +      │                │
      │                            │     ClearRouteCache ───────────────────► │
      │                            │                         │   (7) re-match │
      │                            │                         │   rule x-ai-eg-│
      │                            │                         │   model==gpt-4o│
      │                            │                         │   → backend A  │
      │◄──────────────────────────────── response ────────────────────────────│
```

The body shown above is a chat-completions request only as an example; the gateway
forwards whatever the client sent — same endpoint path, same schema — and the service
returns the same schema. When the gating header is **absent**, step (2) short-circuits:
the filter skips the call and proceeds exactly as today (no mutation, no added latency).

The key point: steps (3)–(4) happen **inside** the single AI Gateway filter while the
request is on the catch-all route. The existing `ClearRouteCache` re-match (step 7) then
routes to the correct header-keyed rule using the **post-mutation** model. The two-route /
landing-route machinery is reused exactly as-is.

### The mutation-service contract

- **Request:** the gateway calls the service at the **same endpoint URL as the inbound
  request** (i.e., it mirrors the original request path and method) and forwards the
  **buffered request body unchanged**, preserving the original `Content-Type`. The body is
  treated as opaque — the gateway does not require it to be any particular schema. A
  configurable allow-list of request headers may be forwarded for context.
- **Response (success):** `200 OK` with a body in the **same schema as the request that
  was sent** (i.e., the same schema the client used). The gateway treats the returned body
  as the new working request body. Returned response headers (optional, behind an
  allow-list) may be applied as request-header mutations.
- **No-op:** returning the body unchanged (or `204 No Content`) means "no mutation".
- **Idempotency/scope:** the service must return a *request* body in the same schema, never
  a completion/response payload — it sits on the request path only.

### Header-based gating and configuration

This is **not** a new CRD field on `AIGatewayRoute`, and it does not change route/catch-all
generation. It has two independent pieces:

**1. Where to call (static filter configuration).** The mutation endpoint and call
parameters are part of the AI Gateway `ext_proc` filter's static configuration — the same
configuration the controller already renders for the filter (alongside flags such as the
root prefix and endpoint prefixes). Sketch:

```yaml
# AI Gateway ext_proc filter configuration (NOT a field on the AIGatewayRoute CRD)
requestMutation:
  endpoint: http://semantic-router.svc:8080   # host/authority of any conforming service
  # No fixed path/schema: the gateway calls the service at the SAME endpoint URL as the
  # inbound request (mirrors the original request path) and forwards the body as-is.
  gatingHeader: x-ai-eg-route-mutate           # call ONLY when this header is present
  gatingHeaderValue: ""                         # optional: require an exact value
  timeoutMs: 250
  failureMode: FailOpen                         # FailOpen | FailClosed
  forwardRequestHeaders: [x-session-id]         # optional allow-list to forward
  applyResponseHeaders: []                      # optional allow-list to apply back
```

**2. When to call (the gating header, per request).** The filter invokes the service for a
request only if that request carries the configured `gatingHeader` (optionally matching
`gatingHeaderValue`). Otherwise the call is skipped. Crucially, **setting the gating header
needs no new API** — it can be added by mechanisms that already exist in the data path:

- A Gateway-API / Envoy Gateway **`RequestHeaderModifier`** filter on the specific
  `HTTPRoute` or listener that should consult the service — this enables it
  per-route/per-listener declaratively, without touching `AIGatewayRoute`.
- An upstream external-authorization or policy hop that adds the header for selected
  tenants.
- The client itself, for explicit opt-in.

```
  enable for a route, declaratively, with a standard header-modifier filter:

  HTTPRoute (or listener)
    filters:
      - type: RequestHeaderModifier
        requestHeaderModifier:
          set:
            - name: x-ai-eg-route-mutate
              value: "on"          ──►  router ext_proc sees the gating header  ──►  calls
                                                                                     mutation
  no filter set?  ──►  header absent  ──►  router ext_proc skips the call (today's behavior)
```

So enablement is purely a function of header presence: per-request, per-route, per-tenant,
toggled with existing primitives. There is **no** `EnvoyPatchPolicy`, no second `ext_proc`
definition, and no change to how the header-keyed and catch-all rules are generated.

### Behavior in the router processor

The call is inserted in `routerProcessor.ProcessRequestBody`
(`internal/extproc/processor_impl.go`) right after the body is parsed and **before**
`x-ai-eg-model` is set:

```go
// After r.eh.ParseBody(...) yields the parsed body and originalModel:

mc := r.config.RequestMutation
if mc != nil && gated(r.requestHeaders, mc.GatingHeader, mc.GatingHeaderValue) {
    mutated, hdrs, err := r.requestMutator.Mutate(ctx, r.originalRequestBodyRaw, r.requestHeaders)
    switch {
    case err != nil && mc.FailureMode == FailClosed:
        return createUserFacingErrorResponse(503, "MutationUnavailable", err.Error()), nil
    case err != nil:
        // FailOpen: log and continue with the original body unchanged.
        logger.Warn("request mutation failed; continuing fail-open", slog.Any("error", err))
    default:
        r.originalRequestBodyRaw = mutated.Raw   // working body becomes the mutated body
        r.forceBodyMutation = true               // emit a BodyMutation downstream
        originalModel = mutated.Model            // re-derive model from the mutated body
        applyHeaderAllowList(additionalHeaders, hdrs)
    }
}
// When the gating header is absent, the block is skipped entirely — zero added work.

// Existing logic continues unchanged:
//   r.requestHeaders[ModelNameHeaderKeyDefault] = originalModel
//   set x-ai-eg-model = originalModel
//   return CommonResponse{ HeaderMutation, BodyMutation, ClearRouteCache: true }

// gated reports whether the request opted in via the configured gating header.
func gated(h map[string]string, name, wantValue string) bool {
    v, ok := h[name]
    if !ok {
        return false
    }
    return wantValue == "" || v == wantValue
}
```

Because the proposal reuses the existing `BodyMutation` + `ClearRouteCache` path, no new
Envoy filter, route, or patch is introduced — only a header check and, when opted in, an
outbound HTTP call from within the filter the gateway already runs.

### Failure handling

- **`FailOpen` (default):** on timeout, connection error, non-2xx, or unparseable
  response, the gateway proceeds with the **original** body — the request behaves exactly
  as if the mutation service were not configured.
- **`FailClosed`:** the same conditions return an immediate `503` to the client. Use only
  when mutation is a hard requirement (e.g., a mandatory policy rewrite).
- **Bounds:** a per-call deadline (`timeoutMs`), a maximum body size, and a circuit breaker
  protect the hot path. The mutation call adds at most one RTT on the request path; the
  service can be co-located to minimize it.

## Why this avoids both problems

| Concern | Second ext_proc + EnvoyPatchPolicy | Router-phase gated call (this proposal) |
|---|---|---|
| Extra Envoy filter | yes | **no** |
| Filter-ordering management | manual, pinned | **none** (single filter) |
| EnvoyPatchPolicy / raw patches | required | **none** |
| New CRD surface | EnvoyPatchPolicy resource | **none** (filter config + a header) |
| Affected by catch-all collision | yes (route attachment) | **no** (runs on catch-all, re-matches after) |
| Enable / disable per route or tenant | edit/rebuild patches | **set or clear a gating header** |
| Blast radius on misconfig | whole listener | request-scoped (fail-open) |
| Per-model/route churn | grows the patch | constant config |

The two-route and landing-route mechanics are not "fixed" — they are **reused**. The
mutation runs during the catch-all (first) pass and sets the model; the existing
`ClearRouteCache` re-match then sends the request to the correct header-keyed rule. The
fragile part (a second filter ordered by patches) is removed entirely.

## Alternatives Considered

- **Second `ext_proc` ordered via `EnvoyPatchPolicy`/composite `ExtensionWithMatcher`.**
  The status quo workaround; rejected for the maintenance and stability reasons above.
- **A second (outer) Gateway that sets `x-ai-eg-model` upstream.** Removes the landing-
  route ambiguity by making the model known before the inner Gateway routes, but doubles
  the gateway footprint and request authorization/processing hops. Heavier than a single
  in-filter call.
- **External-authorization that only sets headers.** `ext_authz` can add a routing header
  but cannot rewrite the request body, so it cannot carry richer mutations (message/param
  rewrites). This proposal supports full body mutation via the existing `BodyMutation`
  path.

## Open Questions

1. **Gating-header convention:** what default name (`x-ai-eg-route-mutate`?) and value
   semantics (presence-only vs. exact value vs. value-as-mode) should ship, and should the
   header be stripped before the request leaves the gateway?
2. **Streaming requests:** the call is request-path only and unaffected by `stream=true`,
   but should we cap mutation for very large buffered bodies?
3. **Schema neutrality:** the body is forwarded opaquely on the same endpoint URL, so the
   contract is schema-agnostic by design. Open: how does the gateway re-derive
   `x-ai-eg-model` from the *returned* body across schemas — reuse the same model-extraction
   it already applies per endpoint, or have the service additionally return the model via a
   response header?
4. **Observability:** standard metrics for mutation latency, fail-open rate, and
   model-change rate (original vs. mutated model) — align with the original-model tracking
   proposal.

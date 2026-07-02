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
  - [Why the existing workarounds (EnvoyPatchPolicy / EnvoyExtensionPolicy) don't solve it](#why-the-existing-workarounds-envoypatchpolicy--envoyextensionpolicy-dont-solve-it)
- [Proposal](#proposal)
  - [Overview](#overview)
  - [Request flow](#request-flow)
  - [The mutation-service contract](#the-mutation-service-contract)
  - [Approach A: static configuration via Helm values](#approach-a-static-configuration-via-helm-values)
  - [Approach B: header added by the client or filter chain](#approach-b-header-added-by-the-client-or-filter-chain)
  - [Approach C: model-gated services via GatewayConfig](#approach-c-model-gated-services-via-gatewayconfig)
  - [Comparing the approaches](#comparing-the-approaches)
  - [Behavior in the router processor](#behavior-in-the-router-processor)
  - [Failure handling](#failure-handling)
- [Why this avoids both problems](#why-this-avoids-both-problems)
- [Alternatives Considered](#alternatives-considered)
- [Open Questions](#open-questions)

<!-- /toc -->

## Summary

Today, inserting a request-rewriting step (for example, a semantic-router that selects
the model from the prompt) *before* the AI Gateway makes its routing decision requires
wiring a **second `ext_proc` filter** ahead of the AI Gateway filter — either by
header-wrapping it with an `EnvoyPatchPolicy` composite, or by attaching an
`EnvoyExtensionPolicy` to the AIG-generated route. On a single Gateway both ultimately hang
the filter off the **shared catch-all route**, so a misconfiguration affects all traffic;
and the `EnvoyPatchPolicy` variant is additionally brittle to maintain (index-pinned,
unvalidated, must be re-keyed as models churn).

This proposal makes that step a capability of the **existing router-phase AI Gateway
filter** (`ai-gateway-extproc`): during request-body processing, the filter calls a
**generic external service**, forwarding the request **to the same endpoint URL and in the
same schema it arrived in**. The service mutates the body (e.g., model selection,
message/param rewriting) and returns it in that same schema. The gateway then re-derives
`x-ai-eg-model` from the returned body and lets native routing proceed — reusing the
existing route-cache-clear mechanism. The gateway does **not** impose any particular
request schema (e.g., chat-completions); whatever the client sent is what the service
receives and returns.

Three approaches are proposed for how the extproc learns whether and where to call:

- **Approach A — static configuration via Helm values:** the service URL and parameters
  are configured at deploy time; the extproc calls it for matched traffic.
- **Approach B — header added by the client or filter chain:** a pre-routing hop (or the
  client) adds a request header that gates the call (and optionally selects an allow-listed
  target); the extproc reacts to that header per request.
- **Approach C — model-gated services via `GatewayConfig`:** the set of callable
  third-party services is declared on the `GatewayConfig` resource, each entry gated by a
  model name; when that model appears in the request payload, the extproc calls the
  matching service.

None of the approaches add a second filter, filter ordering, or an `EnvoyPatchPolicy`, and
none change how routes are generated. Approaches A and B need no new CRD field; Approach C
adds a field to `GatewayConfig` (the resource that already configures the extproc), not to
`AIGatewayRoute`.

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

### Why the existing workarounds (EnvoyPatchPolicy / EnvoyExtensionPolicy) don't solve it

Two workarounds have been tried to slot a second SR `ext_proc` in front of the AI Gateway
filter on a single Gateway:

1. **`EnvoyPatchPolicy` composite-wrap (header-wrap the ext_proc).** A raw JSON patch
   replaces a listener filter's `typed_config` with an
   `envoy.extensions.common.matching.v3.ExtensionWithMatcher` composite that reads a header
   (e.g. `x-route-target`) and executes the wrapped SR `ext_proc` only when the header
   matches a value. This lets one SR filter per model coexist on the same gateway, gated by
   header.
2. **`EnvoyExtensionPolicy` on the AIG-generated `HTTPRoute`.** Instead of a raw listener
   patch, attach an `EnvoyExtensionPolicy` (`spec.extProc[]`) to the route so Envoy Gateway
   renders the SR `ext_proc` declaratively — no hand-written listener JSON.

Both are unsatisfactory, but for a reason that is worth stating precisely, because the
usual objection is misleading.

**The "it rewrites listener configuration" objection is misleading.** Yes, `EnvoyPatchPolicy`
edits the listener/HCM filter chain by raw JSON and a bad patch can break the listener — but
that framing is *avoidable*: Workaround 2 (`EnvoyExtensionPolicy` on a route) adds the same
`ext_proc` **without touching listener config at all**; it is a typed, route-scoped policy.
So "it patches the listener" is not the fundamental problem.

**The fundamental problem is the catch-all.** On a single gateway the SR filter must run
*before* the model is known, so the only route it can be attached to is the **catch-all**
(see the landing-route problem), which carries *all* first-pass traffic. Consequently:

- Even the "clean" `EnvoyExtensionPolicy`-on-a-route approach ends up **affecting all
  traffic**, because that route is the catch-all — it is route-scoped in name only.
- A misconfiguration — whether the patch or the EEP — impacts **all traffic**, not a
  subset, precisely because the catch-all is shared by every request.

**`EnvoyPatchPolicy` additionally cannot be maintained dynamically.** On top of the shared
catch-all blast radius, the composite-wrap patch is:

- **Index-pinned.** `jsonPatches[].path` is an RFC 6901 JSON Pointer
  (`/…/http_filters/<slot+2>/typed_config`) that addresses filters by array position. Envoy
  Gateway has no name-based addressing, so adding/removing a model shifts slots and forces
  every newer patch to be re-keyed — a slot allocator becomes necessary just to keep indices
  stable.
- **Unvalidated and version-fragile.** Free-form JSON bypasses CRD validation, and the
  filter set/order/naming is a controller implementation detail (documented as "subject to
  change" on the `AIGatewayRoute` type), so an upgrade can silently invalidate the patch.
- **Order maintained by hand.** The SR filter must stay strictly before the AI Gateway
  filter (via `--extProcBeforeFilterNames`), asserted manually rather than declaratively.

Net: `EnvoyExtensionPolicy` removes the raw-listener objection but **not** the catch-all
one, and `EnvoyPatchPolicy` carries **both** the catch-all blast radius **and** a brittle,
index-pinned maintenance burden that is impractical to manage dynamically as models/routes
churn.

```
  single Gateway — all first-pass traffic lands on the shared catch-all rule

  attach the SR ext_proc via EITHER:
    • EnvoyPatchPolicy      → header-wrap composite (matches x-route-target)
    • EnvoyExtensionPolicy  → spec.extProc on the AIG-generated HTTPRoute

  client ─►[ SR ext_proc ]─►[ AI Gateway ext_proc ]─► router ─► backend
               ▲
               └─ can only attach where the request is on its FIRST pass: the
                  shared CATCH-ALL rule (model not known yet). A misconfig here
                  (patch OR EEP) therefore impacts ALL traffic — every request
                  crosses the catch-all before it is routed to its real model.
```

## Proposal

### Overview

Instead of adding a second filter, let the **existing router-phase AI Gateway filter**
(the `ai-gateway-extproc`) make a synchronous, third-party **HTTP call to a generic
mutation service** during request-body processing — *before* it derives `x-ai-eg-model`
and clears the route cache.

Three deployment approaches are proposed; they share the same request flow, contract, and
router-processor mechanics, and differ only in **how the extproc learns whether and where
to call**:

- **Approach A — static configuration via Helm values.** The service URL and call
  parameters are baked into the extproc's configuration at deploy time (via Helm values).
  The extproc makes the call for matched traffic. Operator-owned, centrally managed.
- **Approach B — header added by the client or filter chain.** A pre-routing hop (or the
  client) adds a request header that gates the call (and optionally selects the target).
  The extproc reads that header and makes the call. Dynamic and per-request, no redeploy.
- **Approach C — model-gated services via `GatewayConfig`.** The callable third-party
  services are declared on the `GatewayConfig` resource, each gated by a model name. When
  the extproc extracts that model from the payload, it calls the matching service. Fully
  server-side and declarative, driven by the model the gateway already parses.

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

The diagram shows the header-gated variant (Approach B); step (2) is simply where the
extproc decides whether to call — via static config (Approach A), the gating header
(Approach B), or a model match in `GatewayConfig` (Approach C). The rest of the flow is
identical in all three. The body shown is a chat-completions request only as an example;
the gateway forwards whatever the client sent — same endpoint path, same schema — and the
service returns the same schema. When the call is **not enabled** for a request, step (2)
short-circuits and the filter proceeds exactly as today (no mutation, no added latency).

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

### Approach A: static configuration via Helm values

The mutation endpoint and call parameters are supplied **statically through the AI Gateway
Helm values** and rendered into the `ai-gateway-extproc` configuration (the same config the
controller already produces for the filter, alongside flags such as the root prefix and
endpoint prefixes). The extproc then makes the call for matched traffic. This is **not** a
new CRD field on `AIGatewayRoute` and does not change route/catch-all generation.

```yaml
# Helm values.yaml
extProc:
  requestMutation:
    enabled: true
    endpoint: http://semantic-router.svc:8080   # host/authority of any conforming service
    # No fixed path/schema: the extproc calls the service at the SAME endpoint URL as the
    # inbound request (mirrors the original request path) and forwards the body as-is.
    timeoutMs: 250
    failureMode: FailOpen                         # FailOpen | FailClosed
    forwardRequestHeaders: [x-session-id]         # optional allow-list to forward
    applyResponseHeaders: []                      # optional allow-list to apply back
    # Optional static gate so only part of the traffic is mutated. Empty = call for all
    # traffic handled by this extproc.
    gatingHeader: ""                              # e.g. "x-ai-eg-route-mutate"
    gatingHeaderValue: ""                         # optional exact-value match
```

**Characteristics**

- **Operator-owned and centrally managed.** The target and its parameters are fixed at
  deploy time; clients cannot influence them. This is the strongest posture against
  request forgery — there is no client-supplied URL to abuse.
- **Simple.** No per-request logic beyond the optional static gate; enablement is a
  deploy-time decision (global, or scoped by the optional gating header).
- **Trade-off:** changing the target or toggling it requires a Helm upgrade / config
  reload, and it is coarse-grained (per-deployment), so varying behavior per tenant or
  per request needs the optional gating header (which then overlaps with Approach B).

### Approach B: header added by the client or filter chain

The decision to call — and, optionally, **which** allow-listed target to use — is carried
in a **request header added by the client or by the filter chain**. The
`ai-gateway-extproc` reads that header in `ProcessRequestBody` and makes the call; when the
header is absent it skips the call (today's behavior).

The one hard requirement is timing: the mutation call happens *before* routing is finalized
(that is its purpose — it can change the model that decides the route), so the header must
already be present **when the router extproc runs**. It therefore has to be added by a
source evaluated *before* the extproc parses the body:

```
  who adds the gating header (pre-routing):

  (a) the client sets it         ──►  x-ai-eg-route-mutate: on   (per-request opt-in; untrusted)

  (b) the filter chain sets it   ──►  a pre-routing hop (e.g. ext_authz / external policy)
      based on identity/tenant        injects the header for selected tenants/identities

                        │
                        ▼
      ai-gateway-extproc (ProcessRequestBody) sees x-ai-eg-route-mutate  ──►  calls mutation
      no header?                                                         ──►  skips the call

  optional target selection (an operator allow-list key, never a raw URL):
      x-ai-eg-route-mutate-target: semantic-router
```

**Characteristics**

- **Dynamic, per-request/per-tenant.** Enablement (and target selection) can vary per
  request (client) or per identity/tenant (a pre-routing filter-chain hop) and change at
  runtime with no redeploy. It cannot be keyed on the destination route, since gating
  precedes routing.
- **Decoupled.** The client / filter-chain hop owns enablement; the extproc just reacts to
  whatever header is already on the request.
- **Trade-off — trust and SSRF.** A header that carries a *raw target URL* is a
  server-side request forgery risk if it can originate from the client. The safe pattern
  (recommended): the header only **gates** the call and/or **selects a key** into an
  operator-configured allow-list of targets (still defined via Helm as in Approach A); the
  extproc never dials an arbitrary client-supplied URL. Inbound copies of the
  gating/selection headers must be **stripped at the edge** so only trusted filter-chain
  values survive.

### Approach C: model-gated services via GatewayConfig

Rather than a header or a single static target, declare the **set of callable third-party
services on the `GatewayConfig` resource** — the resource that already configures the
`ai-gateway-extproc` for a Gateway. Each entry is **gated by a model name**. The extproc
already extracts the model from the request body in `ProcessRequestBody`; if that model
matches a configured entry, the extproc calls the corresponding service. No header and no
client involvement are required — the gate is the model the gateway already parses.

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: GatewayConfig
metadata:
  name: ai-gateway-config
spec:
  # NEW: model-gated request-mutation services (a field on GatewayConfig, not AIGatewayRoute).
  requestMutationServices:
    - model: auto                                  # gate: incoming body "model" == "auto"
      endpoint: http://semantic-router.svc:8080    # same endpoint URL + schema as inbound
      timeoutMs: 250
      failureMode: FailOpen
    - model: smart-router
      endpoint: http://another-router.svc:9090
      failureMode: FailClosed
```

Flow:

```
  request body: {"model":"auto", ...}
        │
        ▼
  ai-gateway-extproc (ProcessRequestBody) extracts model = "auto"
        │
        ├─ matches a GatewayConfig entry (model: auto)  ──►  call that service, mutate body,
        │                                                     re-derive x-ai-eg-model
        └─ no matching entry                             ──►  skip the call (today's behavior)
```

**Characteristics**

- **Fully server-side and declarative.** The gate is the model in the payload — which the
  extproc already parses — so no client cooperation, no header hygiene, and no SSRF surface
  (targets are operator-defined in `GatewayConfig`).
- **Natural fit for "sentinel model" routing.** A client asks for a routing sentinel (e.g.
  `model: auto`), the matched service picks the real model and rewrites the body, and the
  existing `ClearRouteCache` re-match sends it to the concrete backend.
- **Trade-offs:** it **does** add a new field to `GatewayConfig` (though not to
  `AIGatewayRoute`, and no route-generation change); the gate is a **model name**, so it is
  gateway-scoped and only fires for models a client actually sends; and a model can map to
  at most one service (last-writer / validation rules needed for duplicates).

None of the three approaches use an `EnvoyPatchPolicy`, add a second `ext_proc`, or change
how the header-keyed and catch-all rules are generated.

### Comparing the approaches

| Dimension | A — static Helm | B — header (client/filter chain) | C — model-gated `GatewayConfig` |
|---|---|---|---|
| What triggers the call | matched traffic (optional static gate) | a pre-routing header | the **model** in the payload |
| Where the target comes from | Helm values | operator allow-list, selected by header | `GatewayConfig` entry for the model |
| Who decides | operator | client / pre-routing filter-chain hop | operator (per model) |
| New CRD field | no | no | **yes** (on `GatewayConfig`) |
| Granularity | per-deployment | per-request / per-tenant | **per model** |
| Trust / SSRF surface | minimal | must allow-list + strip inbound headers | minimal (no client input) |
| Change without redeploy | no | yes | yes (edit `GatewayConfig`) |
| Best for | one stable service, org-wide | per-tenant/identity rollout, opt-in | model-driven routing (e.g. `auto`) |

All three reuse the identical router-processor mechanics below; the only difference is how
the extproc resolves *whether to call* and *which target*.

### Behavior in the router processor

A single insertion point in `routerProcessor.ProcessRequestBody`
(`internal/extproc/processor_impl.go`) works for **all three** approaches, right after the
body is parsed and **before** `x-ai-eg-model` is set; only target/gate resolution differs:

```go
// After r.eh.ParseBody(...) yields the parsed body and originalModel:

mc := r.config.RequestMutation
if mc != nil {
    // resolveTarget encapsulates the difference between the three approaches:
    //   Approach A: return the statically configured endpoint (optionally gated).
    //   Approach B: gate on the request header and select an ALLOW-LISTED target by key.
    //   Approach C: match the parsed model against the GatewayConfig service table.
    // In no case does it dial an arbitrary client-supplied URL.
    if target, ok := resolveTarget(mc, originalModel, r.requestHeaders); ok {
        mutated, hdrs, err := r.requestMutator.Mutate(ctx, target, r.originalRequestBodyRaw, r.requestHeaders)
        switch {
        case err != nil && target.FailureMode == FailClosed:
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
}
// When the call is not enabled, the block is skipped entirely — zero added work.

// Existing logic continues unchanged:
//   r.requestHeaders[ModelNameHeaderKeyDefault] = originalModel
//   set x-ai-eg-model = originalModel
//   return CommonResponse{ HeaderMutation, BodyMutation, ClearRouteCache: true }

// resolveTarget decides whether to call and which allow-listed target to use.
func resolveTarget(mc *RequestMutation, model string, h map[string]string) (t Target, ok bool) {
    // Approach C: the parsed model gates the call and selects the GatewayConfig service.
    if t, ok = mc.ServiceForModel[model]; ok { // from GatewayConfig.requestMutationServices
        return t, true
    }
    // Approach B: header gates the call and (optionally) selects an allow-listed target.
    if mc.GatingHeader != "" {
        v, present := h[mc.GatingHeader]
        if !present || (mc.GatingHeaderValue != "" && v != mc.GatingHeaderValue) {
            return Target{}, false
        }
        if key := h[mc.TargetSelectHeader]; key != "" {
            t, ok = mc.Targets[key]
            return t, ok
        }
    }
    // Approach A: statically configured default endpoint.
    return mc.Default, mc.Default.Endpoint != ""
}
```

Because the proposal reuses the existing `BodyMutation` + `ClearRouteCache` path, no new
Envoy filter, route, or patch is introduced — only a config/header check and, when
enabled, an outbound HTTP call from within the filter the gateway already runs.

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

| Concern | Second ext_proc (EnvoyPatchPolicy / EnvoyExtensionPolicy) | Router-phase gated call (this proposal) |
|---|---|---|
| Extra Envoy filter | yes | **no** |
| Filter-ordering management | manual, pinned (`--extProcBeforeFilterNames`) | **none** (single filter) |
| Raw listener patch | yes for EnvoyPatchPolicy; no for EnvoyExtensionPolicy | **none** |
| Dynamic maintenance | index-pinned patches, slot allocator, re-key on churn | **constant config** |
| Attaches to | the shared catch-all route (only option pre-routing) | runs in-filter on the catch-all, re-matches after |
| Blast radius on misconfig | **all traffic** (shared catch-all) | request-scoped (fail-open) |

The two-route and landing-route mechanics are not "fixed" — they are **reused**. The
mutation runs during the catch-all (first) pass and sets the model; the existing
`ClearRouteCache` re-match then sends the request to the correct header-keyed rule. Unlike
the workarounds, the call lives *inside* the AI Gateway filter, so it does not attach a
second filter to the catch-all and a failure is contained to the single request (fail-open).

## Alternatives Considered

- **Second `ext_proc` via `EnvoyPatchPolicy` composite-wrap or `EnvoyExtensionPolicy` on
  the route.** The status-quo workarounds; both must hang the filter off the shared
  catch-all (so a misconfig affects all traffic), and the `EnvoyPatchPolicy` variant is
  additionally brittle to maintain dynamically. Rejected for the reasons detailed above.
- **A second (outer) Gateway that sets `x-ai-eg-model` upstream.** Removes the landing-
  route ambiguity by making the model known before the inner Gateway routes, but doubles
  the gateway footprint and request authorization/processing hops. Heavier than a single
  in-filter call.
- **External-authorization that only sets headers.** `ext_authz` can add a routing header
  but cannot rewrite the request body, so it cannot carry richer mutations (message/param
  rewrites). This proposal supports full body mutation via the existing `BodyMutation`
  path.

## Open Questions

1. **Which approach(es) to ship:** Approach A (static Helm), B (header-injected), C
   (model-gated `GatewayConfig`), or a combination? A reasonable default is A first
   (simplest, safest), then C for model-driven routing, and B where per-tenant/per-request
   gating is needed.
2. **Gating/selection-header convention (Approach B):** what default names
   (`x-ai-eg-route-mutate`, `x-ai-eg-route-mutate-target`?) and value semantics
   (presence-only vs. exact value vs. value-as-mode) should ship? Confirm targets are always
   resolved through an operator-defined allow-list (never a raw client URL), and that
   inbound copies of these headers are stripped at the edge.
3. **Model gate semantics (Approach C):** should `model` match exactly only, or also
   support wildcards/prefixes and a catch-all entry? How are duplicate `model` entries
   validated, and should a per-model list precede any static default from Approach A?
4. **Streaming requests:** the call is request-path only and unaffected by `stream=true`,
   but should we cap mutation for very large buffered bodies?
5. **Schema neutrality:** the body is forwarded opaquely on the same endpoint URL, so the
   contract is schema-agnostic by design. Open: how does the gateway re-derive
   `x-ai-eg-model` from the *returned* body across schemas — reuse the same model-extraction
   it already applies per endpoint, or have the service additionally return the model via a
   response header?
6. **Observability:** standard metrics for mutation latency, fail-open rate, and
   model-change rate (original vs. mutated model) — align with the original-model tracking
   proposal.

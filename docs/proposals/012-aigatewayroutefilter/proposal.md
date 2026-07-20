# Router-Phase ext_proc via an `AIGatewayExtensionPolicy` CRD

## Table of Contents

<!-- toc -->

- [Summary](#summary)
- [Background](#background)
  - [How an AIGatewayRoute becomes an HTTPRoute](#how-an-aigatewayroute-becomes-an-httproute)
  - [The two-route / landing-route problem (recap)](#the-two-route--landing-route-problem-recap)
  - [Why the existing workarounds fall short](#why-the-existing-workarounds-fall-short)
- [Problem Statement](#problem-statement)
- [Goals / Non-Goals](#goals--non-goals)
- [Proposal](#proposal)
  - [Overview](#overview)
  - [The `AIGatewayExtensionPolicy` CRD](#the-aigatewayextensionpolicy-crd)
  - [Scope: all catch-all routes](#scope-all-catch-all-routes)
  - [`headers`: gating the composite](#headers-gating-the-composite)
  - [Request flow](#request-flow)
  - [How the ext_proc is added at `PostTranslateModify`](#how-the-ext_proc-is-added-at-posttranslatemodify)
  - [Listing policies and building the composite (code)](#listing-policies-and-building-the-composite-code)
  - [Enablement: all catch-all routes](#enablement-all-catch-all-routes)
  - [Composite placement vs. per-route configuration](#composite-placement-vs-per-route-configuration)
  - [Envoy version requirement (CompositePerRoute over RDS)](#envoy-version-requirement-compositeperroute-over-rds)
  - [Policy lifecycle: add, update, and remove](#policy-lifecycle-add-update-and-remove)
- [Code Changes](#code-changes)
  - [1. API types (`api/v1alpha1`)](#1-api-types-apiv1alpha1)
  - [2. Controller (`internal/controller`)](#2-controller-internalcontroller)
  - [3. Extension server (`internal/extensionserver`)](#3-extension-server-internalextensionserver)
  - [4. Manifests, RBAC, generated code](#4-manifests-rbac-generated-code)
  - [5. Tests](#5-tests)
- [End-to-end example: from CRDs to xDS](#end-to-end-example-from-crds-to-xds)
- [Validation on a live cluster](#validation-on-a-live-cluster)
- [Why this avoids the catch-all blast radius](#why-this-avoids-the-catch-all-blast-radius)
- [Alternatives Considered](#alternatives-considered)
- [Open Questions](#open-questions)

<!-- /toc -->

## Summary

This proposal introduces a new CRD, **`AIGatewayExtensionPolicy`**, that describes a
router-phase `ext_proc` (modeled on the `extProc` field of Envoy Gateway's
`EnvoyExtensionPolicy`) plus a **`headers`** gate (header name + value). Whenever a
policy exists, its ext_proc is wired onto **all AI Gateway catch-all
(`route-not-found`) routes**, gated by the policy's `headers`. This is deliberately
simple — the catch-all is where every request lands on its first pass (before
`x-ai-eg-model` exists), which is exactly the window a router-phase mutation needs.

The AI Gateway extension server, during its existing `PostTranslateModify` phase,
inserts a composite filter — named with its canonical registered name
`envoy.filters.http.composite` (this matters; see
[Envoy version requirement](#envoy-version-requirement-compositeperroute-over-rds)) —
(added **disabled**) into the listener HCM filter chain **ahead of the AI Gateway
`ext_proc`**, and then attaches a **`CompositePerRoute`** — whose match tree is keyed
on the policy's `headers` and whose `ExecuteFilterAction` delegates to the described
`ext_proc`. The `CompositePerRoute` is wrapped in an
`envoy.config.route.v3.FilterConfig` before being placed in the route's
`typed_per_filter_config` (mirroring how the AI Gateway router `ext_proc` is enabled
per route). Attaching the per-route config both **enables** the disabled composite
for that route and supplies its match tree. The composite is enabled on **all
catch-all (`route-not-found`) routes** — every policy on every catch-all — so a
request is covered on its first pass, before `x-ai-eg-model` exists and before
routing is finalized. (See
[Composite placement vs. per-route configuration](#composite-placement-vs-per-route-configuration)
for why `CompositePerRoute` — not `ExtensionWithMatcher` — is the mechanism that
makes per-route enablement possible.)

> **Envoy version prerequisite.** Resolving a route-level `CompositePerRoute` via
> Envoy Gateway's RDS requires **Envoy ≥ 1.39 / a `main` (dev) build that contains
> Envoy PR [#43996](https://github.com/envoyproxy/envoy/pull/43996) (commit
> `e842507299`, 2026-04-29)**. Envoy ≤ 1.38.x rejects the entire `RouteConfiguration`
> (see [Envoy version requirement](#envoy-version-requirement-compositeperroute-over-rds)).
> This was validated end-to-end on a cluster running `envoyproxy/envoy:distroless-dev`
> (`1.39.0-dev`); see [Validation on a live cluster](#validation-on-a-live-cluster).

This is a **declarative, name-based, validated** replacement for the
`EnvoyPatchPolicy` composite-wrap workaround: it keeps the "run a second ext_proc
before the AI Gateway decides the route" capability, but removes the index-pinned
raw-JSON fragility, and — because the composite gates on the policy's `headers` —
shrinks the blast radius from "all catch-all traffic" to "only traffic carrying
those headers."

## Background

### How an AIGatewayRoute becomes an HTTPRoute

The controller renders each `AIGatewayRoute` into a single `HTTPRoute`
(`internal/controller/ai_gateway_route.go`, `newHTTPRoute`) whose rules are:

- **One rule per `AIGatewayRoute` rule** — a path-prefix match (the root prefix,
  e.g. `/v1`) plus any header matches the user declared (model-based routing is a
  header match on `x-ai-eg-model`).
- **One controller-injected catch-all rule** — named `route-not-found`, matching
  only the root path prefix with no header match, appended last so that a request
  always matches *something* on the prefix even before the model is known.

### The two-route / landing-route problem (recap)

Because `x-ai-eg-model` is produced **server-side** by the AI Gateway router-phase
`ext_proc` (it parses the body, extracts the model, sets the header, and returns
`ClearRouteCache: true`), a fresh request has no `x-ai-eg-model` on its first pass.
The only rule it can match is the header-less **catch-all**. Only after
`ClearRouteCache` does Envoy re-match onto the correct header-keyed rule.

Consequently, anything that must run **before routing is finalized** runs while the
request is on the **catch-all** route, not on its eventual destination route. On a
single Gateway with multiple `AIGatewayRoute`s, all catch-alls collapse (same
prefix, no headers) to one surviving rule via Gateway-API conflict resolution, so
all first-pass traffic funnels through a single shared catch-all.

### Why the existing workarounds fall short

Slotting a second `ext_proc` (e.g. a semantic-router) in front of the AI Gateway
filter today is done via `EnvoyPatchPolicy` composite-wrap or `EnvoyExtensionPolicy`
on the generated route. Both must hang the filter off the shared catch-all, so a
misconfiguration affects **all** traffic; and the `EnvoyPatchPolicy` variant is
additionally index-pinned (RFC 6901 JSON Pointer into `http_filters[]`),
unvalidated, version-fragile, and requires manual filter ordering via
`--extProcBeforeFilterNames`.

(See Proposal 012 for the full treatment of these problems.)

## Problem Statement

We want a **first-class, declarative** way to run a router-phase `ext_proc`
before the AI Gateway makes its routing decision, that:

1. does not require hand-written `EnvoyPatchPolicy` JSON or manual filter ordering,
2. is validated and name-based (survives model/route churn and EG upgrades), and
3. limits the blast radius of a misconfiguration to the traffic the `headers` gate
   selects, rather than to all catch-all traffic.

## Goals / Non-Goals

**Goals**

- A CRD describing a router-phase `ext_proc`, wired onto **all** AI Gateway
  catch-all routes with no per-route targeting.
- Header-gated execution using the policy's own `headers` list, evaluated on the
  first (catch-all) pass.
- Reuse the existing catch-all / `ClearRouteCache` routing mechanics unchanged.

**Non-Goals**

- Changing how header-keyed / catch-all rules are generated.
- Replacing the AI Gateway router/upstream `ext_proc` split.
- Defining the mutation-service wire contract (that is Proposal 012's concern;
  this proposal is about *how a second ext_proc is wired in and gated*, and is
  complementary — the wrapped ext_proc may be a semantic-router or anything else).

## Proposal

### Overview

Add an `AIGatewayExtensionPolicy` CRD (an `ext_proc` description + `headers` gate). At
`PostTranslateModify`, the extension server lists all policies and injects a
**header-gated composite `ext_proc`** into the listener (added disabled, ordered
before the AI Gateway `ext_proc`), then enables it on **every** AI Gateway catch-all
route.

```
  AIGatewayExtensionPolicy "sr"                    AI Gateway catch-all routes
  ┌────────────────────────────────┐               (route-not-found, all AIGwRoutes)
  │ spec.headers:                   │  applied to   ┌──────────────────────────────┐
  │   - name: x-tenant-id           │ ────────────► │ httproute/.../rule/N  (catch- │
  │     value: premium              │               │   all, no header match)        │
  │ spec.extProc:                   │               │   → composite gated by         │
  │   backendRefs: [sr-svc]         │               │     x-tenant-id: premium       │
  │   processingMode: {...}         │               └──────────────────────────────┘
  └────────────────────────────────┘
    (every policy is wired onto every catch-all, gated by headers)
```

### The `AIGatewayExtensionPolicy` CRD

A new namespaced CRD in `api/v1alpha1`. Its spec reuses Envoy Gateway's `ExtProc`
type (so ext_proc semantics — backend refs, processing mode, timeouts, metadata
options — match `EnvoyExtensionPolicy` 1:1) plus a `headers` gate:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayExtensionPolicy
metadata:
  name: semantic-router
  namespace: default
spec:
  # Gate: the wrapped ext_proc runs only when ALL these request headers match.
  headers:
    - name: x-tenant-id
      value: premium
  # The ext_proc to run (same shape as EnvoyExtensionPolicy.spec.extProc).
  extProc:
    backendRefs:
      - name: semantic-router-svc
        port: 8080
    processingMode:
      request: Buffered
      response: Skip
    messageTimeout: 250ms
```

### Scope: all catch-all routes

**Merely defining a policy** wires its ext_proc onto **every AI Gateway catch-all
(`route-not-found`) route** during translation, gated by the policy's `headers`.
`AIGatewayRoute` and its rules are **unchanged**.

The catch-all is the right (and only) place this needs to attach: on the first pass a
request has no `x-ai-eg-model` and lands on the header-less catch-all, and on a
single Gateway all catch-alls collapse (same prefix, no headers) to one surviving
rule via Gateway-API conflict resolution — so wiring the ext_proc onto every
catch-all guarantees it runs on the first pass regardless of which `AIGatewayRoute`
eventually owns the request. Scoping to *specific* traffic is done entirely by the
`headers` gate, not by route selection.

### `headers`: gating the composite

The gate is the policy's explicit `headers` list (each a `name` + `value`). The
composite runs the wrapped ext_proc only when **all** listed request headers match.
Because these are ordinary client-supplied request headers, they are evaluable on
the **first pass**, while the request is still on the catch-all and `x-ai-eg-model`
does not yet exist — which is exactly the window a router-phase mutation needs.

An **empty `headers`** list means "no gate" — the ext_proc runs for *all* first-pass
traffic on the catch-all. This is allowed (useful for policies meant to apply to
every request, e.g. auth or PII redaction), but on the shared catch-all it re-widens
the blast radius to all traffic, so validation should warn/require an explicit opt-in
(see Open Questions).

### Request flow

```
  ┌────────┐   POST /v1/chat/completions      ┌──────────────────────────────┐
  │ Client │   x-tenant-id: premium           │ Envoy listener (HCM filters)  │
  │        │   {"model":"auto", ...}          │                               │
  └───┬────┘─────────────────────────────────►│                               │
      │                                        │ 1) match catch-all rule       │
      │                                        │    (no x-ai-eg-model yet)     │
      │                                        │ 2) composite ext_proc gate:   │
      │                                        │    x-tenant-id == premium ?   │
      │                                        │        yes → run SR ext_proc  │──► SR svc
      │                                        │            (mutate body,      │◄── 200 body
      │                                        │             set model=gpt-4o) │
      │                                        │ 3) AI Gateway ext_proc:       │
      │                                        │    parse body, set            │
      │                                        │    x-ai-eg-model=gpt-4o,       │
      │                                        │    ClearRouteCache            │
      │                                        │ 4) re-match header-keyed rule │
      │                                        │    x-ai-eg-model==gpt-4o       │
      │◄─────────────── response ──────────────│    → backend                  │
```

The composite runs **before** the AI Gateway `ext_proc`, so its body mutation is
in place by the time the model is derived; the existing `ClearRouteCache` re-match
then routes to the concrete backend. The two-route / landing-route machinery is
**reused**, not modified.

### How the ext_proc is added at `PostTranslateModify`

The AI Gateway extension server already patches xDS in `PostTranslateModify`
(`internal/extensionserver/post_translate_modify.go`) for InferencePool, the AI
Gateway router `ext_proc`, and quota rate limiting. We add one more step there,
structured like `maybeInjectQuotaRateLimiting`:

1. **List the policies.** List `AIGatewayExtensionPolicy`s via `s.k8sClient`. For each
   policy build one entry `{ policyName, headers, extProc, clusterName }`. There is no
   per-route mapping — every entry applies to every catch-all route.
2. **Ensure the ext_proc service cluster exists.** Because the backend is referenced
   from *our* CRD, Envoy Gateway does not synthesize a cluster for it. We build one
   the same way `buildQuotaRateLimitCluster` / the EPP clusters are built, and append
   to `req.Clusters` (dedup like the `extProcUDSExist` guard).
3. **Insert a `Composite` filter (disabled) before the AI Gateway `ext_proc`.** Add
   a single composite filter (`Composite{}`, no matcher), **named with its canonical
   registered name `envoy.filters.http.composite`**, into the HCM filter chain with
   `disabled: true`, reusing the ordering logic from
   `insertRouterLevelAIGatewayExtProc` / `insertAIGatewayExtProcFilter`. A disabled
   filter does nothing until a route supplies per-route config, which both enables
   and configures it — exactly how the AI Gateway ext_proc itself is inserted
   disabled-at-HCM then enabled per route. **Do not** wrap it in
   `ExtensionWithMatcher` — that makes the match tree gateway-global and cannot be
   scoped per route.
4. **Build the per-route `CompositePerRoute`.** For each entry, construct an
   `extprocv3.ExternalProcessor` from `spec.extProc`, wrap it in a
   `filters.http.composite.v3.ExecuteFilterAction`, and place it as the `on_match`
   action of a `CompositePerRoute.matcher` whose `HttpRequestHeaderMatchInput`
   predicates come from the policy's `headers`.
5. **Enable the `CompositePerRoute` on every catch-all route** (next section) via
   `TypedPerFilterConfig["envoy.filters.http.composite"]`. Each catch-all gets **all**
   policies merged into one `CompositePerRoute` (one matcher arm per policy). The
   `CompositePerRoute` is **wrapped in an `envoy.config.route.v3.FilterConfig`**
   (`FilterConfig{ config: <CompositePerRoute Any> }`) before being stored on the
   route — the same wrapper the AI Gateway router `ext_proc` uses for per-route
   enablement. Supplying this per-route config re-enables the disabled composite for
   that route. On routes with no per-route config (i.e. the header-keyed rule routes)
   the composite stays disabled (pure no-op).

### Listing policies and building the composite (code)

Because there is no targeting, the correlation step from earlier drafts disappears:
the extension server simply lists all policies into a flat slice of entries, and
enables that same slice on every catch-all route it finds while walking `req.Routes`.

```go
// extensionPolicyEntry is one AIGatewayExtensionPolicy. Multiple entries share ONE
// composite filter and are combined into a single CompositePerRoute whose matcher
// has one arm per entry (keyed by that entry's headers). See the note after this
// snippet.
type extensionPolicyEntry struct {
    // name is the per-policy delegate name used for the ExecuteFilterAction's inner
    // filter (unique per policy). The composite filter name and the per-route
    // TypedPerFilterConfig key are the SHARED composite name, not this.
    name string
    // headers are the policy's gate headers used to gate the composite (evaluated on
    // the first / catch-all pass).
    headers []aigv1b1.AIGatewayExtensionPolicyHeaderMatch
    // extProc is copied verbatim from the AIGatewayExtensionPolicy spec.
    extProc egv1a1.ExtProc
    // clusterName is the Envoy cluster synthesized for the ext_proc backend.
    clusterName string
}

// buildExtensionPolicyEntries lists AIGatewayExtensionPolicies and returns one entry
// per policy. Reads via s.k8sClient like listQuotaPolicies / maybeModifyCluster.
func (s *Server) buildExtensionPolicyEntries(ctx context.Context) ([]extensionPolicyEntry, error) {
    var policies aigv1b1.AIGatewayExtensionPolicyList
    if err := s.k8sClient.List(ctx, &policies); err != nil {
        return nil, err
    }
    out := make([]extensionPolicyEntry, 0, len(policies.Items))
    for i := range policies.Items {
        p := &policies.Items[i]
        out = append(out, extensionPolicyEntry{
            name:        extensionPolicyClusterName(p.Namespace, p.Name),
            headers:     p.Spec.Headers,
            extProc:     p.Spec.ExtProc,
            clusterName: extensionPolicyClusterName(p.Namespace, p.Name),
        })
    }
    return out, nil
}
```

The consumer then walks `req.Routes` and enables **all** entries on **every**
catch-all (`route-not-found`) route — because on the first pass a request funnels
through whatever single catch-all survives Gateway-API conflict resolution,
regardless of which route eventually owns it:

```go
allEntries, err := s.buildExtensionPolicyEntries(ctx)
// ... synthesize a cluster per entry (dedup by cluster name) ...

for _, routeCfg := range req.Routes {
    for _, vh := range routeCfg.VirtualHosts {
        for _, r := range vh.Routes {
            if !isCatchAllRoute(r) { // AIG-generated, header-less "route-not-found"
                continue
            }
            // All policies collapse into ONE CompositePerRoute whose matcher has one
            // arm per entry (gate = entry.headers, action = ExecuteFilterAction(
            // entry.extProc)). Keyed by the shared composite filter name, since
            // typed_per_filter_config must key on a listener filter. Supplying it
            // re-enables the disabled composite.
            cpr := buildCompositePerRoute(allEntries) // matcher_list with len(allEntries) arms
            setTypedPerFilterConfig(r, extensionPolicyCompositeName, cpr)
        }
    }
}
```

Because `typed_per_filter_config` is keyed by filter name and there is a single
composite filter per listener, several policies on the same catch-all must be merged
into one `CompositePerRoute` with multiple matcher arms (not multiple map entries).
This is a natural fit: the `matcher_list` evaluates arms in order and runs the first
matching `ExecuteFilterAction`.

Because the entry list is rebuilt from the live CRDs on every `PostTranslateModify`,
the injected set is always **derived state** — there is nothing to reconcile or
delete by hand (see the lifecycle section below).

### Enablement: all catch-all routes

The composite is added **disabled** at the HCM, then explicitly enabled per route by
attaching a `CompositePerRoute` via `TypedPerFilterConfig`. Enablement is applied to
**all `route-not-found` catch-all routes** — with *every* policy — because on the
first pass all traffic funnels through the single surviving catch-all (same prefix,
no headers) after Gateway-API conflict resolution. The catch-all rule carries
`Name: "route-not-found"`, which surfaces in the xDS route name and is matched on.

The composite is **not** enabled on the header-keyed rule routes. On those (and any
other route without a `CompositePerRoute`) the composite stays disabled and is a pure
no-op.

> **Single execution.** Because the composite is enabled **only** on the catch-all,
> the wrapped ext_proc runs **once** per request — on the first pass, before the AI
> Gateway `ext_proc` derives `x-ai-eg-model` and issues `ClearRouteCache`. After the
> re-match onto a header-keyed rule route (which has no composite), it does not run
> again. This avoids the double-execution hazard of also enabling on rule routes, at
> the cost of not covering requests that would somehow bypass the catch-all on their
> first pass (none do today, given the landing-route mechanics).

### Composite placement vs. per-route configuration

A natural objection (and a genuine Envoy constraint) is that a composite wrapped in
**`ExtensionWithMatcher`** is a **gateway/listener-level** construct: its
`xds_matcher` match tree applies to the whole HCM filter chain and cannot be scoped
per route by toggling it on/off. That is true, and it is *not* the mechanism this
proposal relies on.

Envoy exposes **two mutually-exclusive** ways to drive the composite filter (the
proto docs warn: never mix them — "Never set [the `matcher`] field when using the
Composite filter with the ExtensionWithMatcher which will result in undefined
behavior"):

| Mechanism | Where the match tree lives | Per-route? |
|---|---|---|
| `ExtensionWithMatcher{ xds_matcher }` wrapping the composite | listener/HCM (gateway-level) | only via `ExtensionWithMatcherPerRoute` *override*; wrapper anchored at listener |
| **bare `Composite{}` in `http_filters` + `CompositePerRoute{ matcher }`** | **the route's `typed_per_filter_config`** | **yes — this is the per-route API** |

This proposal uses the **second** mechanism. The `envoy.filters.http.composite`
filter is present (disabled) in the HCM chain (its *presence* is gateway-level —
which is true of every per-route filter: the filter must exist on the listener for a
route's `typed_per_filter_config` to bind, e.g. the AI Gateway ext_proc itself is
inserted disabled at the listener and enabled per route). The **match tree and the
`ExecuteFilterAction` that runs the policy's ext_proc live in a `CompositePerRoute`
attached to each catch-all route**. Supplying that per-route config both re-enables
the disabled composite for the route and provides its matcher.
`CompositePerRoute.matcher` is documented as the "override of the match tree for this
route," and `envoy.filters.http.ext_proc` is a valid `ExecuteFilterAction` delegate.

Because we write this xDS directly in `PostTranslateModify` (rather than through
Envoy Gateway's `EnvoyExtensionPolicy`), we are bound only by raw-Envoy capability,
which supports `CompositePerRoute`. So per-route enablement holds — via
`CompositePerRoute`, not `ExtensionWithMatcher`.

References: [Composite filter proto](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/composite/v3/composite.proto)
(`Composite`, `CompositePerRoute`, `ExecuteFilterAction`),
[Composite filter docs](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/composite_filter),
[per-route matcher support (envoy#27088)](https://github.com/envoyproxy/envoy/pull/27088).

### Envoy version requirement (CompositePerRoute over RDS)

Making `CompositePerRoute` work through Envoy Gateway's RDS delivery has **two hard
requirements** that were only discovered while validating on a cluster. Both are
reflected in the implementation.

**1. The composite filter MUST use its canonical name, and the per-route config MUST
be wrapped in `FilterConfig`.** Since Envoy 1.22, per-route `typed_per_filter_config`
is resolved **strictly by the config's type URL** (the map-key name is ignored;
`no_extension_lookup_by_name` is on). Envoy Gateway also validates RDS route configs
**eagerly and independently of the listener filter chain**. Two consequences:

- The HCM composite filter is named `envoy.filters.http.composite` (the canonical
  registered name), and the per-route key matches it.
- The `CompositePerRoute` is wrapped in `envoy.config.route.v3.FilterConfig`
  (`FilterConfig{ config: <Any> }`) — a core route type that always passes eager RDS
  validation — exactly as the AI Gateway router `ext_proc` per-route enablement does.

**2. The data-plane Envoy MUST be new enough to register `CompositePerRoute` for
by-type resolution.** This is the important one and is the subject of **failure #2**
below.

> **Failure #2 — Envoy ≤ 1.38.x NACKs the entire RouteConfiguration.**
> On Envoy `1.37.0` and `1.38.3`, attaching a `CompositePerRoute` (raw *or*
> `FilterConfig`-wrapped) causes Envoy to reject the whole route config:
>
> ```
> gRPC config for type.googleapis.com/envoy.config.route.v3.RouteConfiguration
> rejected: Didn't find a registered implementation for
> 'envoy.filters.http.composite' with type URL:
> 'envoy.extensions.filters.http.composite.v3.CompositePerRoute'
> ```
>
> Because the RDS update is rejected, **every route disappears and all requests get
> `HTTP 404`** (not just the policy-targeted traffic).
>
> **Root cause (upstream Envoy):** in Envoy ≤ 1.38.x the composite factory is
> declared as `DualFactoryBase<Composite>` — passing only the `ConfigProto`
> template argument. Since `DualFactoryBase<ConfigProto, RouteConfigProto = ConfigProto>`,
> the route-config type silently **defaults to `Composite`**, so
> `createEmptyRouteConfigProto()` returns `Composite` and the factory's
> `configTypes()` never registers `CompositePerRoute` in the by-type registry. As a
> result `getFactoryByType("…CompositePerRoute")` returns `nullptr` and Envoy throws.
> (The bare `Composite{}` filter on the listener is accepted fine; only the
> *route-level* `CompositePerRoute` is unresolvable.)
>
> **Fix / required version:** Envoy PR
> [#43996](https://github.com/envoyproxy/envoy/pull/43996) ("composite: add inline
> matcher support", commit `e842507299`, merged **2026-04-29**) changes the factory to
> `CommonFactoryBase<Composite, CompositePerRoute>` and adds
> `createRouteSpecificFilterConfigTyped(const CompositePerRoute&, …)`, which registers
> `CompositePerRoute` by type. This landed **after** the 1.38.x branch was cut, so it
> is present only on Envoy `main` / the eventual **1.39** release. **This feature
> therefore requires Envoy ≥ 1.39 (or a `main`/dev build ≥ `e842507299`).**
>
> **Concrete image used to validate:** `docker.io/envoyproxy/envoy:distroless-dev`
> (the rolling `main` distroless build, reporting `1.39.0-dev`). Set it on the
> `EnvoyProxy` CR the Gateway uses:
>
> ```yaml
> apiVersion: gateway.envoyproxy.io/v1alpha1
> kind: EnvoyProxy
> spec:
>   provider:
>     kubernetes:
>       envoyDeployment:
>         container:
>           image: docker.io/envoyproxy/envoy:distroless-dev
> ```
>
> Note: the stale `envoyproxy/envoy-dev` Docker Hub repo (last updated Aug 2025) does
> **not** contain the fix — use `envoyproxy/envoy:distroless-dev`.
>
> If a fixed Envoy is not available, the only 1.37/1.38-compatible alternative is to
> gate the composite at the **listener level** via `ExtensionWithMatcher` (gateway-wide,
> header-gated), giving up strict per-route scoping — see
> [Alternatives Considered](#alternatives-considered).

### Policy lifecycle: add, update, and remove

**Q: When an `AIGatewayExtensionPolicy` is deleted, will the extension server remove
the composite-wrapped `ext_proc`?**

**Yes — automatically, with no explicit teardown code.** `PostTranslateModify` is a
**stateless, full-recompute** hook: on every invocation it rebuilds the injected
xDS from scratch out of (a) the xDS Envoy Gateway hands it and (b) the current set
of `AIGatewayExtensionPolicy` CRDs it lists via `s.k8sClient`. The composite is
*derived state*, not stored state. So:

- **Delete (or edit) the policy** → `buildExtensionPolicyEntries` no longer emits
  that entry → the next translation's xDS simply does not contain the composite's
  cluster or that policy's `CompositePerRoute` arm; if no policy remains, the
  composite is enabled on no route and stays a disabled no-op.

There is nothing stale to clean up because the injection is never persisted between
translations — this is exactly how the existing InferencePool / quota injections
behave.

**The one real requirement is *triggering* a re-translation.** Envoy Gateway
re-runs `PostTranslateModify` when *its* watched inputs change (Gateways,
HTTPRoutes, …). It does **not** watch our CRDs, and — importantly — an
`AIGatewayExtensionPolicy` change does **not** by itself change any generated
`HTTPRoute` (routing structure is untouched by the policy), so EG would not
otherwise notice. Because the policy is **not** bound to a specific route, a change
must re-translate **every** Gateway that has AI Gateway catch-all routes. Two
mechanisms cover this, both already used in the codebase:

1. **Controller watch → gateway resync.** The controller watches
   `AIGatewayExtensionPolicy`; on any change it enumerates the AI Gateway–owned
   Gateways (via the existing `AIGatewayRoute` → Gateway indexing) and calls
   `syncGateways`, which sends a `GenericEvent` to the gateway controller and forces
   each Gateway (hence its xDS) to be re-translated.
2. **Encode policy identity into every generated HTTPRoute annotation.** `newHTTPRoute`
   already sets a `HACK` annotation so EG reconciles when backend refs change
   (`httpRouteBackendRefPriorityAnnotationKey = buildPriorityAnnotation(...)`). The
   controller stamps an analogous annotation on **every** generated `HTTPRoute`
   encoding the current set of policies (e.g. a hash of all policy
   names/generations in scope). When any policy is added/removed/changed, the
   annotation changes on all routes → the `HTTPRoute`s diff → EG re-translates → the
   recompute drops (or adds) the composite.

Together these guarantee that add/update/remove of a policy deterministically
converges the injected xDS, with the composite removed as soon as the policy is
gone.

## Code Changes

Brief, file-by-file. The intent is to mirror existing patterns (EPP ext_proc
injection, quota rate-limit injection) rather than introduce new machinery.

### 1. API types (`api/v1alpha1`)

- **`api/v1alpha1/ai_gateway_extension_policy.go`** (new): `AIGatewayExtensionPolicy`,
  `AIGatewayExtensionPolicyList`, `AIGatewayExtensionPolicySpec` (embedding
  `egv1a1.ExtProc`, plus `Headers`), the `AIGatewayExtensionPolicyHeaderMatch` type,
  and `AIGatewayExtensionPolicyStatus`. Kubebuilder markers copied from
  `AIGatewayRoute` / `QuotaPolicy`.
- **`api/v1alpha1/registry.go`**: register the new kinds in the scheme (alongside
  `AIGatewayRoute`, `QuotaPolicy`, etc.).

```go
// api/v1alpha1/ai_gateway_extension_policy.go
type AIGatewayExtensionPolicySpec struct {
    // Headers gate the composite: the wrapped ext_proc runs only when ALL listed
    // request headers match. Evaluable on the first (catch-all) pass. Empty means
    // "no gate" (runs for all first-pass traffic on the catch-all).
    //
    // +optional
    // +kubebuilder:validation:MaxItems=16
    Headers []AIGatewayExtensionPolicyHeaderMatch `json:"headers,omitempty"`

    // ExtProc mirrors EnvoyExtensionPolicy's extProc so semantics match EG.
    ExtProc egv1a1.ExtProc `json:"extProc"`
}

type AIGatewayExtensionPolicyHeaderMatch struct {
    // Name is the request header name to match.
    Name string `json:"name"`
    // Value is the exact value the header must have.
    Value string `json:"value"`
}
```

### 2. Controller (`internal/controller`)

- Watch `AIGatewayExtensionPolicy`; on any change, resync **all** AI Gateway–owned
  Gateways (reuse `syncGateways`) so `PostTranslateModify` re-runs — the policy is
  not bound to a specific route, so a change is potentially global.
- Stamp the policy-set annotation on **every** generated `HTTPRoute` (see lifecycle);
  set Accepted / ResolvedRefs status conditions on the policy.
- `newHTTPRoute` route/rule structure is **unchanged** (only the annotation is
  added).

### 3. Extension server (`internal/extensionserver`)

- **`extension_policy.go`** (new): `maybeInjectAIGatewayExtensionPolicies(ctx,
  clusters, listeners, routes)`, called from `PostTranslateModify` next to
  `maybeInjectQuotaRateLimiting`. Contains the fetch/mapping, cluster build,
  composite insertion, and per-route `CompositePerRoute` attachment described above.
- **`post_translate_modify.go`**: one added call in `PostTranslateModify`:

```go
req.Clusters, err = s.maybeInjectAIGatewayExtensionPolicies(ctx, req.Clusters, req.Listeners, req.Routes)
if err != nil {
    return nil, fmt.Errorf("failed to inject AIGatewayExtensionPolicies: %w", err)
}
```

- New helpers (all local to `extension_policy.go`): `buildExtensionPolicyCluster`,
  `insertCompositeBeforeAIGatewayExtProc` (adds `envoy.filters.http.composite`
  disabled, once per listener), `buildCompositePerRoute` (the `CompositePerRoute{
  matcher → ExecuteFilterAction(ext_proc) }` construction), and
  `enableCompositeOnCatchAllRoutes`.

The heart of the injection is `buildCompositePerRoute`. It turns the resolved
`extensionPolicyEntry` list for one route into a single
`xds.type.matcher.v3.Matcher` (a `matcher_list`) with **one arm per policy**: each
arm's predicate ANDs that policy's `headers`, and its `on_match` action is an
`ExecuteFilterAction` that delegates to the entry's `ExternalProcessor`. The whole
matcher is wrapped in a `CompositePerRoute` and marshalled to an `Any`. The caller
then wraps that `Any` in an `envoy.config.route.v3.FilterConfig`
(`FilterConfig{ config: <CompositePerRoute Any> }`) before dropping it into the
route's `TypedPerFilterConfig` under `extensionPolicyCompositeName` — the
`FilterConfig` indirection is what lets the config survive Envoy Gateway's eager RDS
validation (it is a registered core route type), deferring the inner
`CompositePerRoute` to filter-chain association at runtime, exactly as the AI Gateway
router `ext_proc` per-route enablement does.

Note the two distinct `TypedExtensionConfig` families involved: the matcher tree
(inputs and actions) uses the **xDS** core/matcher types
(`github.com/cncf/xds/go/xds/...`), while the delegated filter inside
`ExecuteFilterAction` uses Envoy's **config.core.v3** `TypedExtensionConfig`. Mixing
them up is the most common compile/most-annoying-runtime error here.

```go
import (
    xdscorev3 "github.com/cncf/xds/go/xds/core/v3"
    xdsmatcherv3 "github.com/cncf/xds/go/xds/type/matcher/v3"
    corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
    matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
    compositev3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/composite/v3"
    extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
)

// buildCompositePerRoute builds the per-route composite config for one route from
// all policy attachments (entries) enabled on it. Attached on the route's
// TypedPerFilterConfig, keyed by extensionPolicyCompositeName; supplying it also
// re-enables the disabled composite. Uses CompositePerRoute (NOT ExtensionWithMatcher)
// so the match tree is per-route.
func buildCompositePerRoute(entries []extensionPolicyEntry) (*anypb.Any, error) {
    matchers := make([]*xdsmatcherv3.Matcher_MatcherList_FieldMatcher, 0, len(entries))
    for i := range entries {
        e := &entries[i]

        // (1) Build the delegated ext_proc as an ExecuteFilterAction. The inner
        // TypedExtensionConfig is Envoy's config.core.v3 flavor.
        extProcAny, err := toAny(e.extProc) // *extprocv3.ExternalProcessor
        if err != nil {
            return nil, err
        }
        execAny, err := toAny(&compositev3.ExecuteFilterAction{
            TypedConfig: &corev3.TypedExtensionConfig{
                Name:        e.name, // per-policy delegate name
                TypedConfig: extProcAny,
            },
        })
        if err != nil {
            return nil, err
        }

        // (2) Build the predicate that ANDs this policy's gate headers.
        predicate, err := buildHeaderPredicate(e.headers)
        if err != nil {
            return nil, err
        }

        // (3) One arm: predicate -> run the ExecuteFilterAction. The on_match
        // action is an xDS-core TypedExtensionConfig ("composite-action").
        matchers = append(matchers, &xdsmatcherv3.Matcher_MatcherList_FieldMatcher{
            Predicate: predicate,
            OnMatch: &xdsmatcherv3.Matcher_OnMatch{
                OnMatch: &xdsmatcherv3.Matcher_OnMatch_Action{
                    Action: &xdscorev3.TypedExtensionConfig{
                        Name:        "composite-action",
                        TypedConfig: execAny,
                    },
                },
            },
        })
    }

    // (4) Wrap all arms in a matcher_list and hand it to CompositePerRoute.
    return toAny(&compositev3.CompositePerRoute{
        Matcher: &xdsmatcherv3.Matcher{
            MatcherType: &xdsmatcherv3.Matcher_MatcherList_{
                MatcherList: &xdsmatcherv3.Matcher_MatcherList{Matchers: matchers},
            },
        },
    })
}

// buildHeaderPredicate turns a policy's gate headers into a predicate that requires
// ALL of them (logical AND). A single header collapses to one SinglePredicate;
// multiple headers become an and_matcher over SinglePredicates. (An empty list is
// handled by the caller as "no gate" — the arm always matches.)
func buildHeaderPredicate(hs []aigv1b1.AIGatewayExtensionPolicyHeaderMatch) (*xdsmatcherv3.Matcher_MatcherList_Predicate, error) {
    single := func(h aigv1b1.AIGatewayExtensionPolicyHeaderMatch) (*xdsmatcherv3.Matcher_MatcherList_Predicate, error) {
        // Input: envoy.type.matcher.v3.HttpRequestHeaderMatchInput, packed into an
        // xDS-core TypedExtensionConfig.
        inputAny, err := toAny(&matcherv3.HttpRequestHeaderMatchInput{HeaderName: h.Name})
        if err != nil {
            return nil, err
        }
        // Exact value match.
        sm := &xdsmatcherv3.StringMatcher{
            MatchPattern: &xdsmatcherv3.StringMatcher_Exact{Exact: h.Value},
        }
        return &xdsmatcherv3.Matcher_MatcherList_Predicate{
            MatchType: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate_{
                SinglePredicate: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate{
                    Input: &xdscorev3.TypedExtensionConfig{Name: "request-headers", TypedConfig: inputAny},
                    Matcher: &xdsmatcherv3.Matcher_MatcherList_Predicate_SinglePredicate_ValueMatch{
                        ValueMatch: sm,
                    },
                },
            },
        }, nil
    }

    if len(hs) == 1 {
        return single(hs[0])
    }
    preds := make([]*xdsmatcherv3.Matcher_MatcherList_Predicate, 0, len(hs))
    for _, h := range hs {
        p, err := single(h)
        if err != nil {
            return nil, err
        }
        preds = append(preds, p)
    }
    return &xdsmatcherv3.Matcher_MatcherList_Predicate{
        MatchType: &xdsmatcherv3.Matcher_MatcherList_Predicate_AndMatcher{
            AndMatcher: &xdsmatcherv3.Matcher_MatcherList_Predicate_PredicateList{Predicate: preds},
        },
    }, nil
}

// extensionPolicyCompositeName is the single composite filter shared by all
// AIGatewayExtensionPolicy attachments on a listener. Per-route CompositePerRoute
// entries key on this name.
//
// It MUST be the canonical registered composite name. Envoy validates RDS route
// configs independently of the listener filter chain and resolves a route's
// typed_per_filter_config strictly by type URL (name lookup is disabled since 1.22).
// A non-canonical name would fail resolution and Envoy would NACK the whole
// RouteConfiguration ("Didn't find a registered implementation … CompositePerRoute").
const extensionPolicyCompositeName = "envoy.filters.http.composite"

// insertCompositeBeforeAIGatewayExtProc inserts one composite filter (Composite{},
// no matcher, disabled) into each HCM filter chain of the listener, immediately
// before the AI Gateway ext_proc filter (aiGatewayExtProcName). Because it is added
// disabled, it does nothing until a route supplies a CompositePerRoute (which both
// enables and configures it), so it is safe to keep on every AIG listener. It is
// idempotent and mirrors the write-back style of insertRouterLevelAIGatewayExtProc /
// injectQuotaRateLimitFilterIntoListener.
//
// MUST run after the AI Gateway ext_proc has been inserted (i.e. after
// maybeModifyListenerAndRoutes), so the ordering anchor exists on the chain.
func (s *Server) insertCompositeBeforeAIGatewayExtProc(ln *listenerv3.Listener) error {
    filterChains := ln.GetFilterChains()
    if ln.DefaultFilterChain != nil {
        filterChains = append(filterChains, ln.DefaultFilterChain)
    }
    for _, currChain := range filterChains {
        httpConManager, hcmIndex, err := findHCM(currChain)
        if err != nil {
            return fmt.Errorf("failed to find HCM in filter chain: %w", err)
        }

        // Single pass over httpConManager.HttpFilters to compute:
        //   - alreadyPresent: is extensionPolicyCompositeName already in the chain?
        //   - aiGatewayIndex:  index of aiGatewayExtProcName (our ordering anchor).
        // (Trivial loop omitted for brevity.)
        alreadyPresent, aiGatewayIndex := scanCompositeAndAnchor(httpConManager.HttpFilters)

        if alreadyPresent {
            continue // Idempotent across re-translations.
        }
        if aiGatewayIndex == -1 {
            continue // No AI Gateway ext_proc on this chain => nothing to gate.
        }

        compositeAny, err := toAny(&compositev3.Composite{})
        if err != nil {
            return fmt.Errorf("failed to marshal Composite to Any: %w", err)
        }
        compositeFilter := &httpconnectionmanagerv3.HttpFilter{
            Name:       extensionPolicyCompositeName,
            Disabled:   true, // enabled per route via CompositePerRoute
            ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: compositeAny},
        }

        // Insert immediately before the AI Gateway ext_proc filter (append + shift).
        httpConManager.HttpFilters = append(httpConManager.HttpFilters, nil)
        copy(httpConManager.HttpFilters[aiGatewayIndex+1:], httpConManager.HttpFilters[aiGatewayIndex:])
        httpConManager.HttpFilters[aiGatewayIndex] = compositeFilter

        // Write the updated HCM back into the filter chain.
        hcAny, err := toAny(httpConManager)
        if err != nil {
            return fmt.Errorf("failed to marshal updated HCM to Any: %w", err)
        }
        currChain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: hcAny}
    }
    return nil
}
```

`compositev3` is
`github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/composite/v3`.
Insertion is anchored on `aiGatewayExtProcName` rather than the router filter so the
composite is guaranteed to sit directly ahead of the AI Gateway ext_proc regardless
of what other ext_proc/EPP filters EG placed on the chain — the ordering guarantee
the `EnvoyPatchPolicy` workaround had to assert by hand.

### 4. Manifests, RBAC, generated code

- `manifests/charts/ai-gateway-crds-helm/templates/aigateway.envoyproxy.io_aigatewayextensionpolicies.yaml` (new CRD manifest). `AIGatewayRoute` CRD is unchanged.
- Add `aigatewayextensionpolicies` (+`/status`) to controller RBAC.
- Regenerate `zz_generated.deepcopy.go`, clientset/informers/listers, and
  `site/docs/api/api.mdx`.

### 5. Tests

- `internal/extensionserver/extension_policy_test.go`: given policy fixtures and a
  synthetic `PostTranslateModifyRequest`, assert the composite is inserted (disabled)
  before `ai-gateway-extproc`, gated by the right header matchers, the ext_proc
  cluster exists, and it's enabled on `route-not-found` catch-all routes only (not on
  header-keyed rule routes).
- Controller tests for status conditions and the policy-set annotation stamped on
  generated `HTTPRoute`s.
- `api/v1alpha1` deepcopy/registry tests.
- `cmd/aigw` translate golden files if an example is added.

## End-to-end example: from CRDs to xDS

This section walks a single concrete example all the way from the user-authored
CRDs to the xDS that Envoy runs, so the whole path can be reviewed at once.

### Step 0 — user-authored CRDs

```yaml
# The extension policy (semantic router): headers gate + extProc.
# It applies to every AI Gateway catch-all route, gated by headers.
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayExtensionPolicy
metadata:
  name: semantic-router
  namespace: default
spec:
  headers:
    - name: x-tenant-id
      value: premium
  extProc:
    backendRefs:
      - name: semantic-router-svc     # a Service (or EG Backend) in the namespace
        port: 8080
    processingMode:
      request: Buffered
      response: Skip
    messageTimeout: 250ms
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: chat
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
      kind: Gateway
  rules:
    # rule[0]: premium tenants (unchanged — no filter field on the route).
    - matches:
        - headers:
            - name: x-tenant-id
              value: premium
      backendRefs:
        - name: gpt-4o-backend
    # rule[1]: normal model-keyed routing.
    - matches:
        - headers:
            - name: x-ai-eg-model
              value: llama-3
      backendRefs:
        - name: llama-backend
```

### Step 1 — controller renders the HTTPRoute (unchanged structure + one annotation)

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: chat                 # same name as the AIGatewayRoute
  namespace: default
  annotations:
    aigateway.envoyproxy.io/generated: "true"
    # existing HACK annotation that forces EG re-translation on ref changes:
    aigateway.envoyproxy.io/backend-ref-priority: "<hash>"
    # NEW: encodes the in-scope policy set (all policies) so add/remove re-translates
    # every generated HTTPRoute (the policy is not bound to a specific route):
    aigateway.envoyproxy.io/extension-policies: "default/semantic-router@<gen>"
spec:
  rules:
    - matches: [{ path: {value: /v1}, headers: [{name: x-tenant-id, value: premium}] }]
      backendRefs: [{ name: gpt-4o-backend, ... }]
    - matches: [{ path: {value: /v1}, headers: [{name: x-ai-eg-model, value: llama-3}] }]
      backendRefs: [{ name: llama-backend, ... }]
    - name: route-not-found      # controller-injected catch-all (unchanged)
      matches: [{ path: {value: /v1} }]
      filters: [{ extensionRef: ai-eg-route-not-found-response }]
```

Note the policy did **not** add an HTTPRoute filter or change any route match — it
only produced the `extension-policies` annotation (stamped on every generated
`HTTPRoute`, since the policy is not route-scoped).

### Step 2 — Envoy Gateway translates to xDS and calls `PostTranslateModify`

EG produces the baseline xDS (listener with HCM, route config
`httproute/default/chat/rule/*`, clusters for the backends). The AI Gateway
extension server then patches it. After `buildExtensionPolicyEntries` resolves:

```
allEntries = [
  {
    name:        "aigw-extpolicy/default/semantic-router",
    headers:     [ {name: x-tenant-id, value: premium} ],   // policy.spec.headers
    extProc:     <from AIGatewayExtensionPolicy>,
    clusterName: "aigw-extpolicy/default/semantic-router",
  },
]   // one entry per policy; enabled on EVERY catch-all route
```

the following xDS is added/modified.

**(a) A cluster for the ext_proc backend** (synthesized by us, since EG does not
create one for our CRD):

```yaml
name: aigw-extpolicy/default/semantic-router
type: STRICT_DNS
load_assignment:
  endpoints:
    - lb_endpoints:
        - endpoint: { address: { socket_address: { address: semantic-router-svc.default, port_value: 8080 } } }
```

**(b) A single `Composite` filter (disabled), inserted into the HCM before the AI
Gateway `ext_proc`.** No matcher here — it does nothing until a route supplies a
`CompositePerRoute` (which also re-enables it):

```yaml
http_filters:
  # ... (buffer, api-key auth, etc.) ...
  - name: envoy.filters.http.composite                        # NEW, composite (disabled)
    disabled: true                                            # canonical registered name
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.Composite
  - name: envoy.filters.http.ext_proc/aigateway               # existing AI Gateway ext_proc
    disabled: true
  - name: envoy.filters.http.router
```

**(c) The catch-all route enables + configures the composite (gate + delegate) and
enables the AI Gateway ext_proc.** Only the catch-all gets a `CompositePerRoute`; the
header-keyed rule routes (`rule/0`, `rule/1`) do not (the composite stays disabled on
them):

```yaml
# route config: httproute/default/chat/rule/2  (the route-not-found catch-all)
- name: httproute/default/chat/rule/2/match/0-...   # name resolves to route-not-found
  match: { prefix: /v1 }
  typed_per_filter_config:
    envoy.filters.http.ext_proc/aigateway:
      "@type": type.googleapis.com/envoy.config.route.v3.FilterConfig
      config: {}
    envoy.filters.http.composite:                     # canonical composite filter name
      # CompositePerRoute is wrapped in FilterConfig so it survives EG's eager RDS
      # validation (FilterConfig is a registered core route type); the inner
      # CompositePerRoute is resolved at filter-chain association time.
      "@type": type.googleapis.com/envoy.config.route.v3.FilterConfig
      config:
        "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.CompositePerRoute
        matcher:
          matcher_list:
            matchers:
              - predicate:
                  single_predicate:
                    input:
                      "@type": type.googleapis.com/envoy.type.matcher.v3.HttpRequestHeaderMatchInput
                      header_name: x-tenant-id
                    value_match: { exact: premium }        # from policy.spec.headers
                on_match:
                  action:
                    name: composite-action
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.ExecuteFilterAction
                      typed_config:
                        name: aigw-extpolicy/default/semantic-router
                        typed_config:
                          "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                          grpc_service: { envoy_grpc: { cluster_name: aigw-extpolicy/default/semantic-router } }
                          processing_mode: { request_body_mode: BUFFERED, response_header_mode: SKIP }
                          message_timeout: 0.250s
```

The composite is enabled on the catch-all **only**, so the policy runs once on the
first pass and does not re-run after `ClearRouteCache` re-matches onto a header-keyed
rule route. The per-route key is the **canonical composite filter name**
(`envoy.filters.http.composite`), not a per-policy name, because
`typed_per_filter_config` must key on a filter present in the HCM chain and is
resolved by type URL. The value is a `FilterConfig` wrapping the `CompositePerRoute`
so it passes Envoy Gateway's eager RDS validation.

### Step 3 — request-time behavior

```
POST /v1/chat/completions   x-tenant-id: premium   {"model":"auto", ...}

  match catch-all (rule/2)
    │
    ├─ composite gate: x-tenant-id == premium → run semantic-router ext_proc
    │      → body mutated: {"model":"gpt-4o", ...}
    │
    ├─ AI Gateway ext_proc: parse body → x-ai-eg-model=gpt-4o → ClearRouteCache
    │
    └─ re-match → rule[0] (x-tenant-id==premium) → gpt-4o-backend
                  (or a header-keyed model rule, per your route design)
```

A request **without** `x-tenant-id: premium` fails the composite gate on the
catch-all, the semantic router never runs, and it proceeds exactly as today — this
is the shrunk blast radius in action.

### Removal

Delete the `AIGatewayExtensionPolicy`: the controller rewrites the
`extension-policies` annotation on every generated route (and/or resyncs the
gateways) → EG re-translates → the recompute yields no entries → the ext_proc cluster
(a) is absent and no catch-all gets a `CompositePerRoute`, so the composite (b) stays
a disabled no-op (c). No teardown code runs; nothing is re-emitted.

## Validation on a live cluster

The end-to-end path was exercised on a real cluster to confirm the design works and
to surface the failures documented above. Setup:

- A trivial gRPC ext_proc (`testextproc/`) that logs every phase and stamps an
  `x-extproc-<phase>: seen` header on each of `REQUEST_HEADERS`, `REQUEST_BODY`,
  `RESPONSE_HEADERS`, `RESPONSE_BODY`, deployed as a `Deployment` + `Service`.
- An `AIGatewayExtensionPolicy` gated on `x-enable-extproc: "true"`, with
  `extProc.backendRefs` pointing at the test service. It wires onto every AI Gateway
  catch-all route.
- The AI Gateway controller image carrying this implementation, and — critically —
  the data-plane Envoy set to `docker.io/envoyproxy/envoy:distroless-dev`
  (`1.39.0-dev`, contains PR #43996).

Results:

- **Without** the `x-enable-extproc: true` header → `HTTP 200`, **no** `x-extproc-*`
  response headers; the ext_proc is never contacted (gate not matched).
- **With** the header → `HTTP 200` with `x-extproc-response-headers: seen` and
  `x-extproc-response-body: seen`; the test ext_proc logs show all four phases,
  confirming the composite-wrapped router-phase ext_proc ran.
- The final `EnvoyProxy`/Envoy config dump shows the `envoy.filters.http.composite`
  filter inserted **disabled** in the HCM chain ahead of the AI Gateway ext_proc, and
  the route-level `typed_per_filter_config` carrying the `FilterConfig`-wrapped
  `CompositePerRoute` with the header gate and the `ExecuteFilterAction` delegate.

## Why this avoids the catch-all blast radius

| Concern | EnvoyPatchPolicy / EnvoyExtensionPolicy | `AIGatewayExtensionPolicy` (this proposal) |
|---|---|---|
| Raw listener JSON patch | yes (EPP) | **no** (typed xDS at `PostTranslateModify`) |
| Index-pinned / re-key on churn | yes (EPP) | **no** (name-based) |
| Filter ordering | manual (`--extProcBeforeFilterNames`) | **enforced** by insertion helper |
| Where it attaches | shared catch-all (all first-pass traffic) | all catch-all routes, but **gated** by `headers` |
| Blast radius on misconfig | all catch-all traffic | **only traffic carrying the policy `headers`** |
| Validated CRD | no (free-form JSON) | **yes** |

The routing mechanics are **reused**: the composite runs during the catch-all pass
and mutates the request; the AI Gateway `ext_proc` then derives `x-ai-eg-model` and
`ClearRouteCache` re-matches to the concrete backend.

## Alternatives Considered

- **`EnvoyPatchPolicy` composite-wrap / `EnvoyExtensionPolicy` on the route.** The
  status-quo workarounds; rejected for the shared-catch-all blast radius and (for
  EPP) index-pinned fragility.
- **A new HTTPRoute filter type instead of xDS injection.** Rejected because the
  request is on the catch-all on the first pass, so a route-attached filter cannot
  be scoped to the destination rule without the same catch-all problem.
- **Listener-level `ExtensionWithMatcher` gate (instead of per-route
  `CompositePerRoute`).** This is the only option that works on Envoy ≤ 1.38.x, which
  cannot resolve `CompositePerRoute` over RDS (failure #2). It header-gates the
  composite for the whole HCM filter chain rather than per route, giving up strict
  per-route scoping (the gate is gateway-wide). Preferred approach remains
  `CompositePerRoute` on Envoy ≥ 1.39; this is the compatibility fallback for older
  data planes.

## Open Questions

1. **Empty `headers`.** An empty `headers` list means "no gate" (runs for all
   first-pass traffic on the catch-all), which reintroduces the full blast radius.
   Should validation require at least one header, or accept empty only with an
   explicit opt-in field?
2. **Backend reference kinds.** Should `spec.extProc.backendRefs` allow EG `Backend`
   objects (needs address resolution) or only `Service` (simple STRICT_DNS)?
3. **Multiple policies on one catch-all.** A single composite filter per listener;
   all policies merge into one `CompositePerRoute` with one matcher arm each. Since
   `matcher_list` runs the first matching arm, define arm ordering (policy name?
   creation time? most-specific first?) when several policies' `headers` overlap.
4. **Namespace / gateway scoping.** A policy currently applies to *every* catch-all
   the extension server sees during a translation. Should scope be restricted (e.g.
   to catch-alls whose `AIGatewayRoute` shares the policy's namespace, or to a
   specific `GatewayClass`), and how should that interact with multi-tenant gateways?
   The `headers` gate limits actual execution regardless.
5. **Standalone mode / `cmd/aigw`.** Ensure the injection path honors
   `isStandAloneMode` and the offline translate flow.
6. **Status/observability.** Conditions on `AIGatewayExtensionPolicy` (Accepted,
   ResolvedRefs) and metrics for gate hit-rate and ext_proc latency.

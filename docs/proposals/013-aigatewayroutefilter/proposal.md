# Per-Rule Router-Phase ext_proc via an `AIGatewayRouteFilter` CRD

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
  - [The `AIGatewayRouteFilter` CRD](#the-aigatewayroutefilter-crd)
  - [Attaching a filter to a rule](#attaching-a-filter-to-a-rule)
  - [`clientMatches`: gating on the first pass](#clientmatches-gating-on-the-first-pass)
  - [Request flow](#request-flow)
  - [How the ext_proc is added at `PostTranslateModify`](#how-the-ext_proc-is-added-at-posttranslatemodify)
  - [Building the `clientMatches → extProc` mapping (code)](#building-the-clientmatches--extproc-mapping-code)
  - [Scoping to the catch-all route](#scoping-to-the-catch-all-route)
  - [Composite placement vs. per-route configuration](#composite-placement-vs-per-route-configuration)
  - [Filter lifecycle: add, update, and remove](#filter-lifecycle-add-update-and-remove)
- [Code Changes](#code-changes)
  - [1. API types (`api/v1beta1`)](#1-api-types-apiv1beta1)
  - [2. Controller (`internal/controller`)](#2-controller-internalcontroller)
  - [3. Extension server (`internal/extensionserver`)](#3-extension-server-internalextensionserver)
  - [4. Manifests, RBAC, generated code](#4-manifests-rbac-generated-code)
  - [5. Tests](#5-tests)
- [End-to-end example: from CRDs to xDS](#end-to-end-example-from-crds-to-xds)
- [Why this avoids the catch-all blast radius](#why-this-avoids-the-catch-all-blast-radius)
- [Comparison with Proposal 012](#comparison-with-proposal-012)
- [Alternatives Considered](#alternatives-considered)
- [Open Questions](#open-questions)

<!-- /toc -->

## Summary

This proposal introduces a new CRD, **`AIGatewayRouteFilter`**, that describes a
router-phase `ext_proc` (modeled on the `extProc` field of Envoy Gateway's
`EnvoyExtensionPolicy`) and can be **attached to an individual rule of an
`AIGatewayRoute`**. When a filter is attached to a rule, the rule's `matches`
become **`clientMatches`** — the client-visible headers that gate the filter.

The AI Gateway extension server, during its existing `PostTranslateModify` phase,
places a bare `envoy.filters.http.composite` filter in the listener HCM filter
chain **ahead of the AI Gateway `ext_proc`**, and then attaches a
**`CompositePerRoute`** — whose match tree is keyed on the `clientMatches` and whose
`ExecuteFilterAction` delegates to the described `ext_proc` — **only on the
catch-all (`route-not-found`) routes** of every AIGatewayRoute-generated
`HTTPRoute`, which is exactly where a request lives on its first pass, before
`x-ai-eg-model` exists and before routing is finalized. (See
[Composite placement vs. per-route configuration](#composite-placement-vs-per-route-configuration)
for why `CompositePerRoute` — not `ExtensionWithMatcher` — is the mechanism that
makes per-route enablement possible.)

This is a **declarative, name-based, validated** replacement for the
`EnvoyPatchPolicy` composite-wrap workaround: it keeps the "run a second ext_proc
before the AI Gateway decides the route" capability, but removes the index-pinned
raw-JSON fragility, and — because the composite gates on the rule's client headers
— shrinks the blast radius from "all catch-all traffic" to "only traffic carrying
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

We want a **first-class, per-rule** way to run a router-phase `ext_proc` before the
AI Gateway makes its routing decision, that:

1. does not require hand-written `EnvoyPatchPolicy` JSON or manual filter ordering,
2. is validated and name-based (survives model/route churn and EG upgrades), and
3. limits the blast radius of a misconfiguration to the traffic it actually targets,
   rather than to all catch-all traffic.

## Goals / Non-Goals

**Goals**

- A CRD describing a router-phase `ext_proc`, attachable to a specific rule.
- Header-gated execution on the first pass, using the rule's own client matches.
- Reuse the existing catch-all / `ClearRouteCache` routing mechanics unchanged.

**Non-Goals**

- Changing how header-keyed / catch-all rules are generated.
- Replacing the AI Gateway router/upstream `ext_proc` split.
- Defining the mutation-service wire contract (that is Proposal 012's concern;
  this proposal is about *how a second ext_proc is attached and gated*, and is
  complementary — the attached ext_proc may be a semantic-router or anything else).

## Proposal

### Overview

Add an `AIGatewayRouteFilter` CRD (an `ext_proc` description). Reference it from an
`AIGatewayRoute` rule via `filterRefs`. The rule's `matches` become the
`clientMatches` for that filter. At `PostTranslateModify`, the extension server
resolves the mapping and injects a **header-gated composite `ext_proc`** into the
listener, enabled on the catch-all routes only, ordered before the AI Gateway
`ext_proc`.

```
  AIGatewayRoute rule[i]                         AIGatewayRouteFilter "sr"
  ┌───────────────────────────┐                  ┌───────────────────────────┐
  │ matches:                   │  filterRefs: sr  │ spec.extProc:              │
  │   headers:                 │ ───────────────► │   backendRefs: [sr-svc]    │
  │   - x-tenant-id: premium   │                  │   processingMode: {...}    │
  │   - x-ai-eg-model: gpt-4o  │                  │   messageTimeout: 250ms    │
  └───────────────────────────┘                  └───────────────────────────┘
             │
             │ clientMatches = rule.matches MINUS x-ai-eg-model
             ▼
       (x-tenant-id: premium)  ── gates the composite ext_proc on the catch-all
```

### The `AIGatewayRouteFilter` CRD

A new namespaced CRD in `api/v1beta1`, whose spec reuses Envoy Gateway's `ExtProc`
type so that the ext_proc semantics (backend refs, processing mode, timeouts,
metadata options) match `EnvoyExtensionPolicy` 1:1:

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRouteFilter
metadata:
  name: semantic-router
  namespace: default
spec:
  extProc:
    backendRefs:
      - name: semantic-router-svc
        port: 8080
    processingMode:
      request: Buffered
      response: Skip
    messageTimeout: 250ms
```

### Attaching a filter to a rule

`AIGatewayRouteRule` gains a `filterRefs` field (a local, optionally-namespaced
reference), mirroring the shape of `backendRefs`:

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
spec:
  rules:
    - matches:
        - headers:
            - name: x-tenant-id
              value: premium
      filterRefs:
        - name: semantic-router          # AIGatewayRouteFilter in the same namespace
      backendRefs:
        - name: gpt-4o-backend
```

Route/catch-all **generation is unchanged**; `filterRefs` only informs the
extension server what to inject and how to gate it.

### `clientMatches`: gating on the first pass

The gate must be evaluable on the **first pass**, while the request is on the
catch-all and `x-ai-eg-model` does not yet exist. Therefore `clientMatches` is
defined as:

```
clientMatches(rule) = rule.matches.headers  MINUS  { x-ai-eg-model }
```

i.e., only the **client-visible** headers. A rule whose only match is
`x-ai-eg-model` has no first-pass gate; attaching a filter to such a rule is
rejected by validation (see Open Questions) rather than silently degrading to
"all traffic."

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

1. **Fetch the mapping.** List `AIGatewayRoute`s and `AIGatewayRouteFilter`s via
   `s.k8sClient`. For each rule with `filterRefs`, build an entry:
   `{ filterName, clientMatches, extProc, clusterName, ownerRoute }`.
2. **Ensure the SR service cluster exists.** Because the backend is referenced from
   *our* CRD, Envoy Gateway does not synthesize a cluster for it. We build one the
   same way `buildQuotaRateLimitCluster` / the EPP clusters are built, and append
   to `req.Clusters` (dedup like the `extProcUDSExist` guard).
3. **Insert a bare `Composite` filter before the AI Gateway `ext_proc`.** Add a
   single `envoy.filters.http.composite` (`Composite{}`, no matcher) into the HCM
   filter chain, reusing the ordering logic from `insertRouterLevelAIGatewayExtProc`
   / `insertAIGatewayExtProcFilter`. With no per-route config it is a pass-through
   no-op, so it is safe to have present on every AIG listener. **Do not** wrap it in
   `ExtensionWithMatcher` — that makes the match tree gateway-global and cannot be
   scoped per route.
4. **Build the per-route `CompositePerRoute`.** For each entry, construct an
   `extprocv3.ExternalProcessor` from `spec.extProc`, wrap it in a
   `filters.http.composite.v3.ExecuteFilterAction`, and place it as the `on_match`
   action of a `CompositePerRoute.matcher` whose `HttpRequestHeaderMatchInput`
   predicates come from `clientMatches`.
5. **Attach the `CompositePerRoute` on the catch-all only** (next section) via
   `TypedPerFilterConfig[<composite-name>]`. On the header-keyed routes the composite
   has no per-route config and is a no-op, so the delegated ext_proc runs at most
   once — on the first (catch-all) pass.

### Building the `clientMatches → extProc` mapping (code)

Step 1 above is the correlation step, and it is worth spelling out because it is the
part reviewers most need to agree on. The extension server has no direct pointer
from an xDS route back to an `AIGatewayRoute`; instead it relies on the naming
convention that the generated `HTTPRoute` (and therefore the xDS route config) is
named after the `AIGatewayRoute` (`httproute/<namespace>/<name>/rule/<index>`, the
same convention `maybeModifyCluster` already parses). So the mapping is keyed by
`"<namespace>/<aiGatewayRouteName>"`, which lets us find the catch-all routes to
enable while walking route configs.

```go
// routeFilterEntry is one resolved (rule -> filter) attachment. Multiple entries on
// the same AIGatewayRoute share ONE bare composite filter and are combined into a
// single CompositePerRoute whose matcher has one arm per entry (keyed by that
// entry's clientMatches). See the note after this snippet.
type routeFilterEntry struct {
    // name is the per-attachment delegate name used for the ExecuteFilterAction's
    // inner filter (unique per entry). The composite filter name and the per-route
    // TypedPerFilterConfig key are the SHARED bare composite name, not this.
    name string
    // clientMatches are the rule's client-visible header matches (x-ai-eg-model
    // removed) used to gate the composite on the first (catch-all) pass.
    clientMatches []gwapiv1.HTTPHeaderMatch
    // extProc is copied verbatim from the AIGatewayRouteFilter spec.
    extProc egv1a1.ExtProc
    // clusterName is the Envoy cluster synthesized for the ext_proc backend.
    clusterName string
}

// buildRouteFilterEntries lists AIGatewayRoutes + AIGatewayRouteFilters and returns
// a map keyed by "namespace/aiGatewayRouteName" -> entries. Mirrors the shape of
// buildQuotaBackendPolicies (quota_ratelimit.go) and reads via s.k8sClient like
// listQuotaPolicies / maybeModifyCluster.
func (s *Server) buildRouteFilterEntries(ctx context.Context) (map[string][]routeFilterEntry, error) {
    var routes aigv1b1.AIGatewayRouteList
    if err := s.k8sClient.List(ctx, &routes); err != nil {
        return nil, err
    }
    out := make(map[string][]routeFilterEntry)
    for i := range routes.Items {
        route := &routes.Items[i]
        for ri := range route.Spec.Rules {
            rule := &route.Spec.Rules[ri]
            if len(rule.FilterRefs) == 0 {
                continue
            }
            // clientMatches must be evaluable on the first pass (no x-ai-eg-model yet).
            clientMatches := clientMatchHeaders(rule)
            if len(clientMatches) == 0 {
                // No first-pass gate; validation should have rejected this. Skip so a
                // misconfig can never widen to "all catch-all traffic".
                s.log.Info("skipping filter attachment with empty clientMatches",
                    "route", route.Name, "ruleIndex", ri)
                continue
            }
            for _, ref := range rule.FilterRefs {
                ns := route.Namespace
                if ref.Namespace != nil && *ref.Namespace != "" {
                    ns = string(*ref.Namespace)
                }
                var f aigv1b1.AIGatewayRouteFilter
                if err := s.k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &f); err != nil {
                    if apierrors.IsNotFound(err) {
                        // Filter (or its ref) was removed: simply do not inject it.
                        s.log.Info("AIGatewayRouteFilter not found, skipping",
                            "namespace", ns, "name", ref.Name)
                        continue
                    }
                    return nil, err
                }
                key := route.Namespace + "/" + route.Name
                out[key] = append(out[key], routeFilterEntry{
                    name:          routeFilterName(ns, ref.Name, route.Name, ri),
                    clientMatches: clientMatches,
                    extProc:       f.Spec.ExtProc,
                    clusterName:   routeFilterClusterName(ns, ref.Name),
                })
            }
        }
    }
    return out, nil
}

// clientMatchHeaders returns the rule's header matches with x-ai-eg-model removed,
// since that header does not exist on the first (catch-all) pass.
func clientMatchHeaders(rule *aigv1b1.AIGatewayRouteRule) []gwapiv1.HTTPHeaderMatch {
    var out []gwapiv1.HTTPHeaderMatch
    for mi := range rule.Matches {
        for _, h := range rule.Matches[mi].Headers {
            if string(h.Name) == internalapi.ModelNameHeaderKeyDefault { // "x-ai-eg-model"
                continue
            }
            out = append(out, h)
        }
    }
    return out
}
```

The consumer then walks `req.Routes`, and for each route config whose name resolves
to a key present in the map, enables + gates the corresponding entries on that
config's catch-all (`route-not-found`) route:

```go
for _, routeCfg := range req.Routes {
    key := aiGatewayRouteKeyFromRouteConfigName(routeCfg.Name) // "ns/name" or ""
    entries := entriesByRoute[key]
    if len(entries) == 0 {
        continue
    }
    for _, vh := range routeCfg.VirtualHosts {
        for _, r := range vh.Routes {
            if !isCatchAllRoute(r) { // route name / metadata carries "route-not-found"
                continue
            }
            // All entries for this route collapse into ONE CompositePerRoute whose
            // matcher has one arm per entry (gate = entry.clientMatches, action =
            // ExecuteFilterAction(entry.extProc)). Keyed by the shared bare composite
            // filter name, since typed_per_filter_config must key on a listener filter.
            cpr := buildCompositePerRoute(entries) // matcher_list with len(entries) arms
            setTypedPerFilterConfig(r, bareCompositeFilterName, cpr)
        }
    }
}
```

Because `typed_per_filter_config` is keyed by filter name and there is a single
bare composite filter per listener, several attachments on the same catch-all route
must be merged into one `CompositePerRoute` with multiple matcher arms (not multiple
map entries). This is a natural fit: the `matcher_list` evaluates arms in order and
runs the first matching `ExecuteFilterAction`.

Because the map is rebuilt from the live CRDs on every `PostTranslateModify`, the
injected set is always **derived state** — there is nothing to reconcile or delete
by hand (see the lifecycle section below).

### Scoping to the catch-all route

The `clientMatches` gate matches on **client** headers, which are present on *both*
the first-pass (catch-all) and the second-pass (header-keyed) route. To avoid
running the SR ext_proc twice, the `CompositePerRoute` is attached via
`TypedPerFilterConfig` **only on the `route-not-found` catch-all routes** of
AIGatewayRoute-generated HTTPRoutes, using the same route-identification approach as
`enableRouterLevelAIGatewayExtProcOnRoute` / `isRouteGeneratedByAIGateway`. The
catch-all rule carries `Name: "route-not-found"`, which surfaces in the xDS route
name and is matched on. On every other route the bare composite has no per-route
config and is a pass-through, so the delegated ext_proc runs at most once.

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

This proposal uses the **second** mechanism. The bare `envoy.filters.http.composite`
filter is present in the HCM chain (its *presence* is gateway-level — which is true
of every per-route filter: the filter must exist on the listener for a route's
`typed_per_filter_config` to bind, e.g. the AI Gateway ext_proc itself is inserted
at the listener and enabled per route). The **match tree and the `ExecuteFilterAction`
that runs the SR ext_proc live in a `CompositePerRoute` attached to the catch-all
route only.** `CompositePerRoute.matcher` is documented as the "override of the match
tree for this route," and `envoy.filters.http.ext_proc` is a valid
`ExecuteFilterAction` delegate.

Because we write this xDS directly in `PostTranslateModify` (rather than through
Envoy Gateway's `EnvoyExtensionPolicy`), we are bound only by raw-Envoy capability,
which supports `CompositePerRoute`. So per-route enablement holds — via
`CompositePerRoute`, not `ExtensionWithMatcher`.

References: [Composite filter proto](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/composite/v3/composite.proto)
(`Composite`, `CompositePerRoute`, `ExecuteFilterAction`),
[Composite filter docs](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/composite_filter),
[per-route matcher support (envoy#27088)](https://github.com/envoyproxy/envoy/pull/27088).

### Filter lifecycle: add, update, and remove

**Q: When a `filterRef` is removed from a rule (or the `AIGatewayRouteFilter` CRD is
deleted), will the extension server remove the composite-wrapped `ext_proc`?**

**Yes — automatically, with no explicit teardown code.** `PostTranslateModify` is a
**stateless, full-recompute** hook: on every invocation it rebuilds the injected
xDS from scratch out of (a) the xDS Envoy Gateway hands it and (b) the current set
of `AIGatewayRoute` / `AIGatewayRouteFilter` CRDs it lists via `s.k8sClient`. The
composite filter is *derived state*, not stored state. So:

- **Remove a `filterRef`** → `buildRouteFilterEntries` no longer emits that entry →
  the next translation's xDS simply does not contain the composite filter, its
  cluster, or its catch-all `TypedPerFilterConfig`.
- **Delete the `AIGatewayRouteFilter`** → the `s.k8sClient.Get` returns `NotFound`,
  the entry is skipped (see code above), same result.

There is nothing stale to clean up because the injection is never persisted between
translations — this is exactly how the existing InferencePool / quota injections
behave.

**The one real requirement is *triggering* a re-translation.** Envoy Gateway
re-runs `PostTranslateModify` when *its* watched inputs change (Gateways,
HTTPRoutes, …). It does **not** watch our CRDs, and — importantly — removing a
`filterRef` does **not** change the generated `HTTPRoute` (routing structure is
untouched by `filterRefs`), so EG would not otherwise notice. Two mechanisms cover
this, both already used in the codebase:

1. **Controller watch → gateway resync.** The controller watches
   `AIGatewayRouteFilter` (and `AIGatewayRoute`); on any change it calls
   `syncGateways`, which sends a `GenericEvent` to the gateway controller and forces
   the Gateway (hence its xDS) to be re-translated.
2. **Encode filter identity into an HTTPRoute annotation.** `newHTTPRoute` already
   sets a `HACK` annotation so EG reconciles when backend refs change
   (`httpRouteBackendRefPriorityAnnotationKey = buildPriorityAnnotation(...)`). We
   add an analogous annotation encoding the rules' `filterRefs` (e.g. a hash of the
   resolved filter names). When a `filterRef` is added/removed/changed, the
   annotation changes → the `HTTPRoute` diffs → EG re-translates → the recompute
   drops (or adds) the composite.

Together these guarantee that add/update/remove of a filter deterministically
converges the injected xDS, with the composite removed as soon as the attachment is
gone.

## Code Changes

Brief, file-by-file. The intent is to mirror existing patterns (EPP ext_proc
injection, quota rate-limit injection) rather than introduce new machinery.

### 1. API types (`api/v1beta1`)

- **`api/v1beta1/ai_gateway_route_filter.go`** (new): `AIGatewayRouteFilter`,
  `AIGatewayRouteFilterList`, `AIGatewayRouteFilterSpec` (embedding
  `egv1a1.ExtProc`), and `AIGatewayRouteFilterStatus`. Kubebuilder markers copied
  from `AIGatewayRoute`.
- **`api/v1beta1/ai_gateway_route.go`**: add `FilterRefs []AIGatewayRouteFilterRef`
  to `AIGatewayRouteRule`, plus the `AIGatewayRouteFilterRef` type (name +
  optional namespace, like `AIGatewayRouteRuleBackendRef`).
- **`api/v1beta1/ai_gateway_route_helper.go`**: add
  `(*AIGatewayRouteRule).ClientMatchHeaders()` returning `rule.Matches` headers
  minus `x-ai-eg-model`.
- **`api/v1beta1/registry.go`**: register the new kinds in the scheme.

```go
// api/v1beta1/ai_gateway_route_filter.go
type AIGatewayRouteFilterSpec struct {
    // ExtProc mirrors EnvoyExtensionPolicy's extProc so semantics match EG.
    ExtProc egv1a1.ExtProc `json:"extProc"`
}
```

### 2. Controller (`internal/controller`)

- Watch `AIGatewayRouteFilter`; on change, resync owning gateways (reuse
  `syncGateways`) so `PostTranslateModify` re-runs.
- Resolve `filterRefs` when reconciling `AIGatewayRoute`; set Accepted /
  ResolvedRefs status conditions on both resources.
- `newHTTPRoute` is **unchanged** (routing structure is untouched).
- Optionally add a field index `filter → gateways` for cheap lookups.

### 3. Extension server (`internal/extensionserver`)

- **`route_filter.go`** (new): `maybeInjectAIGatewayRouteFilters(ctx, clusters,
  listeners, routes)`, called from `PostTranslateModify` next to
  `maybeInjectQuotaRateLimiting`. Contains the fetch/mapping, cluster build, bare
  composite insertion, and per-route `CompositePerRoute` attachment described above.
- **`post_translate_modify.go`**: one added call in `PostTranslateModify`:

```go
req.Clusters, err = s.maybeInjectAIGatewayRouteFilters(ctx, req.Clusters, req.Listeners, req.Routes)
if err != nil {
    return nil, fmt.Errorf("failed to inject AIGatewayRoute filters: %w", err)
}
```

- New helpers (all local to `route_filter.go`): `buildRouteFilterCluster`,
  `insertBareCompositeBeforeAIGatewayExtProc` (adds `envoy.filters.http.composite`
  once per listener), `buildCompositePerRoute` (the `CompositePerRoute{ matcher →
  ExecuteFilterAction(ext_proc) }` construction), and
  `attachCompositePerRouteOnCatchAll`.

```go
// buildCompositePerRoute builds the per-route composite config that runs the SR
// ext_proc only when the clientMatches headers match. It is attached on the
// catch-all route's TypedPerFilterConfig, keyed by the bare composite filter name.
func buildCompositePerRoute(extProc *extprocv3.ExternalProcessor,
    clientMatches []gwapiv1.HTTPHeaderMatch) (*anypb.Any, error) {
    // ExecuteFilterAction{ TypedConfig: TypedExtensionConfig{ ext_proc } }
    // → xds matcher_list keyed by HttpRequestHeaderMatchInput(clientMatches)
    // → wrapped in compositev3.CompositePerRoute{ Matcher: <that match tree> }
    // NOTE: use CompositePerRoute (NOT ExtensionWithMatcher) so this is per-route.
}

// insertBareCompositeBeforeAIGatewayExtProc adds a single bare composite filter to
// the HCM chain (once), just before envoy.filters.http.ext_proc/aigateway. With no
// per-route config it is a pass-through no-op.
func (s *Server) insertBareCompositeBeforeAIGatewayExtProc(ln *listenerv3.Listener) error {
    // Composite{} as an HttpFilter named e.g. "aigw-routefilter/composite",
    // inserted before the AI Gateway ext_proc (mirror insertAIGatewayExtProcFilter).
}
```

### 4. Manifests, RBAC, generated code

- `manifests/charts/ai-gateway-crds-helm/templates/aigateway.envoyproxy.io_aigatewayroutefilters.yaml` (new CRD manifest); regenerate the `AIGatewayRoute` CRD for the new `filterRefs` field.
- Add `aigatewayroutefilters` (+`/status`) to controller RBAC.
- Regenerate `zz_generated.deepcopy.go`, clientset/informers/listers, and
  `site/docs/api/api.mdx`.

### 5. Tests

- `internal/extensionserver/route_filter_test.go`: given AIGatewayRoute + filter
  fixtures and a synthetic `PostTranslateModifyRequest`, assert the composite is
  inserted before `ai-gateway-extproc`, gated by the right header matchers, the SR
  cluster exists, and it's enabled only on `route-not-found`.
- Controller tests for ref resolution/status.
- `api/v1beta1` deepcopy/registry tests.
- `cmd/aigw` translate golden files if an example is added.

## End-to-end example: from CRDs to xDS

This section walks a single concrete example all the way from the user-authored
CRDs to the xDS that Envoy runs, so the whole path can be reviewed at once.

### Step 0 — user-authored CRDs

```yaml
# The ext_proc description (semantic router), reusing EG's extProc shape.
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRouteFilter
metadata:
  name: semantic-router
  namespace: default
spec:
  extProc:
    backendRefs:
      - name: semantic-router-svc     # a Service (or EG Backend) in the namespace
        port: 8080
    processingMode:
      request: Buffered
      response: Skip
    messageTimeout: 250ms
---
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: chat
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
      kind: Gateway
  rules:
    # rule[0]: premium tenants get semantic routing before the model is known.
    - matches:
        - headers:
            - name: x-tenant-id
              value: premium
      filterRefs:
        - name: semantic-router
      backendRefs:
        - name: gpt-4o-backend
    # rule[1]: normal model-keyed routing, no filter.
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
    # NEW: encodes attached filter identity so add/remove re-translates:
    aigateway.envoyproxy.io/filter-refs: "default/semantic-router@rule0"
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

Note the `filterRefs` on `rule[0]` did **not** add an HTTPRoute filter or change any
route match — it only produced the `filter-refs` annotation.

### Step 2 — Envoy Gateway translates to xDS and calls `PostTranslateModify`

EG produces the baseline xDS (listener with HCM, route config
`httproute/default/chat/rule/*`, clusters for the backends). The AI Gateway
extension server then patches it. After `buildRouteFilterEntries` resolves:

```
entriesByRoute["default/chat"] = [
  {
    name:          "aigw-routefilter/default/semantic-router/chat/0",
    clientMatches: [ {name: x-tenant-id, value: premium} ],   // x-ai-eg-model would be stripped
    extProc:       <from AIGatewayRouteFilter>,
    clusterName:   "aigw-routefilter/default/semantic-router",
  },
]
```

the following xDS is added/modified.

**(a) A cluster for the ext_proc backend** (synthesized by us, since EG does not
create one for our CRD):

```yaml
name: aigw-routefilter/default/semantic-router
type: STRICT_DNS
load_assignment:
  endpoints:
    - lb_endpoints:
        - endpoint: { address: { socket_address: { address: semantic-router-svc.default, port_value: 8080 } } }
```

**(b) A single bare `Composite` filter, inserted into the HCM before the AI Gateway
`ext_proc`.** No matcher, no per-route config here — it is a pass-through until a
route supplies a `CompositePerRoute`:

```yaml
http_filters:
  # ... (buffer, api-key auth, etc.) ...
  - name: aigw-routefilter/composite                          # NEW, bare composite (no matcher)
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.Composite
  - name: envoy.filters.http.ext_proc/aigateway               # existing AI Gateway ext_proc
    disabled: true
  - name: envoy.filters.http.router
```

**(c) The catch-all route supplies the `CompositePerRoute` (gate + delegate) and
enables the AI Gateway ext_proc:**

```yaml
# route config: httproute/default/chat/rule/2  (the route-not-found catch-all)
- name: httproute/default/chat/rule/2/match/0-...   # name resolves to route-not-found
  match: { prefix: /v1 }
  typed_per_filter_config:
    envoy.filters.http.ext_proc/aigateway:
      "@type": type.googleapis.com/envoy.config.route.v3.FilterConfig
      config: {}
    aigw-routefilter/composite:                       # keyed by the bare composite filter name
      "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.CompositePerRoute
      matcher:
        matcher_list:
          matchers:
            - predicate:
                single_predicate:
                  input:
                    "@type": type.googleapis.com/envoy.type.matcher.v3.HttpRequestHeaderMatchInput
                    header_name: x-tenant-id
                  value_match: { exact: premium }        # from clientMatches
              on_match:
                action:
                  name: composite-action
                  typed_config:
                    "@type": type.googleapis.com/envoy.extensions.filters.http.composite.v3.ExecuteFilterAction
                    typed_config:
                      name: semantic-router-extproc
                      typed_config:
                        "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                        grpc_service: { envoy_grpc: { cluster_name: aigw-routefilter/default/semantic-router } }
                        processing_mode: { request_body_mode: BUFFERED, response_header_mode: SKIP }
                        message_timeout: 0.250s
```

The header-keyed routes (`rule/0`, `rule/1`) get **no** `CompositePerRoute`, so the
bare composite is a no-op there and the semantic router runs at most once — on the
catch-all pass. Note the per-route key is the **bare composite filter name**
(`aigw-routefilter/composite`), not a per-attachment name, because
`typed_per_filter_config` must key on a filter present in the HCM chain.

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

Delete the `filterRef` (or the `AIGatewayRouteFilter`): the controller rewrites the
`filter-refs` annotation (and/or resyncs the gateway) → EG re-translates → the
recompute yields no entry for `default/chat` → xDS blocks (a), (b), (c) are all
absent. No teardown code runs; the composite is simply not re-emitted.

## Why this avoids the catch-all blast radius

| Concern | EnvoyPatchPolicy / EnvoyExtensionPolicy | `AIGatewayRouteFilter` (this proposal) |
|---|---|---|
| Raw listener JSON patch | yes (EPP) | **no** (typed xDS at `PostTranslateModify`) |
| Index-pinned / re-key on churn | yes (EPP) | **no** (name-based) |
| Filter ordering | manual (`--extProcBeforeFilterNames`) | **enforced** by insertion helper |
| Where it attaches | shared catch-all (all first-pass traffic) | catch-all, but **gated** by `clientMatches` |
| Blast radius on misconfig | all catch-all traffic | **only traffic carrying the client headers** |
| Validated CRD | no (free-form JSON) | **yes** |

The routing mechanics are **reused**: the composite runs during the catch-all pass
and mutates the request; the AI Gateway `ext_proc` then derives `x-ai-eg-model` and
`ClearRouteCache` re-matches to the concrete backend.

## Comparison with Proposal 012

Proposal 012 makes the mutation call a capability of the **existing** AI Gateway
router `ext_proc` (no second filter, an outbound HTTP call from within
`ProcessRequestBody`). This proposal instead keeps the mutation in a **separate,
attachable `ext_proc`** but makes attaching/gating it declarative and per-rule.

- **012** — fewest moving parts, no cluster to manage, no second filter; the
  gateway itself calls the service.
- **013 (this)** — reuses the standard `ext_proc` extension point (the service is a
  normal ext_proc, not a bespoke "mutation service" contract), and expresses
  enablement per rule via a CRD gated by client headers.

They are complementary; 012 and 013 can coexist (012 for the simple in-filter call,
013 for teams that want a standalone, per-rule, standard ext_proc).

## Alternatives Considered

- **`EnvoyPatchPolicy` composite-wrap / `EnvoyExtensionPolicy` on the route.** The
  status-quo workarounds; rejected for the shared-catch-all blast radius and (for
  EPP) index-pinned fragility.
- **In-filter call from the AI Gateway `ext_proc` (Proposal 012).** A different and
  complementary approach; see above.
- **A new HTTPRoute filter type instead of xDS injection.** Rejected because the
  request is on the catch-all on the first pass, so a route-attached filter cannot
  be scoped to the destination rule without the same catch-all problem.

## Open Questions

1. **`clientMatches` validity.** Reject attaching a filter to a rule whose only
   match is `x-ai-eg-model` (no first-pass gate)? Reject empty `clientMatches`
   (would match all traffic and reintroduce the blast radius)?
2. **Backend reference kinds.** Should `spec.extProc.backendRefs` allow EG `Backend`
   objects (needs address resolution) or only `Service` (simple STRICT_DNS)?
3. **Multiple filters per rule / per gateway.** A single bare composite filter per
   listener; multiple attachments on one catch-all route merge into one
   `CompositePerRoute` with one matcher arm each. Since `matcher_list` runs the first
   matching arm, define arm ordering (rule order? most-specific first?) when several
   entries' `clientMatches` overlap.
4. **Double execution.** Catch-all-only enablement prevents the second-pass re-run;
   is there a case where the SR must also run post-model? If so, an "already
   mutated" sentinel header could gate it instead.
5. **Standalone mode / `cmd/aigw`.** Ensure the injection path honors
   `isStandAloneMode` and the offline translate flow.
6. **Status/observability.** Conditions on `AIGatewayRouteFilter` (ResolvedRefs,
   Accepted) and metrics for gate hit-rate and ext_proc latency.

# Router-phase `ext_proc` via an `AIGatewayExtensionPolicy` CRD

A short, top-to-bottom read (goal, gap, CRD, usage scenarios, architecture). For the
full mechanics — xDS injection, Envoy composite placement, code changes, live-cluster
validation — see the companion **[detailed-proposal.md](./detailed-proposal.md)**.

## What are we aiming at?

Let users attach a **router-phase `ext_proc`** (a semantic router, PII redactor,
auth mutator, …) to AI Gateway traffic through a **typed, validated CRD** —
`AIGatewayExtensionPolicy` — instead of hand-written `EnvoyPatchPolicy` JSON. The
policy describes the ext_proc (reusing Envoy Gateway's `extProc` shape), says which
traffic it is **for** (`targetRefs`), and **when it runs** (`headers` gate).

"Router-phase" = it runs **before** the AI Gateway decides the model/route, so it
can mutate the request body/headers that routing is based on.

## What does it lack today?

- **The two-route problem.** `x-ai-eg-model` is set server-side by the AI Gateway
  ext_proc, so a fresh request first lands on a header-less **catch-all** route; only
  after `ClearRouteCache` does it re-match the real route. Anything that must run
  before routing therefore has to hang off the shared catch-all.
- **Today's workarounds are fragile.** Slotting a second ext_proc in front is done
  via `EnvoyPatchPolicy` composite-wrap or `EnvoyExtensionPolicy` on the route:
  - raw, index-pinned JSON (RFC 6901 pointer into `http_filters[]`), unvalidated and
    version-fragile;
  - manual filter ordering (`--extProcBeforeFilterNames`);
  - it sits on the shared catch-all, so a mistake affects **all** traffic.
- **No first-class, gated, name-based option** that survives model/route churn and
  confines the blast radius to just the traffic that should be touched.

## Proposed CRD

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayExtensionPolicy
metadata:
  name: semantic-router
  namespace: default
spec:
  # WHICH traffic this is for (Gateway API policy attachment).
  targetRefs:
    - group: aigateway.envoyproxy.io
      kind: AIGatewayRoute # or kind: Gateway (see usage scenarios)
      name: chat
  # WHEN it runs: the wrapped ext_proc fires only if ALL these request headers match.
  # Must be client-supplied headers (present on the first pass, before x-ai-eg-model).
  headers:
    - name: x-tenant-id
      value: premium
  # WHAT runs: same shape as EnvoyExtensionPolicy.spec.extProc.
  extProc:
    backendRefs:
      - name: semantic-router-svc
        port: 8080
    processingMode:
      request: Buffered
      response: Skip
    messageTimeout: 250ms
```

| Field        | Meaning                                                                                   |
| ------------ | ----------------------------------------------------------------------------------------- |
| `targetRefs` | Which `AIGatewayRoute`(s) or `Gateway` the policy applies to. Also drives re-translation. |
| `headers`    | The gate — client headers that must all match for the ext_proc to run. Empty = no gate.   |
| `extProc`    | The ext_proc definition, identical to `EnvoyExtensionPolicy.spec.extProc`.                |

`AIGatewayRoute` is **unchanged** — no new `filterRefs` field; attachment lives on
the policy.

## Usage scenarios

The **target kind** selects the scope. We walk a minimal setup — **2
`AIGatewayRoute`s** (`chat`, `embeddings`) on one Gateway, **1 policy** (`sr`) gated
on `x-tenant-id: premium` — and show, per target kind, **where the composite ext_proc
is enabled** and the resulting **request flow**.

`[✔]` = composite enabled here, `[ ]` = not enabled.

```yaml
# Route A
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata: { name: chat, namespace: default }
spec:
  parentRefs: [{ name: eg, kind: Gateway }]
  rules:
    - matches: [{ headers: [{ name: x-ai-eg-model, value: gpt-4o }] }]
      backendRefs: [{ name: openai }]
---
# Route B
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata: { name: embeddings, namespace: default }
spec:
  parentRefs: [{ name: eg, kind: Gateway }]
  rules:
    - matches: [{ headers: [{ name: x-ai-eg-model, value: text-embed-3 }] }]
      backendRefs: [{ name: openai }]
```

Each `AIGatewayRoute` renders an `HTTPRoute` with its header-keyed rule **plus an
auto-generated catch-all rule** (the first-pass landing route). On a shared endpoint
the catch-alls collapse to **one surviving catch-all** for the whole Gateway.

### Scenario 1 — target an `AIGatewayRoute` (`chat`)

```yaml
spec:
  targetRefs:
    - { group: aigateway.envoyproxy.io, kind: AIGatewayRoute, name: chat }
  headers: [{ name: x-tenant-id, value: premium }]
  extProc:
    backendRefs: [{ name: semantic-router, port: 50051 }]
```

Composite enabled **only on `chat`'s routes**; the shared catch-all (and `chat`'s own
internal catch-all) is **skipped** — other routes are untouched.

```
Gateway eg
├─ chat        rule(x-ai-eg-model=gpt-4o)        [✔] composite(sr)
├─ embeddings  rule(x-ai-eg-model=text-embed-3)  [ ]   ← untouched
└─ CATCH-ALL (shared, first-pass landing)        [ ]   ← skipped

POST /v1/embeddings x-tenant-id: premium → CATCH-ALL (no composite) → embeddings rule → openai   (sr never runs)
POST /v1/chat/...   x-tenant-id: premium → CATCH-ALL (no composite) → AI-GW sets x-ai-eg-model
  → ClearRouteCache → chat rule [composite here], but the chain already ran → sr does NOT fire
```

Route-scoped, so `embeddings` is safe — **but** for a _router-phase_ mutation this
only works if `chat` is matchable on the **first pass** (a **client**-header-keyed
rule, or a distinct path/host so its catch-all does not collapse). For the shared
`x-ai-eg-model`-keyed setup above, a router-phase `sr` effectively never runs. Use
this scope for client-header-selected routes.

### Scenario 2 — target the `Gateway` (`eg`)

```yaml
spec:
  targetRefs:
    - { group: gateway.networking.k8s.io, kind: Gateway, name: eg }
  headers: [{ name: x-tenant-id, value: premium }]
  extProc:
    backendRefs: [{ name: semantic-router, port: 50051 }]
```

Composite enabled on **all catch-all routes** of the Gateway — **not** the
header-keyed rules. The shared catch-all is the effective execution point, so it
covers **every** route's first-pass traffic (still gated by `headers`).

```
Gateway eg
├─ chat        rule(x-ai-eg-model=gpt-4o)        [ ]
├─ embeddings  rule(x-ai-eg-model=text-embed-3)  [ ]
└─ CATCH-ALL (shared, first-pass landing)        [✔] composite(sr)   ← effective execution point

POST /v1/chat/...   x-tenant-id: premium → CATCH-ALL [composite matches] → run sr → ... → chat rule → openai
POST /v1/embeddings x-tenant-id: premium → CATCH-ALL [composite matches] → run sr → ... → embeddings rule → openai
```

Gateway-wide: `sr` runs on the first-pass catch-all for **all** routes' traffic. This
is the safe default for true router-phase mutation, at the cost of the widest scope —
the honest representation of the blast radius.

| Target kind      | Composite enabled on                         | Other routes affected?  | Router-phase works?                       |
| ---------------- | -------------------------------------------- | ----------------------- | ----------------------------------------- |
| `AIGatewayRoute` | only that route's routes (catch-all skipped) | no                      | only if the route is first-pass matchable |
| `Gateway`        | all catch-all routes                         | yes (if header matches) | yes (runs on shared catch-all)            |

In both cases the `headers` gate is what actually confines execution — a request runs
the ext_proc only when it carries the gate headers.

## Proposed architecture

### Control plane (controller + Envoy Gateway)

- A new controller **watches `AIGatewayExtensionPolicy`**, validates it, resolves
  `targetRefs`, and sets status conditions (`Accepted`, `ResolvedRefs`).
- **Re-translation trigger** (so Envoy picks up policy edits): preferred path is
  registering the CRD under Envoy Gateway's `extensionManager.policyResources`, so EG
  watches it natively and re-translates on any change — **no HTTPRoute annotation
  hack**. (Requires a `targetRef`, same-namespace attachment to the Gateway, and EG
  RBAC on the CRD; details in [detailed-proposal.md](./detailed-proposal.md).)
- The generated `HTTPRoute` structure is **not changed** by the policy.

### Data plane (extension server at `PostTranslateModify`)

- List the `AIGatewayExtensionPolicy` objects and, for each, **synthesize an ext_proc
  cluster** for its `extProc.backendRefs`.
- Insert a **composite filter** (`envoy.filters.http.composite`) into the listener HCM
  chain, **added disabled**, **ordered before** the AI Gateway ext_proc.
- **Enable** it per route via a `CompositePerRoute` (wrapped in
  `envoy.config.route.v3.FilterConfig`) whose match tree is keyed on the policy
  `headers` and whose action delegates to the ext_proc — on the catch-all route(s)
  (and, for `AIGatewayRoute` targets, that route's rules).
- Injection is **stateless / full-recompute**: delete or edit a policy and the next
  translation simply omits or updates it — nothing to tear down.

> **Envoy version prerequisite.** Route-level `CompositePerRoute` over RDS needs
> **Envoy ≥ 1.39** (contains Envoy PR #43996); ≤ 1.38.x NACKs the `RouteConfiguration`.
> Validated on `envoyproxy/envoy:distroless-dev`. See [detailed-proposal.md](./detailed-proposal.md).

## Request flow (at a glance)

```
Client → catch-all route (no x-ai-eg-model yet)
       → composite gate: do the policy headers match?
           yes → run the wrapped ext_proc (mutate body/headers)
       → AI Gateway ext_proc: parse body, set x-ai-eg-model, ClearRouteCache
       → re-match the real model-keyed route → backend
```

The composite runs **before** the AI Gateway ext_proc on the first pass, so its
mutation is in place by the time the model is derived. Existing catch-all /
`ClearRouteCache` mechanics are **reused, not modified**.

## Open questions

- Should an **empty `headers`** gate be disallowed (or require explicit opt-in), since
  it re-widens the blast radius to all first-pass traffic?
- Which **backend kinds** should `extProc.backendRefs` allow (`Service` only vs. EG
  `Backend`)?
- **Trigger mechanism** and **cross-namespace** attachment for `policyResources`
  (same-namespace-only today).
- Ordering when **multiple policies** overlap on one route.

_Full treatment of each: [detailed-proposal.md](./detailed-proposal.md)._

# Scoping example: where the ext_proc lands, per approach

Minimal example: **2 `AIGatewayRoute`s**, **1 `AIGatewayExtensionPolicy`** targeting
**one** of them (`chat`). Shows, per approach, **where the composite ext_proc is
enabled** and the **request flow**.

Legend: `[✔]` = composite enabled here, `[ ]` = not enabled.

## Resources

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
---
# Policy: targets ONE AIGatewayRoute (chat)
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayExtensionPolicy
metadata: { name: sr, namespace: default }
spec:
  targetRefs:
    - { group: aigateway.envoyproxy.io, kind: AIGatewayRoute, name: chat }
  headers: [{ name: x-tenant-id, value: premium }]  # the gate
  extProc:
    backendRefs: [{ name: semantic-router, port: 50051 }]
```

Each `AIGatewayRoute` renders an `HTTPRoute` with its header-keyed rule **plus an
auto-generated catch-all rule** (the first-pass landing route). On a shared endpoint
the catch-alls collapse to **one surviving catch-all** for the whole Gateway.

## Approach A — original: targeted route's rules + all catch-alls

Trigger = HTTPRoute annotation hack; `targetRef → AIGatewayRoute chat`. Enablement =
**the targeted route's own rules AND every catch-all on the Gateway**.

```
Gateway eg
├─ chat        rule(x-ai-eg-model=gpt-4o)        [✔] composite(sr)   ← targeted route's rule
├─ embeddings  rule(x-ai-eg-model=text-embed-3)  [ ]
└─ CATCH-ALL (shared, first-pass landing)        [✔] composite(sr)   ← all catch-alls
```

So **yes — `chat`'s first rule is enabled too.** But it is a **functional no-op**:
the HCM filter chain runs **once**, on the first pass while the request is still on
the catch-all; `ClearRouteCache` does not re-run it after re-routing to the
`gpt-4o` rule. The effective execution point is the **shared catch-all**.

**Request flow** (effective execution on the shared catch-all → also covers
`embeddings`):

```
POST /v1/chat/...   x-tenant-id: premium  {"model":"gpt-4o"}
  → CATCH-ALL [composite matches] → run sr → AI-GW ext_proc sets x-ai-eg-model=gpt-4o
  → ClearRouteCache → re-match chat rule (composite already ran, not re-run) → openai

POST /v1/embeddings x-tenant-id: premium  {"model":"text-embed-3"}
  → CATCH-ALL [composite matches] → run sr  ← affected, though only `chat` was targeted
  → AI-GW ext_proc → ClearRouteCache → embeddings rule → openai
```

Blast radius: the shared catch-all carries **all** first-pass traffic, so `sr` runs
for `embeddings` too (gated by `headers`).

## Approach B — `policyResources`: scoped by target kind

Trigger = EG watches the CRD natively (no annotation). Requires registering the CRD
under `extensionManager.policyResources`:

```yaml
# Envoy Gateway config (helm values / EnvoyGateway CR)
extensionManager:
  policyResources:
    - group: aigateway.envoyproxy.io
      version: v1alpha1
      kind: AIGatewayExtensionPolicy
  hooks:
    xdsTranslator:
      post: [Translation, Cluster, Route]
```

Enablement scope then follows the **target kind**.

### B.1 — `targetRef → AIGatewayRoute chat`

Composite enabled **only on `chat`'s routes**; the shared catch-all and `chat`'s own
internal catch-all are **skipped**.

```
Gateway eg
├─ chat        rule(x-ai-eg-model=gpt-4o)        [✔] composite(sr)
├─ embeddings  rule(x-ai-eg-model=text-embed-3)  [ ]   ← untouched
└─ CATCH-ALL (shared, first-pass landing)        [ ]   ← skipped
```

```
POST /v1/embeddings x-tenant-id: premium → CATCH-ALL (no composite) → embeddings rule → openai   (sr never runs)
POST /v1/chat/...   x-tenant-id: premium → CATCH-ALL (no composite) → AI-GW sets x-ai-eg-model
  → ClearRouteCache → chat rule [composite here], but the chain already ran → sr does NOT fire
```

Route-scoped and `embeddings` is safe — but for **router-phase** mutation this only
works if `chat` is matchable on the **first pass** (a **client**-header-keyed rule,
or a distinct path/host so its catch-all does not collapse). For the shared
`x-ai-eg-model`-keyed setup above, a router-phase `sr` effectively never runs.

### B.2 — `targetRef → Gateway eg`

Same policy, but the `targetRef` names the **Gateway** (must live in the Gateway's
namespace):

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayExtensionPolicy
metadata: { name: sr, namespace: default }
spec:
  targetRefs:
    - { group: gateway.networking.k8s.io, kind: Gateway, name: eg }
  headers: [{ name: x-tenant-id, value: premium }]
  extProc:
    backendRefs: [{ name: semantic-router, port: 50051 }]
```

Composite enabled on **all catch-all routes** of the Gateway — **not** the
header-keyed rules.

```
Gateway eg
├─ chat        rule(x-ai-eg-model=gpt-4o)        [ ]
├─ embeddings  rule(x-ai-eg-model=text-embed-3)  [ ]
└─ CATCH-ALL (shared, first-pass landing)        [✔] composite(sr)   ← effective execution point
```

```
POST /v1/chat/...   x-tenant-id: premium → CATCH-ALL [composite matches] → run sr → ... → chat rule → openai
POST /v1/embeddings x-tenant-id: premium → CATCH-ALL [composite matches] → run sr → ... → embeddings rule → openai
```

Gateway-wide: `sr` runs on the first-pass catch-all for **all** routes' traffic
(gated by `headers`). This is the safe default for true router-phase mutation, at
the cost of the widest scope — the honest representation of the blast radius.

## Summary

| Approach                     | Composite enabled on                     | `chat` first rule | `embeddings` affected? | Router-phase works?                       |
| ---------------------------- | ---------------------------------------- | ----------------- | ---------------------- | ----------------------------------------- |
| **A — targeted + catch-alls**| chat's rules + all catch-alls            | ✔ (no-op)         | yes (via shared catch-all) | yes (runs on shared catch-all)        |
| **B.1 — Route target**       | only chat's routes (catch-alls skipped)  | ✔                 | no                     | only if chat is first-pass matchable      |
| **B.2 — Gateway target**     | all catch-all routes only                | ✕                 | yes (if header matches)| yes (runs on shared catch-all)            |

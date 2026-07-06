# Routing & Policy

This page is the canonical home for Yggdrasil's routing domain model. The vocabulary is
defined in **`CONTEXT.md`** — use those exact terms in code, tests, and PRs. This page
explains how the model behaves; `CONTEXT.md` is the glossary of record.

## Domain glossary (from `CONTEXT.md`)

- **Source Route** — a Kubernetes routing input. Today: **Ingress** or **HTTPRoute**.
- **Route Policy** — Yggdrasil's internal, source-agnostic model of traffic behavior
  (timeouts, health checks, retries, upstream behavior, connection limits, sticky
  sessions). This is the shared model, **not** any Kubernetes object.
- **Policy Source** — the external representation a Route Policy is derived from. Today:
  legacy **annotations** or the **`YggdrasilPolicy`** CRD.
- **Effective Route Policy** — the single Route Policy applied to a Source Route after
  precedence, fallback, and conflict resolution.
- **YggdrasilPolicy** — the CRD (a *typed Policy Source* for HTTPRoutes). It is *not*
  the internal Route Policy.

Key relationships: a Source Route has **at most one** Effective Route Policy; a Policy
Source produces **zero or one** Route Policy. Annotations and the CRD are **both**
parsed into the same `RoutePolicy` model — one is never converted into the other.

## The `SourceRoute` model

`SourceRoute` (`pkg/k8s/generic_resources.go:15`) is the unified route produced by both
the Ingress and Gateway paths. It carries the rule hosts, upstreams (Ingress) /
upstream endpoints (Gateway), a per-host TLS map, a `RouteSource`
(`generic_resources.go:42`: Kind / Namespace / Name / Class / Cluster), and the resolved
`Policy *policy.RoutePolicy` + `PolicySource`. `GetSourceRoutes` concatenates Ingress
routes first, then Gateway routes, across all clusters.

## Ingress vs HTTPRoute precedence

When an Ingress and an HTTPRoute both claim the **same host** (the migration case),
HTTPRoute wins. This is resolved in the translator, not in `pkg/k8s`:

- `sourceKindPriority` (`pkg/envoy/ingress_translator.go:655`): HTTPRoute = 1,
  Ingress = 0.
- `translateIngresses` (`ingress_translator.go:462`) stably sorts the per-host group by
  that priority, so the higher-priority source's upstreams and policy take effect.
- `classFilter` (`ingress_translator.go:248`) requires Ingresses to match a configured
  ingress class but **always keeps** HTTPRoutes.

## The two Policy Sources have different failure semantics

Both produce a `RoutePolicy` (`pkg/policy/policy.go:16`), but they behave differently on
bad input — this is intentional:

| | Annotations | `YggdrasilPolicy` CRD |
|---|---|---|
| Parser | `ParseAnnotations` (`pkg/policy/annotations.go:16`) | `ParseSpec` (`pkg/policy/crd.go:31`) |
| Prefix / scope | `yggdrasil.uswitch.com/*` on the object | Same-namespace HTTPRoute via `TargetRef` |
| On invalid field | **Lenient** — skip that field, apply the rest | **Strict** — reject the *whole* policy |
| Applies to | Ingress **and** HTTPRoute | HTTPRoute only (`ValidateTargetRef`, `crd.go:15`) |

Legacy annotation quirks are preserved deliberately (e.g. an unparsable `weight`
becomes `1`; sticky sessions require cookie name/path/ttl). Annotation keys and their
Envoy meanings are documented in `README.md`; the annotation set is parsed in
`annotations.go:16` and stripped from the object by `StripAnnotations`
(`annotations.go:114`) so raw policy never rides on the shared model.

CRD spec fields (`pkg/apis/yggdrasil/v1alpha1/types.go:29`): `HealthCheck`, `Timeouts`
(default/route/perTry/cluster), `Retry.RetryOn`, `Upstream` (httpVersion "1.1"/"2",
weight — explicit `0` excludes the upstream), `Connection`
(idle/maxConnectionDuration/maxRequestsPerConnection), `StickySessions`. The manifest
`deploy/crds/yggdrasil.uswitch.com_yggdrasilpolicies.yaml` enforces the enums.

## How annotations and the CRD combine (HTTPRoute)

`attachPolicyToSourceRoute` (`pkg/k8s/gateway_resources.go:467`) resolves an HTTPRoute's
Effective Route Policy:

1. Policies are indexed by same-namespace target (`indexPoliciesByTarget`).
2. **Exactly one** `YggdrasilPolicy` targets the route → use it (`ParseSpec`). Invalid
   spec → route left with **no** policy + a diagnostic. **CRD takes precedence over
   annotations when present.**
3. **Zero** policies target the route → **fall back** to `ParseAnnotations` on the
   HTTPRoute's own annotations.
4. **More than one** policy targets the same HTTPRoute → **all ignored** (fail-safe), no
   merge, with a "duplicate YggdrasilPolicies" diagnostic.

Annotations are stripped in all cases. Ingress routes never consult the CRD — they use
`attachAnnotationPolicy` (`generic_resources.go:106`) only.

## Conflicts and merging

There is **no field-level merge** of policies.

- **Same host, same source kind, divergent policy** → the host is *dropped* from all
  conflicting routes with a diagnostic. `resolvePolicyConflicts`
  (`gateway_resources.go:517`) compares `RoutePolicy.Signature()`
  (`pkg/policy/policy.go:62`), a deterministic fingerprint that **excludes `Weight`**
  (weight is per-upstream, not a host policy). Routes left with zero hosts are removed.
- **Same host, different source kind** (Ingress vs HTTPRoute) → resolved by ordering
  (last-writer-wins via `sourceKindPriority`), not by merging.

## Where it lands in Envoy

`applyRoutePolicy` (`pkg/envoy/ingress_translator.go:600`) is the **single place** a
`RoutePolicy` touches Envoy output, regardless of which Policy Source produced it
(timeouts, health-check path/host, retry-on, upstream HTTP version, connection limits,
sticky sessions). Weight is applied separately when adding upstreams (weight `0`
excludes an upstream). See [envoy-generation](envoy-generation.md) for the proto shapes.

## Adding a new traffic-behavior field

Because the model is shared, a new knob touches four places:

1. Add the field to `RoutePolicy` (`pkg/policy/policy.go:16`) and, if host-relevant,
   include it in `Signature()`.
2. Parse it in **both** `ParseAnnotations` and `ParseSpec`.
3. Apply it in `applyRoutePolicy` (`ingress_translator.go:600`).
4. If it maps to a new Envoy proto field, wire it in `pkg/envoy/boilerplate.go`.

Relevant tests: `pkg/policy/policy_test.go` (incl.
`TestSignatureEquivalenceAcrossSources`), `pkg/k8s/gateway_policy_test.go`,
`pkg/envoy/ingress_translator_test.go`.

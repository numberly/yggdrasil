# Yggdrasil — Quickstart

Yggdrasil is a standalone **Envoy control plane** written in Go. It watches
Kubernetes routing resources across **one or more clusters**, converts them into a
source-agnostic internal model, and serves Envoy xDS (v3) configuration to a fleet of
Envoy nodes over gRPC. The result is a multi-cluster load balancer that lives
**outside** Kubernetes — so it keeps routing even when a whole cluster is unavailable.

Supported Envoy versions: **1.20.x – 1.34.x** (see `README.md`).

## What it does, in one pipeline

```
Kubernetes (N clusters)                     Yggdrasil                         Envoy fleet
┌─────────────────────────┐   watch    ┌───────────────────────────┐  ADS/xDS  ┌──────────┐
│ Ingress                 │──────────▶ │ Aggregator  (pkg/k8s)     │           │ envoy #1 │
│ Gateway / HTTPRoute     │            │   → []SourceRoute          │  gRPC     │ envoy #2 │
│ Secrets (TLS)           │            │ Snapshotter (debounce 5s) │──────────▶│   ...    │
│ YggdrasilPolicy (CRD)   │            │ Configurator → xDS snapshot│  :8080    │          │
└─────────────────────────┘            └───────────────────────────┘           └──────────┘
```

1. **`Aggregator`** (`pkg/k8s/aggregator.go:56`) runs informers per cluster and
   coalesces every resource change into a signal event.
2. **`Snapshotter`** (`pkg/envoy/snapshotter.go:57`) debounces those events and, on a
   5-second tick, pulls the current `SourceRoute`s + TLS secrets.
3. **`Configurator`** (`pkg/envoy/configurator.go:119`) translates them into Envoy
   Listeners + Clusters and stores a versioned snapshot in a go-control-plane cache.
4. The **gRPC ADS/xDS server** (`cmd/server.go:55`) serves that snapshot to Envoy nodes
   keyed by node name.

## Repository layout

| Path | Role |
|------|------|
| `main.go`, `cmd/` | CLI entrypoint, config wiring, gRPC + health HTTP servers |
| `pkg/k8s/` | Multi-cluster watch, aggregation, Ingress + Gateway API → `SourceRoute` |
| `pkg/policy/` | Source-agnostic `RoutePolicy` model + annotation/CRD parsers |
| `pkg/apis/yggdrasil/v1alpha1/` | `YggdrasilPolicy` CRD Go types |
| `pkg/client/` | Generated clientset/informers/listers for the CRD (do not hand-edit) |
| `pkg/envoy/` | Internal model → Envoy xDS protos (listeners, clusters, filters) |
| `deploy/crds/` | `YggdrasilPolicy` CRD manifest |
| `docs/` | `GETTINGSTARTED.md` (install + RBAC), `ACCESSLOG.md` |

## Getting started

`docs/GETTINGSTARTED.md` is the hands-on walkthrough (Kubernetes RBAC, running Envoy +
Yggdrasil, verification). Envoy nodes need only a minimal bootstrap pointing LDS/CDS at
Yggdrasil's gRPC address; the full example lives in `README.md`. Envoy's
`--service-node` must equal Yggdrasil's `--node-name`.

Health-check your Envoy nodes at `/yggdrasil/status` — it returns 200 only once
Yggdrasil has configured them.

## Wiki map

- **[Architecture](architecture.md)** — the watch → model → xDS pipeline, the
  aggregator/snapshotter/configurator loop, and what is (and isn't) served.
- **[Routing & policy](routing-and-policy.md)** — the domain model (Source Route,
  Route Policy, Policy Source), Ingress-vs-HTTPRoute precedence, and how annotations
  and the `YggdrasilPolicy` CRD combine. Canonical glossary home.
- **[Kubernetes integration](kubernetes-integration.md)** — multi-cluster sources,
  informer selection, Gateway API opt-in, address discovery, TLS/ReferenceGrant,
  maintenance mode, RBAC.
- **[Envoy generation](envoy-generation.md)** — how the internal model becomes Envoy
  listeners, filter chains, clusters, TLS/SNI, health checks, retries, sticky sessions.
- **[Configuration & operations](configuration.md)** — CLI flags, config file,
  ports, metrics, build/Docker/CI, deployment model.

## Where to start when changing X

| You want to change… | Start in | Then check |
|---------------------|----------|------------|
| A traffic behavior (timeout, retry, sticky…) | `pkg/policy/` shared model | [routing-and-policy](routing-and-policy.md) |
| How a resource is watched / discovered | `pkg/k8s/aggregator.go`, `gateway_resources.go` | [kubernetes-integration](kubernetes-integration.md) |
| The Envoy proto output | `pkg/envoy/boilerplate.go` | [envoy-generation](envoy-generation.md) |
| A CLI flag or config field | `cmd/root.go` | [configuration](configuration.md) |

> **Convention (see `AGENTS.md`):** follow the data from Kubernetes input → internal
> model → Envoy output, and prefer fixing the shared conversion/model layer over
> patching a single caller. Read `CONTEXT.md` before touching routing/policy/Gateway
> behavior — its terms are canonical across code, tests, and docs.

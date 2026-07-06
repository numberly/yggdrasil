# Architecture

Yggdrasil is a pipeline with one direction of flow, stated in `AGENTS.md`:

> Kubernetes input → normalized internal model → Envoy output.

Everything below is an elaboration of that sentence. When changing behavior, follow
the data along this path and prefer fixing the shared model layer over a single caller.

## The three stages

### 1. Aggregator — watch & coalesce (`pkg/k8s/aggregator.go`)

`NewAggregator` (`aggregator.go:56`) builds, **per cluster**, a set of shared informers
(Ingress, optionally Gateway API + `YggdrasilPolicy`, optionally Secrets) and blocks on
`cache.WaitForCacheSync` before returning. Details of informer selection and Gateway API
opt-in live in [kubernetes-integration](kubernetes-integration.md).

Informer callbacks do **not** carry payloads — they only push a typed signal onto a
buffered channel (`pkg/k8s/handlers.go`):

- `EventsIngresses` → `SyncType: INGRESS` (Ingress **and** all Gateway/Policy informers)
- `EventsSecrets` → `SyncType: SECRET`
- `Aggregator.Run` (`aggregator.go:181`) ticks a `COMMAND` event every 5 s.

Only three `SyncType` values exist (`pkg/k8s/types.go:34`): `COMMAND`, `INGRESS`,
`SECRET`. Gateway API and CRD changes are deliberately folded under `INGRESS`.

### 2. Snapshotter — debounce & trigger (`pkg/envoy/snapshotter.go`)

`Snapshotter.Run` (`snapshotter.go:57`) reads the event channel:

- `INGRESS` / `SECRET` set an internal `hadChanges` flag (no work done yet).
- the periodic `COMMAND` tick is what actually calls `snapshot()` — **but only if
  `hadChanges` is set**.

This is a **coalescing/debounce** design: a storm of resource events collapses into at
most one snapshot every 5 seconds. `snapshot()` (`snapshotter.go:33`) pulls
`aggregator.GetSourceRoutes()` + `GetSecrets()`, calls `Configurator.Generate(...)`, and
installs the result with `SetSnapshot`. The `Configurator` dependency is an interface
(`snapshotter.go`), so the snapshotter is decoupled from Envoy specifics.

### 3. Configurator — translate & version (`pkg/envoy/configurator.go`)

`KubernetesConfigurator.Generate(ingresses, secrets)` (`configurator.go:119`) is the
heart of xDS generation and is **mutex-guarded** (it holds `previousConfig` and version
counters). It:

1. Filters `SourceRoute`s by ingress class (`classFilter`) and validity
   (`validIngressFilter`) — HTTPRoutes bypass class filtering.
2. Calls `translateIngresses` (`pkg/envoy/ingress_translator.go:462`) to build the
   intermediate `envoyConfiguration` (virtual hosts + clusters).
3. **Diffs against `previousConfig`** and bumps `listenerVersion` / `clusterVersion`
   (each a `time.Now().String()`) **only for the resource type that changed** — so Envoy
   re-syncs listeners and clusters independently.
4. Packs Listeners + Clusters into a go-control-plane `cache.Snapshot` and updates
   Prometheus counters.

## What is served (and what is not)

This is the single most load-bearing fact about the config model:

- **Served:** Listeners (LDS) and Clusters (CDS) only.
- **Routes are inlined** into each HTTP Connection Manager's `RouteConfig` — there is
  **no RDS**.
- Clusters are **`STRICT_DNS`** with inline LB endpoints — there is **no EDS**.

So a "route change" surfaces as a **listener** update, not a route update. See
[envoy-generation](envoy-generation.md) for how the protos are built.

The snapshot is stored in a `SnapshotCache` keyed by **NodeID** (`--node-name`); the
gRPC ADS/xDS server (`cmd/server.go:55`) streams it to every Envoy whose `--service-node`
matches. Server wiring, ports, and callbacks are covered in
[configuration](configuration.md).

## Relevant tests

- `pkg/envoy/configurator_test.go` — end-to-end `Generate` behavior, versioning.
- `pkg/envoy/ingress_translator_test.go` — the model-building core (host merging,
  precedence, maintenance, policy application).
- `pkg/k8s/aggregator.go` has no dedicated aggregator test; informer/conversion logic is
  covered by `pkg/k8s/generic_resources_test.go`, `gateway_resources_test.go`,
  `gateway_policy_test.go`.

Run everything with `make test`.

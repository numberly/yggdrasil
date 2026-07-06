# Configuration & Operations

## CLI, flags, and config file

Startup chain: `main.go` → `cmd.Execute()` → Cobra `rootCmd` → `main`
(`cmd/root.go:173`). Configuration uses **Cobra + Viper**: flags are defined on
`PersistentFlags` (`cmd/root.go:82`) and each is bound with `viper.BindPFlag`
(`cmd/root.go:122`) using dotted keys (e.g. `httpExtAuthz.timeout`) so a nested config
file and the flags share one keyspace. `--config` (JSON **or** YAML) is read only when
set (`initConfig`, `cmd/root.go:161`); everything is then `viper.Unmarshal`-ed into the
`config` struct (`cmd/root.go:35`).

The full flag reference with defaults lives in **`README.md`** (the canonical list) — do
not duplicate it here. Notable groups:

- **Listen addresses**: `--address` (xDS gRPC, default `0.0.0.0:8080`),
  `--health-address` (HTTP, default `0.0.0.0:8081`).
- **Identity**: `--node-name` — must equal each Envoy's `--service-node`.
- **Envoy data plane**: `--envoy-listener-ipv4-address` (default `0.0.0.0`),
  `--envoy-port` (default `10000`), `--upstream-port` (default `443`).
- **Downstream TLS**: `--cert` / `--key` (required together), `--ca` (viper key
  `trustCA`), `--alpn-protocols`.
- **Watching**: `--ingress-classes`, `--kube-config` (repeatable).
- **Defaults & tuning**: `--default-route-timeout` (15s),
  `--default-cluster-timeout` (30s), `--default-per-try-timeout` (5s), `--retry-on`
  (`5xx`, validated at startup), `--max-ejection-percentage` (-1 = off),
  `--host-selection-retry-attempts` (-1 = off), the `--upstream-healthcheck-*` set,
  `--use-remote-address`.
- **Filters/loggers**: `--http-ext-authz-*`, `--http-grpc-logger-*`, `--tracing-provider`
  (only zipkin supported), `--config-dump`, `--debug`.

**Config-file-only** fields (no flag): `certificates[]`, `clusters[]`, `syncSecrets`,
and `accessLogger.format` (the access-log field map — see `docs/ACCESSLOG.md`).

### Certificates & TLS guards

`checkDownStreamTLSSetup` (`cmd/root.go`) requires `--cert` and `--key` together. If
`certificates` is empty but `--cert`/`--key` are set, a wildcard `{Hosts:["*"]}`
certificate is synthesized; cert/key file contents are read and inlined into the served
config. Guard: `syncSecrets: true` allows **only one** certificate (the fallback for
hosts with a missing/invalid secret).

### Multi-cluster `clusters[]`

Each `clusterConfig` (`cmd/root.go:25`): `apiServer` + `token`/`tokenPath` + `ca`,
optional `maintenance`, `kubernetesClusterName` (metrics label), and `gatewayClasses`
(enables Gateway API for that cluster). See
[kubernetes-integration](kubernetes-integration.md).

## Servers & ports

`runEnvoyServer` (`cmd/server.go:55`) runs two servers:

- **gRPC xDS/ADS** on `--address` (8080): registers ADS, EDS, CDS, RDS, LDS services with
  Prometheus interceptors. Envoy nodes connect here. A `callbacks` struct
  (`cmd/server.go:24`) counts fetch requests; graceful shutdown via `GracefulStop`.
  (Note: although all five xDS services are registered, only Listeners and Clusters carry
  data — see [architecture](architecture.md).)
- **Health/metrics HTTP** on `--health-address` (8081): `GET /metrics`
  (`cmd/server.go:80`, promhttp), `GET /healthz` (always 200), and `GET /configdump`
  (registered only with `--config-dump`; returns the current snapshot as JSON via
  `Snapshotter.ConfigDump`).

## Metrics

Prometheus metrics are exposed at `/metrics` on the health address. The Yggdrasil-specific
gauges/counters (`yggdrasil_cluster_updates`, `yggdrasil_clusters`, `yggdrasil_ingresses`,
`yggdrasil_listener_updates`, `yggdrasil_virtual_hosts`,
`yggdrasil_kubernetes_cluster_in_maintenance`, `yggdrasil_upstream_info`) are registered
in `pkg/envoy/metrics.go`; the full table is in `README.md`.

## Build, test, CI

- **Makefile**: `make test` (`go test -v -cover`, excludes vendor), `make bench`,
  `make build-linux` / `build-darwin` (static, `CGO_ENABLED=0`), `make docker`,
  `make clean`. No lint target.
- **Dockerfile**: `FROM scratch`, copies the static `bin/yggdrasil-linux-amd64` as
  `/yggdrasil`. Minimal, statically linked image.
- **CI** (`.github/workflows/push.yaml`, on push): `test` → `build` (uploads `bin/`
  artifact) → `docker-build-push` (only on `master` or `v*` tags; pushes
  `quay.io/uswitch/yggdrasil`). Go version is read from `go.mod` (**Go 1.25**).

Per `AGENTS.md`: run `make test` after any change to Go code, `go.mod`/`go.sum`, or
behavior-affecting config, and update `README.md`/`docs/` in the same change when public
behavior (flags, annotations, CRDs, config semantics) changes.

## Deployment model

Yggdrasil runs as a standalone control-plane process **outside** the data plane. A fleet
of Envoy nodes connects to its gRPC address and pulls dynamic listeners/clusters over
ADS. Distributed as `quay.io/uswitch/yggdrasil` (scratch image). See `README.md` for the
minimal Envoy bootstrap and architecture diagrams, and `docs/GETTINGSTARTED.md` for a
full walkthrough.

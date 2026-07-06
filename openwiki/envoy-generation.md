# Envoy Generation

How the internal model becomes Envoy xDS protos. The entrypoint is
`KubernetesConfigurator.Generate` (`pkg/envoy/configurator.go:119`, see
[architecture](architecture.md)); this page covers the two layers below it:
`ingress_translator.go` (model) and `boilerplate.go` (protos).

## Intermediate model (`pkg/envoy/ingress_translator.go`)

`translateIngresses` (`ingress_translator.go:462`) turns `[]*k8s.SourceRoute` into an
`envoyConfiguration{VirtualHosts, Clusters, AccessLog}`:

- **`virtualHost`** — host, timeouts, TLS key/cert, `RetryOn`, sticky-session cookie
  fields.
- **`cluster`** — LB hosts (with weights), health-check host/path, HTTP version,
  timeouts, idle / max-connection-duration / max-requests-per-connection, sticky
  `StickySessionChangeOnFailure`.

`translateIngresses` groups source routes by rule host, applies Ingress-vs-HTTPRoute
precedence and maintenance filtering (see [routing-and-policy](routing-and-policy.md)),
dedupes upstreams (`addUpstream` — weight `0` excludes an upstream), resolves per-host
TLS secrets when `syncSecrets`, and calls `applyRoutePolicy`
(`ingress_translator.go:600`) — the single point where a `RoutePolicy` maps onto the
vhost/cluster. It also maintains the `yggdrasil_upstream_info` metric with stale-label
cleanup.

## Listeners & filter chains (`configurator.go` + `boilerplate.go`)

`generateListeners` (`configurator.go:194`) picks **one of three** filter-chain
strategies, then wraps them in `makeListener`:

1. **Dynamic TLS** (`syncSecrets`): one filter chain per virtual host, cert from a
   Kubernetes Secret, SNI matched on the vhost host; a single default cert
   (`Hosts:["*"]`) covers hosts with a missing/invalid secret.
2. **Static TLS** (`certificates` configured): vhosts are matched to configured certs
   (`*` wildcard per label, or global `*`), one filter chain per certificate keyed by
   SNI `ServerNames`.
3. **Plain HTTP**: no TLS.

`makeFilterChain` (`boilerplate.go:356`) wraps the HTTP Connection Manager plus a
`DownstreamTlsContext` (inline cert/key, ALPN, **TLS min version 1.2**) and sets
`FilterChainMatch.ServerNames` for SNI (skipping `*`). `makeListener`
(`boilerplate.go:418`) binds `listener_0` to the configured IPv4 address(es), adds the
**tls_inspector** listener filter, and marks the listener `TrafficDirection_OUTBOUND`
(needed for tracing).

## The HTTP route & connection manager

`makeVirtualHost` (`boilerplate.go:79`) builds a single catch-all `Prefix:"/"` route with
a `RouteAction` (cluster, route `Timeout`, `RetryPolicy` = `RetryOn` + `PerTryTimeout`).
Extras:

- **Host reselection on retry**: when `reselectionAttempts >= 0`, adds a `previous_hosts`
  retry-host predicate + `HostSelectionRetryMaxAttempts`.
- **Sticky sessions**: when the vhost is sticky, attaches a per-route
  `StatefulSessionPerRoute` with cookie-based session state.

`makeConnectionManager` (`boilerplate.go:258`) assembles the HCM: a file access logger
(JSON; see `docs/ACCESSLOG.md`), an optional gRPC access logger, the HTTP filter chain,
optional Zipkin tracing, websocket upgrade, `UseRemoteAddress`, and
`StripMatchingHostPort`. Routes are embedded inline (no RDS).

### HTTP filter chain (`pkg/envoy/http_filters.go`)

`httpFilterBuilder` composes filters and **always appends the router filter last**
(`http_filters.go:35`). Order:

```
health_check  →  [ext_authz]  →  [stateful_session]  →  router
```

The local health filter (`makeHealthConfig`, `boilerplate.go:165`) serves Envoy's
`/yggdrasil/status` check. `makeStatefulSessionFilter` builds the top-level
stateful-session filter that the per-route config references.

## Clusters (`makeCluster`, `boilerplate.go:518`)

Each intermediate `cluster` becomes a **`STRICT_DNS`** Envoy cluster with:

- inline LB endpoints + weights, connect timeout;
- optional **upstream TLS** (via `trustCA`);
- **health checks** (`makeHealthChecks`, `boilerplate.go:496`) — HTTP check on the
  configured host + path;
- **HTTP protocol options** — HTTP/1.1 vs HTTP/2 (default h2 with AllowConnect,
  MaxConcurrentStreams 128); common options idle_timeout 60s default,
  max_connection_duration 0, max_requests_per_connection 10000;
- **circuit breakers** (all thresholds 32768);
- **outlier detection** (`MaxEjectionPercent`) when `--max-ejection-percentage >= 0`;
- **sticky-session `OverrideHostStatus`** (persist to an unhealthy backend) when
  `StickySessionChangeOnFailure == false`.

## Validation helper

`ValidateEnvoyRetryOn` (`pkg/envoy/boilerplate.go`) checks retry-on values against the
allowed set; it is also used to validate the `--retry-on` flag at startup
(`cmd/root.go`).

## Relevant tests

`pkg/envoy/boilerplate_test.go`, `pkg/envoy/ingress_translator_test.go`,
`pkg/envoy/configurator_test.go`. Run with `make test`; benchmark with `make bench`.

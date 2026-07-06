# Kubernetes Integration

How Yggdrasil discovers routing across clusters and turns raw Kubernetes objects into
`SourceRoute`s. For the resulting policy model see
[routing-and-policy](routing-and-policy.md); for how routes become Envoy config see
[envoy-generation](envoy-generation.md).

## Multi-cluster sources

Two independent mechanisms produce clusters to watch, merged in `main`
(`cmd/root.go:206`):

1. **Config-file `clusters[]`** → `createSources` (`cmd/root.go:298`). Each entry builds
   a `rest.Config` from `apiServer` + bearer token (`token` inline or `tokenPath`) +
   `ca`, and a `k8s.KubernetesConfig` carrying the clientset, a `maintenance` flag, a
   `kubernetesClusterName` (metrics label), and per-cluster `gatewayClasses`.
2. **`--kube-config` paths** → `configFromKubeConfig` (`cmd/root.go:348`). An empty path
   means in-cluster config; otherwise `clientcmd.BuildConfigFromFlags`.

Both feed `k8s.NewAggregator(sources, ctx, syncSecrets)` (`cmd/root.go:243`).

## Informer selection & graceful degradation

`NewAggregator` (`pkg/k8s/aggregator.go:56`) is deliberately defensive — a missing API
group logs a warning and continues rather than failing:

- **Ingress** — `getIngressInformer` (`aggregator.go:152`) probes API groups in order:
  `networking.k8s.io/v1` → `v1beta1` → `extensions/v1beta1`, first served wins.
- **Gateway API — opt-in per cluster** (`aggregator.go:82`): watched only when that
  cluster has `gatewayClasses` configured **and** the server serves
  `gateway.networking.k8s.io/v1`. It then watches `GatewayClasses`, `Gateways`,
  `HTTPRoutes`, `ReferenceGrants`, plus core `Services` and `Namespaces`.
- **`YggdrasilPolicy` — nested inside the Gateway block** (`aggregator.go:116`): watched
  only if `yggdrasil.uswitch.com/v1alpha1` is served; otherwise a warning is logged and
  "HTTPRoute policies fall back to annotations".
- **Secrets** (`aggregator.go:132`): only when `syncSecrets`, using a dedicated factory
  with a `type=kubernetes.io/tls` field-selector so the filter applies only to secrets.

All informer callbacks emit signal-only events (see [architecture](architecture.md)).

## Gateway API → `SourceRoute`

`ConvertGatewayResources` (`pkg/k8s/gateway_resources.go:47`) walks each HTTPRoute ×
configured class × matching Gateway × listener and emits a route only when both hold:

- **Attachment** — `httpRouteAttachesToListener` (`gateway_resources.go:165`): parentRef
  group/kind/name/namespace/sectionName must match, and the listener's
  `AllowedRoutes.Namespaces` (Same / All / Selector; default Same) must permit the
  route's namespace (`listenerAllowsRouteNamespace`).
- **Host matching** — `matchingHTTPRouteHosts` (`gateway_resources.go:227`): intersects
  route hostnames with the listener hostname (wildcard aware); if the route lists none,
  it inherits the listener hostname.

### Gateway address discovery (upstream resolution precedence)

`resolveGatewayUpstreams` (`gateway_resources.go:262`) picks the data-plane endpoints
for a Gateway-attached route, first match wins:

1. `Gateway.status.addresses` + the listener port.
2. A **Service owned** by the Gateway via `OwnerReference` (`isOwnedByGateway`).
3. Static `serviceName` / `serviceNamespace` from the cluster's `gatewayClasses` config.

Service-backed endpoints require a `ServicePort` equal to the listener port and pull
ExternalIPs + LoadBalancer ingress. If nothing resolves, the route is **skipped** with a
diagnostic (never fatal). This order matches the `docs/GETTINGSTARTED.md` "Gateway
address discovery" section.

### TLS secret resolution & cross-namespace refs

`gatewayListenerTLS` (`gateway_resources.go:409`) uses **only the first**
`CertificateRefs` entry (extra refs → diagnostic). The secret namespace defaults to the
Gateway's namespace unless the ref specifies one. A **cross-namespace** secret ref is
permitted only by a matching **ReferenceGrant** (`isCrossNamespaceSecretRefPermitted`,
`gateway_resources.go:365`); non-core or grant-less refs become diagnostics and skip the
route.

Diagnostics collected during conversion are surfaced as `logrus.Warn` in
`GetSourceRoutes` (`generic_resources.go`) — they degrade a single route, never the run.

## Ingress → `SourceRoute`

`convertToGenericIngress` (`generic_resources.go:77`) type-switches the three Ingress API
versions. Upstreams come from `Status.LoadBalancer.Ingress` (hostname preferred over IP).
TLS is a `map[host]*IngressTLS` from `Spec.TLS`, with the secret namespace = the
Ingress's own namespace. Each route then gets `attachAnnotationPolicy`
(`generic_resources.go:106`). Ingress class resolution: `getUsableIngressClass`
(`generic_resources.go`) — the `kubernetes.io/ingress.class` annotation beats
`spec.ingressClassName`.

## Maintenance mode

A cluster flagged `maintenance` in `clusters[]` keeps serving only where it is the
**sole** backend: `translateIngresses` skips a maintenance cluster's upstreams for a host
whenever a non-maintenance backend exists for that host (`hasNonMaintenance`,
`pkg/envoy/ingress_translator.go`). Yggdrasil `Fatal`s if **all** clusters are in
maintenance (`cmd/root.go`). Exposed via the `yggdrasil_kubernetes_cluster_in_maintenance`
metric.

## RBAC

`docs/GETTINGSTARTED.md` has the full ClusterRole. In short, get/list/watch on:
`ingresses` (extensions + networking.k8s.io); `gatewayclasses`, `gateways`, `httproutes`,
`referencegrants` (gateway.networking.k8s.io); `yggdrasilpolicies`
(yggdrasil.uswitch.com); `services`, `namespaces` (core); and, when `syncSecrets`,
`secrets`.

## Caveat

`README.md` (line 5) links to `hack/local-gateway-test/README.md` for a local Gateway
API test setup, but the `hack/` directory is currently **absent** from the repository.
Treat that link as aspirational until the fixture is added.

## Relevant tests

`pkg/k8s/generic_resources_test.go`, `gateway_resources_test.go`,
`gateway_policy_test.go`.

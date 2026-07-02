# Agent instructions

## Project overview

- Yggdrasil is a Go-based Envoy control plane.
- It watches Kubernetes routing resources across one or more clusters.
- It generates Envoy xDS configuration for listeners, routes, clusters, endpoints, TLS, health checks, retries, timeouts, access logs, and related traffic behavior.
- The project historically uses Kubernetes Ingresses plus Yggdrasil annotations.
- The project is also adding Gateway API support, especially HTTPRoute and YggdrasilPolicy.
- Treat Kubernetes objects as inputs that are converted into Yggdrasil's internal routing and policy model before Envoy configuration is produced.
- Important flow:
  - Kubernetes resources are watched and aggregated in `pkg/k8s`.
  - Source routes and policy sources are normalized into internal models.
  - `pkg/envoy` converts those models into Envoy xDS snapshots.
  - `cmd` wires configuration, Kubernetes clients, the aggregator, the snapshotter, and the gRPC/health servers.
- When changing behavior, follow the data from Kubernetes input to internal model to Envoy output.
- Prefer fixing the shared conversion/model layer over patching one caller.

## Keep it small

- Make the smallest correct change.
- Reuse existing code and patterns before adding new helpers or abstractions.

## Domain language

- Read `CONTEXT.md` before changing routing, policy, Gateway API, Kubernetes, or Envoy behavior.
- Use `CONTEXT.md` canonical terms in code, tests, docs, and PR text.

## Living document

- When the user corrects an agent, points out a repeated mistake, or contradicts an instruction, update this file with the smallest rule that prevents the same mistake in future sessions.

## Go style

- Keep code self-explanatory.
- Do not add comments unless required for exported Go API documentation or generated-code/tooling directives.
- Do not add a new dependency without explicit approval.
- Prefer the standard library and existing dependencies.

## Tests

- Run `make test` after any change to Go code, `go.mod`, `go.sum`, or behavior-affecting configuration.

## Documentation

- Update `README.md` or `docs/` in the same change when public behavior, flags, annotations, CRDs, or configuration semantics change.

## Git

- Do not commit, push, or open a pull request unless explicitly asked.

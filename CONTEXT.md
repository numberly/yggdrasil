# Yggdrasil Context

## Routing Configuration

**Source Route**: A Kubernetes routing object that Yggdrasil accepts as an input for Envoy configuration. Current source routes are **Ingress** and **HTTPRoute**. _Avoid_: Generic ingress, route object.

**Route Policy**: Yggdrasil-specific traffic behavior attached to a **Source Route**, such as timeouts, health checks, retries, upstream behavior, connection limits, and sticky sessions. _Avoid_: Annotation config, CRD config.

**Policy Source**: The external Kubernetes representation from which a **Route Policy** is derived. Current policy sources are legacy Yggdrasil annotations and **YggdrasilPolicy**. _Avoid_: Policy backend.

**Effective Route Policy**: The single **Route Policy** Yggdrasil applies to a **Source Route** after resolving policy source precedence, fallback, and conflicts. _Avoid_: Merged annotations, final annotations.

**YggdrasilPolicy**: A Kubernetes custom resource that represents a typed **Policy Source** for an **HTTPRoute**. A **YggdrasilPolicy** is not itself the internal **Route Policy**.

**Gateway Address**: The data-plane address and port at which a Gateway implementation is reachable, used by Yggdrasil as upstream endpoints for **Source Routes** attached to that Gateway. Discovered from the cluster rather than statically configured. _Avoid_: Gateway service, upstream service.

## Relationships

An **Ingress** is a **Source Route** whose **Route Policy** may come from legacy Yggdrasil annotations.

An **HTTPRoute** is a **Source Route** whose **Route Policy** may come from a **YggdrasilPolicy** or, during migration, legacy Yggdrasil annotations.

A **Source Route** has at most one **Effective Route Policy**.

A **Policy Source** produces zero or one **Route Policy**.

## Resolved Language

**Dev:** "Should the CRD be converted into annotations?"

**Domain expert:** "No. Annotations and the CRD are policy sources. Both should be parsed into the same Route Policy model."

**Dev:** "Is YggdrasilPolicy the shared model?"

**Domain expert:** "No. YggdrasilPolicy is the Kubernetes custom resource. Route Policy is the shared domain model."

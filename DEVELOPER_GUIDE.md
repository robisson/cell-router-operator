# Developer Guide

## Purpose

This guide explains how the operator works internally and how to extend it safely.

The project manages cell-oriented routing on Kubernetes by separating the problem into two custom resources:

- `Cell`: namespace and entrypoint service lifecycle
- `CellRouter`: Gateway API routing lifecycle

The key design principle is simple:

- `Cell` owns the internal service boundary for a workload.
- `CellRouter` owns the external HTTP routing layer that targets those services.

That separation keeps service discovery concerns and traffic routing concerns independent.

## Core Concepts

### Cell

A `Cell` is the operator's abstraction for an isolated workload domain.

It is cluster-scoped, but it manages namespaced resources:

- a namespace
- an entrypoint service inside that namespace

The `Cell` spec tells the operator:

- what namespace to use
- how to label/annotate the namespace
- how to select the backing pods
- what service name and port to expose
- whether the namespace should be deleted when the CR is deleted

### CellRouter

A `CellRouter` is the routing abstraction.

It manages:

- one `Gateway`
- one `HTTPRoute` per route entry in `.spec.routes`
- one `ReferenceGrant` per route when the backend service is in a different namespace than the route
- the namespace that hosts the gateway resources

Each route entry maps traffic conditions to a target cell.

Supported route criteria today:

- hostnames
- path match
- header matches
- query parameter matches
- weight
- listener binding

## API Model

The main API types live in:

- [api/v1alpha1/cell_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cell_types.go)
- [api/v1alpha1/cellrouter_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cellrouter_types.go)

### Cell Spec Highlights

Important fields:

- `spec.namespace`
- `spec.entrypoint.serviceName`
- `spec.entrypoint.port`
- `spec.entrypoint.targetPort`
- `spec.entrypoint.type`
- `spec.workloadSelector`
- `spec.namespaceLabels`
- `spec.namespaceAnnotations`
- `spec.serviceLabels`
- `spec.serviceAnnotations`
- `spec.tearDownOnDelete`

Status fields:

- `status.namespace`
- `status.entrypointService`
- `status.observedGeneration`
- `status.conditions`

Conditions:

- `NamespaceReady`
- `ServiceReady`
- `Ready`

### CellRouter Spec Highlights

Important gateway fields:

- `spec.gateway.name`
- `spec.gateway.namespace`
- `spec.gateway.gatewayClassName`
- `spec.gateway.listeners`

Important route fields:

- `spec.routes[].name`
- `spec.routes[].cellRef`
- `spec.routes[].listenerNames`
- `spec.routes[].hostnames`
- `spec.routes[].pathMatch`
- `spec.routes[].headerMatches`
- `spec.routes[].queryParamMatches`
- `spec.routes[].weight`

Status fields:

- `status.managedGatewayRef`
- `status.managedRoutes`
- `status.observedGeneration`
- `status.conditions`

Conditions:

- `GatewayReady`
- `RoutesReady`
- `Ready`

## Runtime Wiring

The manager is bootstrapped in [cmd/main.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/cmd/main.go).

Key responsibilities there:

- register API schemes
- register Gateway API types
- configure metrics and probes
- configure leader election
- create the controller manager
- wire `CellReconciler`
- wire `CellRouterReconciler`

The manager intentionally includes both `gateway.networking.k8s.io/v1` and `v1beta1` because the operator works with:

- `Gateway` and `HTTPRoute` from `v1`
- `ReferenceGrant` from `v1beta1`

## Labels and Finalizers

Shared constants live in [internal/constants/constants.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/constants/constants.go).

Important labels:

- `cellrouter.io/managed-by`
- `cellrouter.io/cell-name`
- `cellrouter.io/entrypoint-service`
- `cellrouter.io/router-name`

Important finalizers:

- `cell.cellrouter.io/finalizer`
- `cellrouter.cellrouter.io/finalizer`

These are central to cleanup logic and should not be changed lightly once resources exist in clusters.

## Resource Builder Pattern

The operator keeps object mutation logic in builders instead of embedding raw spec assignment into reconcilers.

Files:

- [internal/resource/cell/builder.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/resource/cell/builder.go)
- [internal/resource/router/builder.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/resource/router/builder.go)

This gives a few benefits:

- reconcilers stay focused on orchestration
- mutation logic becomes testable in isolation
- `CreateOrUpdate` closures stay small and deterministic

### Cell builders

`MutateNamespace`:

- applies operator labels
- applies user-provided namespace labels/annotations
- protects operator-owned labels from being overwritten

`MutateService`:

- applies labels and annotations
- sets the selector from `workloadSelector`
- defaults to `cellrouter.io/cell-name=<cell-name>` if no selector is provided
- sets service type, protocol, port, and target port

### Router builders

`MutateGatewayNamespace`:

- labels the namespace used for gateway resources

`MutateGateway`:

- applies labels/annotations
- copies gateway class and listeners
- sorts listeners for stable reconciliation

`MutateReferenceGrant`:

- allows `HTTPRoute` resources from the gateway namespace to reference a service in the target cell namespace

`MutateHTTPRoute`:

- sets labels
- binds parent refs to the managed gateway
- builds matches from path/header/query config
- points backend refs at the cell entrypoint service

## Cell Reconciler

Implementation:

- [internal/controller/cell_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cell_controller.go)

### Reconciliation flow

Normal path:

1. Load the `Cell`
2. Exit if it no longer exists
3. If deletion timestamp is set, run finalization
4. Ensure the cell finalizer exists
5. Reconcile namespace
6. Reconcile entrypoint service
7. Update status and conditions

Deletion path:

1. Delete the managed entrypoint service if the cell owns it
2. Delete the namespace only if `tearDownOnDelete` is true and the namespace is clearly managed by the operator for that cell
3. Remove the finalizer

### Effective namespace

The helper `effectiveNamespace(cell)` is important:

- if `spec.namespace` is set, use it
- otherwise default to the cell name

This means the CR name is not forced to equal the namespace, but it often will by default.

### Ownership model

The service is set with a controller reference to the `Cell`.

The namespace is not owned via owner reference because namespaces have special lifecycle semantics and are cluster-scoped. Instead, the operator uses labels to determine whether it is safe to delete the namespace.

## CellRouter Reconciler

Implementation:

- [internal/controller/cellrouter_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cellrouter_controller.go)

### Reconciliation flow

Normal path:

1. Load the `CellRouter`
2. Exit if it no longer exists
3. If deletion timestamp is set, run finalization
4. Ensure the router finalizer exists
5. Reconcile the gateway namespace
6. Reconcile the gateway
7. Reconcile all routes
8. Clean up stale routes and stale reference grants
9. Update router status

Deletion path:

1. Delete managed `HTTPRoute` resources
2. Delete managed `ReferenceGrant` resources
3. Delete the managed `Gateway`
4. Remove the finalizer

### Why the gateway namespace is reconciled explicitly

The router spec includes a gateway namespace.

That namespace may not exist before the router is created, so the operator creates and labels it first. Without that step, the gateway create/update would fail on a fresh cluster.

### Route reconciliation details

For each route:

1. Load the referenced `Cell`
2. Resolve the backend service from the cell status/spec
3. Create or update the `ReferenceGrant`
4. Create or update the `HTTPRoute`
5. Inspect route readiness from Gateway API status
6. Add route summary to `status.managedRoutes`

The router only becomes fully ready when:

- the gateway step succeeded
- all target cells are ready
- all routes are accepted
- `ResolvedRefs` is true when present

### Backend resolution

Backend resolution intentionally depends on the `Cell` API contract:

- namespace comes from `status.namespace` or the effective namespace fallback
- service name comes from `spec.entrypoint.serviceName`
- port comes from `spec.entrypoint.port`

This is a useful extension seam if you later want to support:

- named ports
- multiple services per cell
- backend policy objects
- traffic splitting to multiple services

## Cross-Namespace Routing and ReferenceGrant

Gateway API requires explicit permission when an `HTTPRoute` in one namespace targets a `Service` in another namespace.

That is why the operator creates one `ReferenceGrant` per route in the target cell namespace.

Without that object:

- the `HTTPRoute` may exist
- but the backend reference will not resolve
- and routing will fail at the data plane level

This was one of the critical correctness fixes in the project.

## Status and Readiness Semantics

The operator uses status conditions as the contract between:

- spec intent
- reconcile progress
- human/debugger visibility
- local smoke tests

### Cell readiness

A cell is marked ready only after:

- namespace reconciliation succeeds
- service reconciliation succeeds

### Router readiness

A router is marked ready only after:

- gateway reconciliation succeeds
- every route is accepted
- every route has resolved backend references
- referenced cells are themselves ready

That means router readiness is not just "objects were created". It reflects whether the routing layer is actually configured enough to serve traffic as declared.

## Why `Gateway Programmed=False` Can Still Be Okay in Kind

In local Kind setups with Envoy Gateway, the top-level gateway `Programmed` condition may remain false because no external address is assigned to the load balancer service.

That does not necessarily mean routing is broken.

What matters for this project's local correctness is:

- `Gateway Accepted=True`
- listener conditions are accepted/programmed/resolved
- `HTTPRoute Accepted=True`
- `HTTPRoute ResolvedRefs=True`
- real traffic reaches the backend

That is why the local script validates traffic with `curl` instead of relying only on one top-level gateway condition.

## Local End-to-End Flow

The local flow is implemented in [scripts/run-local.sh](/Users/robisson/projetcs/golang/k8s/cell-router-operator/scripts/run-local.sh).

The script:

1. Creates or reuses a Kind cluster
2. Waits for nodes to become ready
3. Installs Gateway API CRDs
4. Installs Envoy Gateway via Helm
5. Applies a local `GatewayClass`
6. Runs unit tests in a Go container
7. Builds the operator image
8. Loads the image into Kind
9. Applies CRDs
10. Deploys the controller
11. Applies sample cells and workloads
12. Applies the sample router
13. Waits for route readiness
14. Sends real traffic through Envoy and validates the responses

The current smoke test covers two cells:

- `payments`
- `orders`

That is important because it validates actual cell-to-cell route selection rather than only existence of one route.

## Current Example Routing Topology

The sample router currently models:

- `payments.example.com` + `/payments` + specific header/query -> `payments`
- `orders.example.com` + `/orders` -> `orders`

This is useful because it exercises:

- host matching
- path matching
- header matching
- query parameter matching
- cross-namespace backend resolution
- multi-cell routing in one gateway

## Testing Strategy

### Unit tests

The main automated safety net is the unit test suite:

- API deep copy tests
- `CellReconciler` tests
- `CellRouterReconciler` tests
- builder tests
- metadata utility tests

Good unit test targets for future changes:

- route cleanup behavior
- condition transitions
- multiple listener scenarios
- weighted traffic generation
- optional TLS listener behavior

### End-to-end testing

The repository does not yet have a full e2e suite under `test/e2e` that covers the cell-router behavior deeply.

The local script is the current practical e2e harness.

If you extend the project significantly, it is worth adding:

- automated Kind-based e2e tests
- assertions on route status
- assertions on actual HTTP behavior
- cleanup validation

## Common Extension Scenarios

### 1. Add new route match types

Example ideas:

- method matching
- header regex matching when supported by the Gateway implementation
- cookie-based routing via filters or custom abstractions

Where to change:

- API type definitions in `api/v1alpha1/cellrouter_types.go`
- route builder logic in `internal/resource/router/builder.go`
- unit tests in `internal/resource/router/builder_test.go`
- reconciliation tests in `internal/controller/cellrouter_controller_test.go`
- sample manifests

### 2. Add traffic splitting across multiple backends

Today one route targets one cell service.

To support real canaries or multi-cell split traffic, you would likely change:

- the route spec model to accept multiple backend targets
- backend resolution to return multiple services
- route builder to emit multiple backend refs with weights
- readiness logic to require all backend refs to resolve

### 3. Support TLS listeners in local flows

The API already allows listener TLS configuration.

To make TLS fully usable end-to-end you would need:

- certificate secret management
- local sample secrets
- possibly additional `ReferenceGrant` behavior depending on certificate refs
- curl or HTTPS verification in `run-local.sh`

### 4. Add policy resources

Natural future additions:

- rate limiting
- authn/authz
- request/response header mutation
- retries/timeouts
- backend traffic policies

Recommended approach:

- keep the public CRD small and stable
- translate from your higher-level API to Gateway API and controller-specific policy CRs
- avoid leaking too many implementation details into your API surface too early

### 5. Expose metrics and richer observability

Good candidates:

- reconciliation duration by controller
- reconcile success/failure counts
- route readiness lag
- resource counts by router
- event correlation for failed routes

## Safe Ways to Extend the Operator

### Preserve idempotency

Every reconcile path should be safe to run repeatedly.

That means:

- avoid append-only mutation logic
- always derive desired state from spec + current cluster state
- prefer `CreateOrUpdate`

### Preserve ownership and cleanup

Before adding a new managed resource, decide:

- should it have an owner reference?
- should it be labeled?
- how will stale instances be detected?
- how will deletion be handled?

### Keep status meaningful

Do not mark a resource ready just because create/update succeeded.

Instead, decide what "operationally ready" means and reflect that in status.

This project already follows that direction for router readiness.

### Keep builders and reconcilers separate

If you add a new managed object:

- put object mutation in a builder
- keep reconciliation orchestration in the controller

That pattern is one of the cleanest parts of the current codebase and is worth preserving.

## Known Limitations and Design Tradeoffs

### Cluster-scoped CRDs

Both `Cell` and `CellRouter` are cluster-scoped.

That simplifies cross-namespace orchestration, but it also means:

- names must be globally unique in the cluster
- RBAC and tenancy need more care in multi-team environments

If multi-tenant self-service becomes important, you may eventually reconsider scope.

### One backend service per cell abstraction

The current model assumes one entrypoint service per cell.

This is intentionally simple, but restrictive if you later want:

- multiple protocols
- multiple service classes
- split internal/external entrypoints

### Gateway implementation dependence

The operator targets Gateway API resources, which is good abstraction-wise, but:

- real behavior still depends on the installed gateway controller
- local validation here uses Envoy Gateway

If you need portability across multiple Gateway implementations, invest in compatibility tests.

## Practical Debugging Checklist

When routing does not work, check in this order:

1. `kubectl get cell <name> -o yaml`
2. `kubectl get cellrouter <name> -o yaml`
3. `kubectl get gateway -A -o yaml`
4. `kubectl get httproute -A -o yaml`
5. `kubectl get referencegrant -A -o yaml`
6. `kubectl -n <cell-namespace> get svc,endpointslice`
7. controller logs
8. gateway controller logs
9. real traffic via `curl`

Typical failure modes:

- missing gateway controller or missing `GatewayClass`
- unresolved cross-namespace backend refs
- wrong workload selector, so the service has no endpoints
- route match not actually matching the request
- local gateway service not exposed the way you expect

## Suggested Next Improvements

High-value next steps:

- add automated e2e tests for multi-cell routing
- support weighted multi-backend routing
- add richer observability and metrics
- improve TLS support and local HTTPS validation
- formalize compatibility expectations for gateway implementations
- add admission validation for spec combinations that are invalid or unsafe

## Summary

The operator is currently structured around a solid pattern:

- API types define high-level intent
- reconcilers orchestrate resource lifecycle
- builders define deterministic desired state
- status conditions reflect operational readiness
- local validation includes real traffic through Gateway API

If you keep those boundaries intact, the project is straightforward to extend without turning the controllers into unmaintainable procedural code.

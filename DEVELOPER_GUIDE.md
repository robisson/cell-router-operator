# Developer Guide

## Purpose

This guide explains how the operator works internally and how to extend it safely.

The project models a cell-based routing control plane on Kubernetes by separating the problem into three custom resources:

- `Cell`: namespace, entrypoint service, backend readiness, lifecycle state, and optional namespace policies
- `CellRouter`: gateway lifecycle and route lifecycle
- `CellPlacement`: reusable placement rules that resolve tenants or partitions to cells through a router

The key design principle is:

- `Cell` owns the service boundary of a workload domain.
- `CellRouter` owns the shared routing boundary in front of cells.
- `CellPlacement` owns reusable traffic-selection rules without hard-coding every placement into the router spec.

That separation keeps workload readiness, route orchestration, and placement concerns independent enough to evolve separately.

## Core Concepts

### Cell

A `Cell` is the operator's abstraction for an isolated workload domain.

It is cluster-scoped, but it manages namespaced resources:

- a namespace
- an entrypoint service inside that namespace
- optionally a `ResourceQuota`
- optionally a `LimitRange`
- optionally a `NetworkPolicy`

The `Cell` spec tells the operator:

- what namespace to use
- how to label and annotate the namespace
- how to select the backing pods
- what service name and port to expose
- what lifecycle state to enforce
- which namespace policies to manage
- whether the namespace should be deleted when the CR is deleted

The `Cell` status tells consumers whether the cell is actually usable for traffic:

- effective namespace
- entrypoint service
- operational state
- available endpoint count
- conditions such as namespace readiness, service readiness, policy readiness, backend readiness, and overall readiness

### CellRouter

A `CellRouter` is the routing abstraction.

It manages:

- one `Gateway`
- one `HTTPRoute` per explicit route in `.spec.routes`
- one `HTTPRoute` per matching `CellPlacement`
- one `ReferenceGrant` per resolved backend service reference
- the namespace that hosts the gateway resources

Each route entry can now model:

- a primary cell
- additional weighted backends
- a fallback backend
- listener binding
- hostname matching
- path matching
- header matching
- query parameter matching

### CellPlacement

A `CellPlacement` is a reusable routing rule that belongs to a `CellRouter`.

It exists to model partition or tenant routing more cleanly than embedding every placement rule into `CellRouter.spec.routes`.

Today it supports:

- `routerRef`
- listener names
- hostnames
- path match
- header matches
- query parameter matches
- one or more destination cells with weights

The router turns a placement into an `HTTPRoute` and publishes placement status with the resolved backends.

The current samples intentionally focus on two equivalent `payments` cells plus tenant placements. That is closer to a real cell-based topology than routing between unrelated business domains.

## API Model

Main API types:

- [cell_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cell_types.go)
- [cellrouter_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cellrouter_types.go)
- [cellplacement_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cellplacement_types.go)

### Cell Spec Highlights

Important fields:

- `spec.namespace`
- `spec.entrypoint.serviceName`
- `spec.entrypoint.port`
- `spec.entrypoint.targetPort`
- `spec.entrypoint.type`
- `spec.workloadSelector`
- `spec.state`
- `spec.policies.resourceQuota`
- `spec.policies.limitRange`
- `spec.policies.networkPolicy`
- `spec.namespaceLabels`
- `spec.namespaceAnnotations`
- `spec.serviceLabels`
- `spec.serviceAnnotations`
- `spec.tearDownOnDelete`

Status fields:

- `status.namespace`
- `status.entrypointService`
- `status.operationalState`
- `status.availableEndpoints`
- `status.observedGeneration`
- `status.conditions`

Conditions:

- `NamespaceReady`
- `ServiceReady`
- `PoliciesReady`
- `BackendReady`
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
- `spec.routes[].additionalBackends`
- `spec.routes[].fallbackBackend`
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

Each managed route summary can now report:

- backend refs actually materialized
- placement ref when the route came from a placement
- a reason such as `traffic-ready`, `degraded: ...`, or `using fallback backend`

### CellPlacement Spec Highlights

Important fields:

- `spec.routerRef`
- `spec.listenerNames`
- `spec.hostnames`
- `spec.pathMatch`
- `spec.headerMatches`
- `spec.queryParamMatches`
- `spec.destinations`

Status fields:

- `status.resolvedBackends`
- `status.observedGeneration`
- `status.conditions`

Conditions:

- `Ready`

## Runtime Wiring

The manager is bootstrapped in [main.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/cmd/main.go).

Main responsibilities there:

- register Kubernetes client-go schemes
- register operator API schemes
- register Gateway API types
- configure metrics and probes
- configure leader election
- create the controller manager
- wire `CellReconciler`
- wire `CellRouterReconciler`

The manager intentionally includes:

- `gateway.networking.k8s.io/v1` for `Gateway` and `HTTPRoute`
- `gateway.networking.k8s.io/v1beta1` for `ReferenceGrant`

## Labels and Finalizers

Shared constants live in [constants.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/constants/constants.go).

Important labels:

- `cellrouter.io/managed-by`
- `cellrouter.io/cell-name`
- `cellrouter.io/entrypoint-service`
- `cellrouter.io/router-name`
- `cellrouter.io/placement-name`

Important finalizers:

- `cell.cellrouter.io/finalizer`
- `cellrouter.cellrouter.io/finalizer`

Important fixed managed names:

- `cell-quota`
- `cell-limits`
- `cell-entrypoint`

These identifiers are part of the operator's ownership contract and should not be changed casually once clusters contain data.

## Resource Builder Pattern

The operator keeps object mutation logic in builders instead of embedding raw spec assignment into reconcilers.

Files:

- [internal/resource/cell/builder.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/resource/cell/builder.go)
- [internal/resource/router/builder.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/resource/router/builder.go)

This keeps reconcilers focused on orchestration and makes mutation logic testable in isolation.

### Cell builders

`MutateNamespace`:

- applies operator labels
- applies user namespace labels and annotations
- protects operator-owned labels from being overwritten

`MutateService`:

- applies labels and annotations
- derives the selector from `workloadSelector`
- defaults to `cellrouter.io/cell-name=<cell-name>` when no selector is provided
- sets type, protocol, port, and target port

`MutateResourceQuota`:

- projects `spec.policies.resourceQuota` into a namespaced `ResourceQuota`

`MutateLimitRange`:

- projects `spec.policies.limitRange` into a container-scoped `LimitRange`

`MutateNetworkPolicy`:

- creates an ingress `NetworkPolicy` that selects the cell workloads
- allows same-namespace traffic
- optionally allows traffic from namespaces selected by labels

`WorkloadSelector`:

- centralizes selector defaulting so the service and policy resources target the same pods

### Router builders

`MutateGatewayNamespace`:

- labels the namespace used to host gateway resources

`MutateGateway`:

- applies labels and annotations
- copies gateway class and listeners
- sorts listeners for stable reconciliation

`MutateReferenceGrant`:

- grants an `HTTPRoute` in the gateway namespace permission to reference one concrete backend `Service`

`MutateHTTPRoute`:

- labels the route
- marks placement-derived routes
- binds parent refs to the managed gateway
- builds matches from path, header, and query configuration
- emits one or more backend refs with weights

`ReferenceGrantName`:

- derives a deterministic per-backend grant name using a hash of route and backend identity
- this matters now that a route can resolve to multiple backends

## Cell Reconciler

Implementation:

- [cell_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cell_controller.go)

### Reconciliation flow

Normal path:

1. Load the `Cell`
2. Exit if it no longer exists
3. If deletion timestamp is set, run finalization
4. Ensure the finalizer exists
5. Reconcile the namespace
6. Reconcile the entrypoint service
7. Reconcile optional policy resources
8. Count ready backend endpoints from `EndpointSlice`
9. Derive operational state and readiness conditions
10. Patch cell status

Deletion path:

1. Delete the managed entrypoint service if the cell owns it
2. Delete the namespace only when `tearDownOnDelete=true` and labels prove the operator owns it
3. Remove the finalizer

### Effective namespace

`effectiveNamespace(cell)` centralizes defaulting:

- use `spec.namespace` when provided
- otherwise default to the cell name

That helper is shared across reconcile, readiness, and finalization so the operator never disagrees with itself about where cell-owned resources should live.

### Backend readiness

This is one of the most important changes in the project.

A cell is no longer considered ready only because its namespace and service exist. The reconciler now lists `EndpointSlice` objects for the entrypoint service and counts ready addresses.

That means:

- a `Cell` can be structurally reconciled but still `Ready=False`
- a cell can expose `BackendReady=False` while pods are still starting
- traffic-aware consumers such as `CellRouter` can make better decisions

### Lifecycle state

`spec.state` currently supports:

- `Active`
- `Draining`
- `Disabled`

Behavior:

- `Active`: cell may become `Ready=True` when the backend is ready
- `Draining`: backend may still be healthy, but the cell is withheld from new traffic
- `Disabled`: cell is withdrawn from routing entirely

The controller writes the enforced value to `status.operationalState`.

### Policy resources

The cell controller now manages optional namespace policy resources:

- `ResourceQuota`
- `LimitRange`
- `NetworkPolicy`

These are reconciled declaratively:

- created or updated when present in spec
- deleted when removed from spec
- guarded by owner references

## CellRouter Reconciler

Implementation:

- [cellrouter_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cellrouter_controller.go)

### Reconciliation flow

Normal path:

1. Load the `CellRouter`
2. Exit if it no longer exists
3. If deletion timestamp is set, run finalization
4. Ensure the finalizer exists
5. Reconcile the gateway namespace
6. Reconcile the `Gateway`
7. List `CellPlacement` resources targeting this router
8. Build a unified route plan from explicit routes and placements
9. Resolve traffic-ready backends
10. Reconcile per-backend `ReferenceGrant` objects
11. Reconcile `HTTPRoute` resources
12. Clean up stale routes and grants
13. Patch router status

Deletion path:

1. Delete managed `HTTPRoute` resources
2. Delete managed `ReferenceGrant` resources
3. Delete the managed `Gateway`
4. Remove the finalizer

### Route planning

The router now works from a route plan, not just raw `spec.routes`.

Sources of route plans:

- explicit `CellRouter.spec.routes`
- implicit routes derived from `CellPlacement`

That unified plan is why a placement can behave operationally like any other route while still remaining an independent API object.

### Backend resolution

For each plan, the router resolves:

- a primary backend from `cellRef`
- zero or more additional weighted backends
- an optional fallback backend

The router now checks whether a backend cell is actually traffic-ready before including it:

- `Active`
- `BackendReady=True`
- `Ready=True`

Draining or disabled cells are skipped as normal backends.

If all primary backends are unavailable and a fallback exists, the fallback is used instead.

### Route outcomes

The router can now express richer route states:

- `traffic-ready`
- `degraded: ...`
- `using fallback backend`
- waiting because no traffic-ready backends were available

This is surfaced in `status.managedRoutes`.

### Watching dependencies

The router reconciler now watches:

- `CellRouter`
- owned `Gateway`
- owned `HTTPRoute`
- owned `ReferenceGrant`
- all `Cell` updates, because lifecycle or backend readiness changes affect routability
- all `CellPlacement` updates targeting a router

That is how a patch such as `payments-cell-1 -> Draining` or a backend readiness transition can reconfigure routes without touching the router object directly.

## CellPlacement Internals

`CellPlacement` does not have its own controller at the moment. Instead, the `CellRouterReconciler` lists placements that reference a given router and patches placement status while processing them.

That means:

- placement reconciliation is effectively router-scoped
- placement readiness depends on router-side backend resolution
- the router remains the single place where routing objects are materialized

This is a reasonable tradeoff while the placement model is still small. If placement logic grows significantly, splitting it into a dedicated controller would become easier to justify.

## Cross-Namespace Routing and ReferenceGrant

Gateway API requires explicit permission when an `HTTPRoute` in one namespace targets a `Service` in another namespace.

That is why the operator creates one `ReferenceGrant` per resolved backend in the target cell namespace.

Without those objects:

- the `HTTPRoute` may exist
- `ResolvedRefs` will fail
- routing will not work

This is especially important now that a route can resolve to multiple backends across multiple cells.

## Status and Readiness Semantics

Status conditions are the contract between:

- declared spec intent
- reconcile progress
- debugging visibility
- smoke tests
- other controllers or automation that depend on traffic readiness

### Cell readiness

A cell is `Ready=True` only when:

- namespace reconciliation succeeded
- service reconciliation succeeded
- policy reconciliation succeeded
- the backend has ready endpoints
- the lifecycle state is `Active`

Examples:

- pods still starting -> `BackendReady=False`, `Ready=False`
- `Draining` with healthy endpoints -> `BackendReady=True`, `Ready=False`
- `Disabled` -> `Ready=False`

### Router readiness

A router is `Ready=True` only when:

- the gateway reconciled successfully
- each explicit route or placement-derived route resolved to at least one traffic-ready backend
- each managed route is accepted by Gateway API
- `ResolvedRefs=True` when the implementation exposes that condition

The router can still stay ready while a specific route is degraded, as long as it still resolves to usable backends. That matters for scenarios where a route still has another healthy backend or where only part of the sample topology is affected.

### Placement readiness

A placement is `Ready=True` when at least one destination resolves to a traffic-ready backend and the router has published that resolution to placement status.

## Why `Gateway Programmed=False` Can Still Be Okay in Kind

In local Kind setups with Envoy Gateway, the top-level `Gateway` `Programmed` condition may remain false because no external address is assigned to the gateway service.

That does not necessarily mean routing is broken.

For local correctness, the meaningful checks are:

- `Gateway Accepted=True`
- listener conditions are accepted/programmed/resolved
- `HTTPRoute Accepted=True`
- `HTTPRoute ResolvedRefs=True`
- real traffic reaches the backend

That is why the project validates traffic with `curl`, not only with top-level gateway conditions.

## Local End-to-End Flow

The local flow is implemented in [run-local.sh](/Users/robisson/projetcs/golang/k8s/cell-router-operator/scripts/run-local.sh).

The script:

1. Creates or reuses a Kind cluster
2. Waits for nodes to become ready
3. Installs Gateway API CRDs
4. Cleans up stale local Envoy Gateway resources when needed
5. Installs Envoy Gateway via Helm
6. Applies the local `GatewayClass`
7. Labels the Envoy Gateway namespace for the sample `NetworkPolicy`
8. Runs unit tests
9. Builds the operator image
10. Loads the image into Kind
11. Applies CRDs
12. Deploys the controller
13. Applies sample cells and workloads
14. Waits for traffic-ready cells
15. Applies the sample router and placements
16. Verifies route readiness
17. Sends real traffic through Envoy
18. Verifies direct and placement traffic with real `curl` requests

## Current Example Topology

The current sample topology includes two cells for the same `payments` domain:

- `payments-cell-1`
- `payments-cell-2`

Explicit routes:

- `payments-cell-1-route`
  - host `payments.example.com`
  - path `/payments/cell-1`
  - backend: `payments-cell-1`
- `payments-cell-2-route`
  - host `payments.example.com`
  - path `/payments/cell-2`
  - backend: `payments-cell-2`

Placements:

- `tenant-a-placement`
  - host `payments.example.com`
  - path `/tenant`
  - header `X-Tenant: tenant-a`
  - destination: `payments-cell-1`
- `tenant-b-placement`
  - host `payments.example.com`
  - path `/tenant`
  - header `X-Tenant: tenant-b`
  - destination: `payments-cell-2`

## Testing Strategy

### Unit tests

The project has focused tests for:

- API deep-copy behavior
- `CellReconciler`
- `CellRouterReconciler`
- builder functions
- metadata merge helpers

Newer tests cover:

- cell waiting for backend endpoints
- placement materialization
- lifecycle-aware routing decisions when a cell is not traffic-ready

Good future unit test targets:

- multiple fallback layers
- route cleanup after placement deletion
- network policy variants
- more detailed status transitions

### End-to-end testing

The local script is still the main practical e2e harness.

A more formal e2e suite would ideally cover:

- initial bootstrap
- direct routing
- placement routing
- lifecycle transitions
- stale route cleanup
- finalizer behavior

## Common Extension Scenarios

### 1. Add new route match types

Where to change:

- API types in [cellrouter_types.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/api/v1alpha1/cellrouter_types.go)
- route builder logic in [builder.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/resource/router/builder.go)
- router tests in [cellrouter_controller_test.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cellrouter_controller_test.go)

### 2. Expand lifecycle policy

Likely next additions:

- maintenance windows
- explicit drain deadlines
- state transition validation
- progressive drain behavior rather than immediate withdrawal from new traffic

The main extension seam is the readiness and routing decision logic in:

- [cell_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cell_controller.go)
- [cellrouter_controller.go](/Users/robisson/projetcs/golang/k8s/cell-router-operator/internal/controller/cellrouter_controller.go)

### 3. Add richer policy resources

Natural next candidates:

- authn/authz policy
- retries and timeouts
- header mutation
- rate limiting
- implementation-specific gateway policy CRs

Recommended approach:

- keep the `Cell` API as the stable intent model
- translate to lower-level resources in builders
- avoid leaking too much gateway-controller-specific configuration into the public CRD too early

### 4. Evolve placement into a richer platform capability

Current `CellPlacement` is intentionally small.

You could extend it toward:

- explicit tenant IDs or partition keys
- placement classes
- capacity-aware placement
- migration workflows
- rebalance workflows
- audit trails for placement moves

At that point, it may make sense to introduce a dedicated placement controller instead of keeping placement logic entirely inside the router reconciler.

### 5. Add more detailed observability

Good candidates:

- reconcile duration by controller
- success and error counters by phase
- number of traffic-ready cells
- number of degraded routes
- fallback activation counts
- route convergence latency

## Safe Ways to Extend the Operator

### Preserve idempotency

Every reconcile path should be safe to run repeatedly.

That means:

- do not append to desired state blindly
- derive desired state from spec plus current cluster facts
- prefer `CreateOrUpdate`

### Preserve ownership and cleanup

Before adding a new managed resource, decide:

- should it use an owner reference?
- should it use labels for ownership discovery?
- how will stale instances be detected?
- how will finalization behave?

### Keep status meaningful

Do not mark a resource ready only because create or update succeeded.

Instead, ask what "traffic-ready" or "operationally ready" means for that object and encode that in conditions.

### Keep builders and reconcilers separate

If you add a new managed object:

- keep mutation in a builder
- keep orchestration in a reconciler

That boundary is one of the strongest maintainability traits in the current codebase.

## Known Limitations and Tradeoffs

### Cluster-scoped CRDs

`Cell`, `CellRouter`, and `CellPlacement` are cluster-scoped.

That simplifies cross-namespace orchestration, but it also means:

- names must be globally unique
- RBAC needs more care in shared clusters
- self-service multitenancy would need stronger governance

### One entrypoint service per cell

The `Cell` abstraction still assumes one logical entrypoint service per cell.

That is simple and useful, but restrictive if you later need:

- multiple protocols
- multiple service classes
- separate internal and external entrypoints

### Placement is still intentionally lightweight

`CellPlacement` is not a scheduler.

It is currently a declarative mapping resource that the router materializes into Gateway API routes.

### Gateway implementation dependence

The operator targets Gateway API resources, which is a good abstraction layer, but real behavior still depends on the installed gateway controller.

The local validation path in this project is built around Envoy Gateway.

## Practical Debugging Checklist

When traffic does not behave as expected, check in this order:

1. `kubectl get cell <name> -o yaml`
2. `kubectl get cellplacement <name> -o yaml`
3. `kubectl get cellrouter <name> -o yaml`
4. `kubectl get gateway -A -o yaml`
5. `kubectl get httproute -A -o yaml`
6. `kubectl get referencegrant -A -o yaml`
7. `kubectl -n <cell-namespace> get svc,endpointslice`
8. `kubectl -n <cell-namespace> get resourcequota,limitrange,networkpolicy`
9. controller logs
10. gateway controller logs
11. real traffic via `curl`

Typical failure modes:

- wrong workload selector, leaving the service without endpoints
- cell lifecycle state blocking routing
- route has no traffic-ready backends
- missing or stale `ReferenceGrant`
- request does not actually match hostname/path/header/query conditions
- network policy blocking ingress from the gateway namespace
- stale local Envoy Gateway installation in Kind

## Suggested Next Improvements

High-value next steps:

- add formal e2e tests for weighted routing, placement, and fallback
- strengthen admission validation for invalid lifecycle or routing combinations
- expose metrics for degraded routes and fallback usage
- evolve placement beyond simple declarative mapping
- improve HTTPS/TLS support in the local flow
- test compatibility against more than one Gateway API implementation

## Summary

The operator is now structured around a stronger pattern than the original baseline:

- API types define intent for cells, routers, and placements
- reconcilers orchestrate lifecycle and traffic-readiness decisions
- builders define deterministic desired state
- status conditions reflect operational readiness, not just object creation
- local validation includes real traffic and placement rules across two equivalent cells of the same workload domain

If you preserve those boundaries, the project remains straightforward to extend without turning the controllers into large procedural scripts.

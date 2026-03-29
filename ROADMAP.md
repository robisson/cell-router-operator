# Roadmap

## Current baseline

`cell-router-operator` already provides a solid foundation for cell-based routing on Kubernetes.

Today the project:

- models `Cell` and `CellRouter` as separate control plane resources
- reconciles namespaces and entrypoint services for cells
- reconciles gateways, HTTP routes, and reference grants for routing
- validates the end-to-end flow locally with Kind and Envoy Gateway

It is not yet a complete cell platform. It still lacks placement, draining, failover, capacity-aware behavior, and deeper operational signals that would be expected from a production-oriented control plane.

## Design principles

- Keep `Cell` responsible for the service boundary of a single cell.
- Keep `CellRouter` responsible for the routing boundary in front of cells.
- Prefer Gateway API portability over tight coupling to a specific implementation.
- Separate "resource reconciled" from "traffic-ready" in status and readiness logic.
- Keep ownership, cleanup, and finalization rules explicit and conservative.

## Phase 1: Operator hardening

**Goal**

Make the current operator behavior safer, more observable, and closer to production-grade reconciliation semantics.

**Why it matters**

The project already creates the right resources, but some readiness and lifecycle signals still reflect object existence more than real operability. Hardening the current model should happen before expanding the control plane.

**Implementation themes**

- Refine readiness so `Cell` and `CellRouter` represent usable backends and operable routes, not only reconciled objects.
- Add stronger validation and defaulting, preferably webhook-backed.
- Reconcile scaffold inconsistencies and tighten API and status contracts.
- Improve logs, conditions, Kubernetes events, metrics, and CI verification.

**Definition of done**

- Status conditions clearly distinguish reconciliation success from traffic readiness.
- Invalid specs are rejected early instead of failing late inside reconciles.
- CI verifies generated manifests and the controller deploy/install flow.
- The operator produces actionable status and logs for the main failure paths.

## Phase 2: Cell lifecycle and policy

**Goal**

Expand `Cell` from a namespace-and-service abstraction into a more explicit lifecycle object.

**Why it matters**

Cell-based systems need operational states and per-cell policy, not just a service boundary. This phase adds control over how cells behave over time.

**Implementation themes**

- Add explicit operational states such as active, draining, and disabled.
- Add optional namespace-level policy management such as resource quotas, limit ranges, or network policy generation.
- Publish clearer cell status for backend availability and lifecycle state.

**Definition of done**

- A maintainer can tell whether a cell is active, draining, disabled, or unavailable from status alone.
- Per-cell policy is declarative and reconciled through the operator instead of manual namespace setup.
- The `Cell` API remains focused on lifecycle and service boundary responsibilities.

## Phase 3: Routing and traffic management

**Goal**

Make `CellRouter` capable of handling more realistic traffic-control scenarios.

**Why it matters**

A production routing layer needs more than simple static route-to-cell mapping. It needs controlled transition behavior and better handling of unavailable targets.

**Implementation themes**

- Expand routing policy toward canary, richer weighted traffic handling, and controlled fallback or drain behavior.
- Improve route readiness and route-level failure reporting.
- Define how the operator should behave when a referenced cell is unresolved, unavailable, or draining.

**Definition of done**

- Route status explains both backend resolution and traffic operability.
- The router can express more than one steady-state traffic pattern.
- Unavailable or transitional cells produce predictable routing outcomes instead of only generic "not ready" behavior.

## Phase 4: Cell platform capabilities

**Goal**

Evolve from a routing operator into a broader cell-platform control plane.

**Why it matters**

The core promise of cell-based architecture is not only routing to cells, but deciding which traffic belongs in which cell and how that mapping evolves safely over time.

**Implementation themes**

- Introduce a placement or assignment concept for mapping tenants or partitions to cells.
- Model cell capacity and metadata so routing decisions can become policy-driven.
- Define migration, rebalance, and drain workflows as future platform features.

**Definition of done**

- The control plane can represent placement intent separately from routing mechanics.
- Cells can expose enough metadata to support future assignment and rebalance logic.
- Migration-oriented workflows have a clear design direction even if full automation remains iterative.

## Recommended implementation order

1. Harden the existing operator first.
2. Add explicit cell lifecycle and per-cell policy.
3. Expand routing and traffic-management behavior.
4. Introduce placement and broader platform capabilities.

This order keeps the project stable while extending it. It also avoids building advanced cell-platform features on top of weak readiness or unclear lifecycle semantics.

## Likely future API additions

- richer `Cell` operational state and backend-availability status
- richer `CellRouter` status for route operability and failure reporting
- a future placement or assignment resource for partition-to-cell mapping

These should remain future-oriented until the current APIs are hardened and the lifecycle model is clearer.

## Non-goals for now

- building a full scheduler in the first iterations
- introducing a multi-cluster control plane immediately
- tightly coupling the project to Envoy-specific APIs beyond Gateway API integration
- expanding the surface area faster than the current operator can observe and report correctly

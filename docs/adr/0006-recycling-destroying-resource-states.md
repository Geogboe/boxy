# ADR 0006: Explicit `recycling`/`destroying` Resource States

Status: Accepted

## Context

Every path that tears down a pool resource — max-age recycling
(`reconcileLocked`'s stale loop), pool drain (`applyDrain`), and
sandbox-triggered destroy (`DestroyResource`) — followed the same shape:
call the provisioner's `Destroy`, then mark the resource `destroyed` and
persist. Nothing was persisted *before* the destroy call. For backends where
teardown takes real wall-clock time (stopping/removing a VM or container), a
resource mid-teardown was indistinguishable from a healthy one via the REST
API, CLI, or `.boxy/state.json` — it just vanished once the destroy
completed. That's the same signature as a stalled/hung backend, with no way
for an operator to tell the difference.

`pkg/model/resource.go` already declared `ResourceStateProvisioning`,
`ResourceStateReleased`, `ResourceStateDestroying`, and `ResourceStateError`,
but none of them were ever assigned by production code — there was no
existing transient-state transition sequence to extend.

## Decision

- Added `ResourceStateRecycling`, used only by the max-age recycle path.
  `ResourceStateDestroying` (already declared, previously unused) is now
  used by drain and by `DestroyResource`. The split keeps the semantics
  honest: drain and explicit sandbox-triggered destroy are not "recycling."
- All three destroy call sites now persist the transient state (via
  `store.PutResource`) *before* invoking `provisioner.Destroy`, guarded by
  `isTransientDestroyState` so a resource already mid-teardown (see orphan
  sweep below) isn't redundantly re-marked. The existing post-destroy
  `destroyed` write is unchanged.
- No REST/CLI/UI surface changes were needed for observability:
  `GET /api/v1/resources`, `/api/v1/resources/{id}`, `GET /api/v1/pools`,
  and `.boxy/state.json` already round-trip `Resource.State` as a plain
  string. The acceptance proof is `internal/server`'s
  `TestAPI_GetResource_ShowsRecyclingStateWhileDestroyInFlight`, which blocks
  a fake provisioner's `Destroy` mid-call and confirms the REST API shows
  `"recycling"` while it's blocked.

### Orphan sweep (crash recovery)

Persisting the transient state before the destroy call introduces a new
failure mode: if the process crashes between that write and the destroy
completing, the resource is stuck `recycling`/`destroying` — and
`RebuildReadyInventory` (`internal/pool/inventory.go`) only re-admits `ready`
resources into `p.Inventory.Resources`, so the stuck resource silently drops
out of inventory. Neither `computeStale` nor the drain path scans anything
but inventory, so without an explicit sweep it would zombie forever: still
counted against `MaxTotal` (`countTrackedResources` counts any non-destroyed
resource with a matching `OriginPool`), with its backing provider resource
possibly never torn down.

`orphanedTransientResources` scans all resources for a matching `OriginPool`
in one of the caller-specified transient states, absent from the rebuilt
inventory, and folds the result into that tick's stale/drain list so the
destroy gets retried.

**The two sweep sites intentionally scope different state sets:**

- The stale/recycle path sweeps only `recycling`. A `destroying` orphan
  there is a failed sandbox-triggered `DestroyResource` call, which is
  *already* retried independently by `sandbox.DeletionReconciler` — it
  re-scans each deleting sandbox's own resource list every tick, with no
  dependency on pool inventory at all. Sweeping it from the pool side too
  would just be a second, redundant retry loop racing the first.
- The drain path sweeps both `recycling` and `destroying`, because drain has
  no other retry mechanism for a resource it left mid-teardown.

This relies on an existing, unchanged invariant: every driver's `Delete`
(devfactory, docker, hyperv) is idempotent — deleting an already-gone
resource returns `nil` rather than erroring. That's what makes any residual
overlap between the two retry paths (e.g. a resource that changes hands
between "pool-owned" and "sandbox-owned" framing across a crash) safe rather
than a hard failure.

## Non-goals / explicitly out of scope

- **No new CLI/REST surface.** `boxy status` still only aggregates counts;
  no new `boxy debug pool list/get` command and no `/ui/resources` page were
  added. The existing endpoints already satisfy the observability
  requirement. A richer per-resource CLI/UI view is a reasonable fast-follow
  if wanted, not part of this change.
- **Not related to ADR 0002.** ADR 0002 forbids returning an *allocated*
  resource back into pool inventory for reuse. This ADR is about observing
  an in-flight destroy of a resource that's already being torn down
  permanently — the two don't conflict or overlap in scope.

## Consequences

- Each destroy operation now does two `PutResource` writes instead of one
  (mark transient, then mark destroyed) — a negligible increase at expected
  scale, but worth noting since `pkg/store/disk.go`'s `PutResource` does a
  full synchronous `state.json` rewrite per call.
- A resource stuck in a transient state for an unusually long time is now a
  visible signal (via the REST API or `.boxy/state.json`) that a destroy is
  taking longer than expected or has stalled — previously indistinguishable
  from normal operation.

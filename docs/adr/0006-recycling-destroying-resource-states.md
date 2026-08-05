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
- No REST/UI surface changes were needed for observability (see the
  Non-goals section below for CLI):
  `GET /api/v1/resources`, `/api/v1/resources/{id}`, `GET /api/v1/pools`,
  and `.boxy/state.json` already round-trip `Resource.State` as a plain
  string. The acceptance proof is `internal/server`'s
  `TestAPI_GetResource_ShowsDestroyingStateWhileDestroyInFlight` (drives
  `DestroyResource`, blocking a fake provisioner's `Destroy` mid-call to
  confirm the REST API shows `"destroying"` while genuinely in flight) and
  `TestAPI_GetResource_ShowsRecyclingStateWhileStaleDestroyInFlight` (the
  same proof for the max-age recycle path, confirming `"recycling"`).

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
in either transient state, absent from the rebuilt inventory, and folds the
result into that tick's stale/drain list so the destroy gets retried.

**Both sweep sites (stale/recycle and drain) sweep both `recycling` and
`destroying`.** An earlier version of this design tried to split scope —
stale/recycle sweeps only `recycling`, reasoning that a `destroying` orphan
is always a failed sandbox-triggered `DestroyResource` call already retried
independently by `sandbox.DeletionReconciler` (it re-scans each deleting
sandbox's own resource list every tick, with no dependency on pool
inventory). That assumption is false: `applyDrain` also uses `destroying`,
for pool-owned resources with no sandbox involved at all. If `applyDrain`
crashes mid-teardown and the operator later clears the drain (`debug pool
fill`), the resource is orphaned outside a drain — at that point only the
stale/recycle path ever runs for that pool again, and the narrower design
left it permanently unrecoverable (verified with a reconcile test seeding
exactly this state: zero `Destroy` calls, state unchanged, forever). Sweeping
both states from both sites fixes this, at the cost of occasionally retrying
a genuinely sandbox-owned `destroying` resource redundantly alongside
`sandbox.DeletionReconciler`'s own retry of it.

That redundancy is safe, not just tolerated, because of an existing,
unchanged invariant: every driver's `Delete` (devfactory, docker, hyperv) is
idempotent — deleting an already-gone resource returns `nil` rather than
erroring. The overlap window is also self-closing: as soon as either retry
path succeeds, the resource record is deleted from the store, so the other
path stops seeing it on its next scan.

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

# Design: Hyper-V Create-Failure Hardening (#174, #185, #183)

## Context

Three issues were filed against the Hyper-V create path during review of #173's
PR (#182/#186):

- **#174** (`priority:high`, `bug`): a failed `Create` can leave a `boxy-*` VM
  object on the host that Boxy has no record of. Root cause: `Create`'s
  cleanup-on-failure path discards its own error, and even when cleanup
  succeeds or fails, `Provisioner.Provision` returns a bare `model.Resource{}`
  on any `Create` error — no ID ever reaches the store, so nothing downstream
  can ever know a VM might exist.
- **#185**: `hyperv.CapacityError` (and any future typed driver error) is
  flattened to a plain string at the `RemoteAgent`/gRPC boundary
  (`AgentError{message}`), so a caller on that path can never `errors.As` for
  it — only the in-process `EmbeddedAgent` path preserves the type.
- **#183**: `Driver.reserveMemory`'s in-process counter and the live
  host-memory query can each fail to reflect the other's state around a
  `Create` call's boundary — in one direction (sequential calls) this can
  under-reserve and overcommit the host; in the other (concurrent calls) it
  can over-reserve and spuriously reject a request the host could satisfy.
  The issue's own text states a full fix ("a third signal" distinguishing
  "not yet visible to the live query" from "already visible") isn't designed.

**Scope note:** much of #174's originally reported symptom (32 creates over 4
hours against a degraded host) is already mitigated by shipped work — ADR-0004
(`Get-VMHost` pre-flight, teardown guard, capped exponential backoff) and
#173/#182 (memory pre-flight, which stops most `Start-VM 0x8007000E` failures
from happening at all). What remains live in `driver.go` today is narrower:
the swallowed cleanup error and the total invisibility of a failed `Create`'s
VM object.

**Sequencing:** #185 → #174 → #183, in that order, in one implementation pass.
#185 is small and mechanical, and #174's cleanup-vs-broken-host distinction
benefits from it. #183 is scoped down per the issue's own admission — this
design narrows the window, it does not close it.

---

## Section 1: #185 — typed errors across the RemoteAgent/gRPC boundary

### `CapacityError` moves to `providersdk`

`CapacityError{RequestedMemoryMB, AvailableMemoryMB int64}` is not intrinsically
Hyper-V-specific, so it's promoted to `pkg/providersdk`. `hyperv.CapacityError`
becomes a type alias, the same pattern already used for
`ExecOp = providersdk.ExecOperation` in `driver.go:284`:

```go
// pkg/providersdk/errors.go
type CapacityError struct {
    RequestedMemoryMB int64
    AvailableMemoryMB int64
}
func (e *CapacityError) Error() string { ... }
func (e *CapacityError) ErrorType() string { return "capacity" }
```

```go
// pkg/providersdk/providers/hyperv/driver.go
type CapacityError = providersdk.CapacityError
```

Existing `errors.As(err, &hyperv.CapacityError{})` call sites are unaffected.

### `ErrorTyper` — a generic classification hook

```go
// pkg/providersdk/driver.go
// ErrorTyper lets a driver error self-report a stable category and JSON
// detail for propagation across the RemoteAgent/gRPC boundary, without
// coupling the provider-neutral agentsdk layer to any specific driver
// package. Optional — errors that don't implement it just lose their type
// on that path, same as today.
type ErrorTyper interface {
    ErrorType() string // e.g. "capacity"
}
```

### Wire format: opaque JSON detail, not per-error proto fields

```protobuf
message AgentError {
  string message = 1;
  string error_type = 2;         // e.g. "capacity"; empty = no typed error
  bytes error_detail_json = 3;   // opaque JSON payload for error_type
}
```

This mirrors `CreateCommand.config_json`/`UpdateCommand.operation_json`'s
existing rationale in the same proto file: opaque JSON instead of a proto
field per error type, so a new typed error never needs a proto change.

### Classification and reconstruction

`pkg/agentsdk/remoteclient.go`'s `errorResult` helper gains an `errors.As`
check:

```go
func errorResult(commandID, msg string, err error) *boxyagentv1.AgentError {
    ae := &boxyagentv1.AgentError{Message: msg}
    var et providersdk.ErrorTyper
    if errors.As(err, &et) {
        ae.ErrorType = et.ErrorType()
        if detail, jerr := json.Marshal(err); jerr == nil {
            ae.ErrorDetailJson = detail
        }
    }
    return ae
}
```

`pkg/agentsdk/remote.go`'s 8 call sites (`RemoteAgent.Create`, `Read`,
`Update`, etc.) gain a shared helper that reconstructs a typed error from
`error_type`/`error_detail_json` when recognized, falling back to today's
plain `fmt.Errorf` otherwise:

```go
func reconstructAgentError(agentID string, ae *boxyagentv1.AgentError) error {
    base := fmt.Errorf("agent %q: %s", agentID, ae.GetMessage())
    switch ae.GetErrorType() {
    case "capacity":
        var ce providersdk.CapacityError
        if json.Unmarshal(ae.GetErrorDetailJson(), &ce) == nil {
            return &ce
        }
    }
    return base
}
```

Only one case exists today (`"capacity"`), but the mechanism generalizes to
any future typed driver error without another proto change.

---

## Section 2: #174 — reliable cleanup, orphan surfacing, quarantine

### Problem, precisely

1. `Create`'s two failure branches (`driver.go:216`, `:223`) call
   `_ = d.deleteBestEffort(...)` — the cleanup result is discarded.
   `deleteBestEffort` itself uses `-ErrorAction SilentlyContinue` on
   `Remove-VM`, a single attempt, no retry.
2. `Provisioner.Provision` (`provisioner_driver.go:36-39`,
   `provisioner_agent.go:50-53`) returns `model.Resource{}, err` on any
   `Create` error — no ID, even though the driver knows the VM name (or GUID)
   it just tried to create. `internal/pool/manager.go:583-588` then only calls
   `recordProvisionFailure` and returns; nothing is ever written to the store.
3. #133's existing `ResourceLister`/`ReconcileAgent` orphan-adoption path
   (already used by docker) does not close this gap even in principle: it
   only runs once, at agent registration (`internal/agentserver/server.go:236`),
   not on a live connection, and even when it adopts an orphan it writes
   `State: Unknown`, `OriginPool: ""` — which nothing in `manager.go` sweeps
   (`orphanedTransientResources` only acts on `Recycling`/`Destroying` states,
   and requires a matching `OriginPool`). Adoption alone is a dead end for
   cleanup, confirmed by reading `computeStale`/`orphanedTransientResources`
   directly.

### Fix 1: bounded, reliable cleanup with a typed failure carrying the ID

`deleteBestEffort` gains bounded retry — 3 attempts, 2s apart (same order of
magnitude as the existing `deleteWaitInterval` default of 3s) — instead of a
single silent-continue attempt.

When cleanup still fails after retries, `Create` returns a new typed error
instead of a plain one:

```go
// pkg/providersdk/errors.go
// OrphanedResourceError indicates Create failed and best-effort cleanup of
// the partially-created resource also failed, leaving it on the host outside
// Boxy's inventory. ID is the provider-native identifier a caller can use to
// record a quarantined resource so cleanup can be retried instead of the
// resource silently existing nowhere in Boxy's view.
type OrphanedResourceError struct {
    ID    string
    Cause error
}
func (e *OrphanedResourceError) Error() string { ... }
func (e *OrphanedResourceError) Unwrap() error { return e.Cause }
```

`Create`'s signature is unchanged — this only affects what the failure
*wraps*, not `Driver.Create`'s contract, so `docker`/`devfactory` are
unaffected.

### Fix 2: `Provisioner.Provision` records a quarantined resource on orphan

Both `DriverProvisioner.Provision` and `AgentProvisioner.Provision` gain an
`errors.As(err, &orphanErr)` check on `Create`/`agent.Create` failure. When
matched, instead of returning a bare error they return a resource with:

```go
model.Resource{
    ID:         model.ResourceID(orphanErr.ID),
    OriginPool: pool.Name,
    Provider:   model.ProviderRef{Name: providerName /* + AgentID for AgentProvisioner */},
    State:      model.ResourceStateError, // existing state, not a new one
    Properties: map[string]any{"quarantine_reason": orphanErr.Cause.Error()},
    CreatedAt:  now, UpdatedAt: now,
}, orphanErr // still an error — caller still records the provision failure/backoff
```

`internal/pool/manager.go`'s provision loop (`reconcileLocked`'s actuator,
`manager.go:583-599`) is adjusted to `PutResource` the quarantined entry even
on error, then still call `recordProvisionFailure` and return as today —
existing backoff and error-reporting behavior for the *pool* is unchanged;
this only adds the missing store write for the *orphaned VM itself*.

### Fix 3: quarantined orphans get swept and destroyed automatically

`ResourceStateError` resources with a non-empty `OriginPool` are folded into
the existing stale/orphan sweep in `reconcileLocked`
(`orphanedTransientResources`/`computeStale`, `manager.go:520-524`), reusing
`destroyAndMark` — already idempotent-safe per its existing doc comment. No
new sweep mechanism; this is the same "retry destroy every tick until it
disappears" path pool-owned stale resources already get.

### Fix 4: periodic agent reconciliation (defense-in-depth for the crash case)

For orphans the inline fix above can't see (e.g. an agent process crash
between `New-VM` succeeding and the failure branch running), hyperv
implements `providersdk.ResourceLister` — same convention as docker's:

```go
// pkg/providersdk/providers/hyperv/driver.go
func (d *Driver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
    // Get-VM | Where-Object Name -like 'boxy-*'
}
```

`internal/agentserver/server.go`'s post-registration reconciliation goroutine
(`server.go:233-239`) changes from a one-shot `pool.ReconcileAgent` call to
`Controller.Run(ctx, interval)` — the existing generic periodic-reconcile
primitive at `pkg/policycontroller/controller.go:128`, which
`pool.ReconcileAgent` already builds a `Controller` for internally. `ctx` is
already scoped to the agent's connection lifetime (the same `ctx` used in the
handler's `select` on `serveDone`/`forceStop`), so the loop stops naturally on
disconnect — no new lifecycle management needed. `interval` reuses
`s.heartbeatInterval` (already configurable, already the cadence the
connection runs on) rather than introducing a new constant.

`pool.ReconcileAgent` is refactored to expose its `Controller` (or gains a
`RunAgentReconciliation` sibling) so the server can call `.Run` instead of
`.Reconcile` once.

Resources adopted via this path still land as `State: Unknown`,
`OriginPool: ""` (unchanged from #133 — this design doesn't touch that
adoption's semantics, since an adopted-but-unexplained orphan isn't
necessarily create-failure fallout and auto-destroying it would be a separate,
riskier decision). They become visible in `boxy` status/inventory for an
operator to inspect, which satisfies the issue's "quarantine" language for
this rarer, crash-only case; only the *known-cause* case (Fix 1-3, a `Create`
call this exact process just attempted) gets fully automatic destroy.

---

## Section 3: #183 — narrow the reservation window (not close it)

No new signal is introduced — the issue's own analysis says one doesn't
exist today. Two bounded mitigations narrow both directions of the gap:

### Mitigation 1: release right after `Start-VM`, not at `Create`'s return

The create script currently does VHD/VM/switch/notes setup, `Start-VM`, then
a trailing `(Get-VM -Name '%s').Id.ToString()` — all as one PowerShell call,
with the reservation held until the whole thing (plus any error-path cleanup)
completes. The script splits into two `d.ps` calls: everything through
`Start-VM` under the reservation, then the ID lookup afterward, unguarded.
This shrinks the over-reservation window (a concurrent `Create`'s live query
already reflecting this VM's committed memory while this call still holds its
reservation) from "the whole `Create` duration" down to one fast `Get-VM`
call.

### Mitigation 2: grace-period release, biased toward the safe direction

`release()` no longer zeroes `reservedMB` synchronously at `Create`'s return.
It schedules the decrement 5 seconds later (a new `reservationGraceInterval`
constant, same order of magnitude as `defaultDeleteWaitInterval`) via
`time.AfterFunc`, independent of the caller's `ctx` (which may already be
cancelled by the time `Create` returns — the decrement is pure in-process
bookkeeping, not I/O, so it doesn't need one). This deliberately trades
under-reservation risk for
over-reservation risk: a stale-high `reservedMB` can only cause a spurious
`CapacityError` on an immediately-following sequential `Create` (annoying,
safe), never let one overcommit the host (dangerous) — the asymmetry the
issue itself frames as "which direction is worse."

### Residual gap (documented, not fixed)

A sequential `Create` arriving after the grace period elapses, before the OS
counter has actually caught up, can still overcommit — this is the same
underlying tension the issue describes, just with a smaller window. #183
stays open, updated with this analysis and the two mitigations shipped,
rather than being closed as fully fixed.

---

## Testing

- **#185**: round-trip test — driver returns `CapacityError`, assert
  `errorResult` populates `error_type`/`error_detail_json`, assert
  `RemoteAgent.Create` reconstructs a `*providersdk.CapacityError` with the
  original fields via `errors.As`.
- **#174**: `Create` failure + cleanup failure → assert
  `OrphanedResourceError` with correct ID; `Provisioner.Provision` on that
  error → assert a `ResourceStateError` resource is written with the right
  `OriginPool`; a follow-up `Reconcile` pass → assert the quarantined
  resource gets destroyed. `hyperv.Driver.List` → assert it filters to
  `boxy-*` names only. `Controller.Run`-based periodic sweep → existing
  `Controller` tests already cover `.Run`'s ticker semantics; add one
  hyperv-`ResourceLister` integration test analogous to docker's.
- **#183**: unit test for the grace-period release (reservation still counted
  immediately after `Create` returns, decremented only after the grace
  period elapses) and for the two-call script split (mock `psExec` asserts
  `Start-VM` is in the first call, `Get-VM ... .Id` in the second).

## Alternatives considered

- **Changing `Driver.Create`'s signature** to allow a non-nil partial
  `Resource` alongside a non-nil error (§Fix 1). Rejected: changes what
  `err != nil` means for every `Driver.Create` implementation (docker,
  devfactory), not just hyperv's failure path. A typed error is narrower.
- **Auto-destroying #133-adopted (`Unknown`/no-`OriginPool`) orphans too**
  (§Fix 4). Rejected for this pass: those orphans have no confirmed
  create-failure cause — could be a store data-loss artifact around a
  legitimate resource. Surfacing for operator review is the safer default;
  only orphans this process just tried to create and knows failed get
  automatic destroy.
- **Solving #183 fully** via a persisted per-VM reservation released on
  `Delete`. Rejected per the issue's own correction: this fixes
  under-reservation but makes over-reservation permanent for a VM's entire
  lifetime, not just the narrow window around `Create` returning.

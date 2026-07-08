# ADR 0004: Hyper-V Teardown Guard and Provisioning Backoff

Status: Accepted

## Context

A production incident (#118) reported that Boxy could leave Hyper-V in a
degraded state: a Boxy-managed VM got stuck in a transitional power state
("Turning Off") during teardown; Boxy's `Delete` path forced
`Stop-VM`/`Remove-VM` against it anyway; Hyper-V ended up with a stale
`vmwp.exe` worker and host-level VM management started failing. Boxy then
kept retrying provisioning against the same degraded host every 10-second
reconcile tick, hitting the same `New-VHD` failure repeatedly.

Two related but distinct problems needed fixing:

- The *destroy* path could actively make host state worse by forcing an
  operation against a VM that hadn't finished a prior transition.
- The *reconcile loop* had no way to back off from a provider/host that was
  already failing, so it hammered it every tick indefinitely.

## Decision

### Teardown guard (`pkg/providersdk/providers/hyperv/driver.go`)

`Delete` now reads the VM's current power state as part of its existing info
query. If that state is transitional (`Starting`, `Stopping`, `Saving`,
`Pausing`, `Resuming`, `Reset`), it polls (observe-only, never forces a
change) until the VM reaches a terminal state or disappears, bounded by a
timeout (default 30s, 3s interval). Only once the VM is confirmed terminal
does `Delete` proceed with the existing `Stop-VM -Force`/`Remove-VM -Force`
script. If the wait times out, `Delete` returns `ErrVMBusy` instead of
forcing removal — this is the direct fix for the incident: Boxy no longer
forces a destructive operation against a VM it can't confirm is safe to
touch.

`Create` also gained a pre-flight `Get-VMHost` probe (a VM-independent,
lightweight host health check) so a degraded host fails fast with a clear
error instead of every reconcile pass re-attempting `New-VHD` against the
same failure.

### Provisioning backoff (`internal/pool/manager.go`)

The pool `Manager` now tracks a per-pool failure count and next-allowed-retry
time, with capped exponential backoff (10s base, doubling, capped at 5
minutes). On a `Provision` failure during the background reconcile pass, the
pool enters backoff; on success, backoff state is cleared. While backoff is
active, the reconcile loop skips attempting to provision for that pool
(logged as a no-op decision), rather than retrying every 10s.

**`EnsureReady` (used by explicit, user/allocation-triggered calls) bypasses
backoff entirely** — only the ambient background `Reconcile` path
(`requireMinReady=false`) respects it. The reasoning: a background tick
retrying a known-broken host every 10s forever is the actual problem being
fixed; a human or an allocation request explicitly asking "is this pool
ready right now" deserves a live answer, not a stale "we're backing off"
no-op, and such calls are inherently rate-limited by whoever/whatever is
triggering them (not an unattended infinite loop).

## Consequences

- Deleting a VM stuck in transition now takes up to ~30s longer (the wait
  window) instead of failing immediately into a bad state — an intentional
  trade of latency for safety.
- A pool whose provider is failing will stop being retried automatically for
  up to 5 minutes at a time, rather than every reconcile tick. Operators
  running `boxy debug pool fill <pool>` (or `Fill`/`Drain` generally) are
  **not** shielded from backoff by this ADR's design — those call
  `reconcileLocked` with `requireMinReady=false`, the same as the background
  ticker, so a manual fill during an active backoff window will silently
  no-op rather than attempt and report why. This is a known, accepted rough
  edge (documented here rather than fixed) because those are rare, manually
  invoked actions that don't risk hammering a failing host the way an
  unattended 10s ticker does; revisit if this proves confusing in practice.
- Backoff state is in-memory only (per `Manager` instance, not persisted) —
  it resets on daemon restart. This is intentional: it's a mitigation for
  "don't hammer a host that just failed," not a durable circuit-breaker
  record that needs to survive restarts.

## Alternatives Considered

1. **Persist backoff state in the store.**
   Rejected for this pass: adds persistence/schema surface for a mitigation
   whose only job is to reduce retry frequency within a single daemon
   run — restart naturally clearing it is acceptable, and often desirable
   (an operator restarting the daemon is implicitly saying "try again").

2. **Apply backoff to `EnsureReady` and `Fill`/`Drain` too.**
   Rejected: `EnsureReady` explicitly needs a live answer to satisfy
   `MaxTotalReachedError`/`DrainedPoolError` semantics correctly; blocking it
   behind backoff would turn a fast, explicit capacity check into a silent
   no-op. `Fill`/`Drain` share `EnsureReady`'s call shape today
   (`requireMinReady=false`) and would need a distinct signal from the
   background ticker to opt out of backoff — deferred as unnecessary scope
   for this fix (see Consequences above).

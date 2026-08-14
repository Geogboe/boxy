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

### Memory preflight and reservation (#173)

`Create` queries the host's live available memory
(`Get-CimInstance Win32_PerfFormattedData_PerfOS_Memory`'s `AvailableMBytes`
— not the naive `Win32_OperatingSystem.FreePhysicalMemory`, which excludes
the reclaimable standby/cache list and routinely underreports what a new VM
can actually use) before running its `New-VHD`/`New-VM`/`Start-VM` script,
rejecting a request that can't fit with a typed `CapacityError` instead of
letting `Start-VM` fail with a raw `0x8007000E`. An in-process,
mutex-guarded reservation counter (`Driver.reservedMB`) makes this atomic
across concurrent `Create` calls on the same agent process — the mutex is
held across the live PowerShell query itself, not just the accounting,
trading a small amount of parallelism for a zero-TOCTOU-gap guarantee
*between concurrent calls*. It does not close the gap in either direction
across rapid sequential calls (see the design spec's "Known gap" and the
tracking follow-up for both the under- and over-reservation cases). The
query itself is time-bounded on top of (never beyond) the caller's context,
so a hung PowerShell call can't hold the mutex — and therefore every other
`Create` on this driver — indefinitely, even on a caller context with no
deadline of its own; the same bound applies to the pre-existing
`Get-VMHost` health probe immediately before it in `Create`'s hot path.
`defaultHostReserveMB` (512 MB,
unexported) is reserved for the host OS and other processes; it is not
currently user-configurable, because `boxy agent serve` has no
provider-config plumbing at all today (a pre-existing gap affecting every
provider, not just Hyper-V — see the design spec's "Known gap").

Full design: `docs/superpowers/specs/2026-08-12-hyperv-memory-preflight-design.md`.

### Create-failure cleanup, quarantine, and typed error propagation (#174, #185, #183)

`Create`'s cleanup-on-failure path (`deleteBestEffort`) now retries up to 3
times, 2s apart, and its own existence re-check (needed anyway, since
`-ErrorAction SilentlyContinue` masked whether `Remove-VM` actually worked)
resolves the VM's real GUID when cleanup still can't confirm it's gone.
`Create` returns a typed `*providersdk.OrphanedResourceError{ID, CauseMessage}`
in that case instead of a plain error. `Provisioner.Provision` (both the
embedded-driver and remote-agent variants) recognizes this type and writes a
`ResourceStateError` resource record — carrying the real GUID and the
originating pool — instead of the failure vanishing with no ID anywhere in
Boxy's store, which was the root cause of #174's "orphaned VMs the host
accumulates but Boxy never learns about." `pool.Manager`'s reconcile loop
picks up these quarantined records automatically via a new
`quarantinedOrphans` filter (parallel to, not a change to,
`orphanedTransientResources`/`isTransientDestroyState` — a quarantined
resource hasn't started teardown yet, which is that helper's documented
meaning) and retries destroying them the same way any other stale resource
is retried. If the underlying `Destroy` call keeps failing for a quarantined
resource, the pool's provision loop is blocked behind it until it either
succeeds or an operator intervenes — a known, deliberately undesigned
tradeoff, not a fix (see the design spec's Section 2, Fix 3).

As defense-in-depth for orphans this inline path can't see (e.g. an agent
crash between `New-VM` succeeding and the failure branch running), the
Hyper-V driver now implements `providersdk.ResourceLister` (same convention
docker already used for #133), and the post-registration reconciliation
sweep (`pool.ReconcileAgent`) now runs periodically for the life of an
agent's connection (`pool.RunAgentReconciliation`, on the connection's
heartbeat cadence) instead of once at registration only — closing the gap
where a long-connected agent (the realistic Hyper-V deployment topology; see
ADR-0005) never got re-audited.

`CapacityError` moved from `hyperv` to `providersdk` (aliased back for
compatibility) since it's not intrinsically Hyper-V-specific, alongside a new
`OrphanedResourceError` and a small `providersdk.ErrorTyper` interface. Both
typed errors now survive the `RemoteAgent`/gRPC boundary (#185): `AgentError`
gained `error_type`/`error_detail_json` fields (opaque JSON, mirroring
`CreateCommand.config_json`'s existing rationale, so a future typed error
never needs another proto change), and the quarantine mechanism above works
identically over that boundary — without it, quarantine would only have
worked for the in-process embedded-agent path, silently doing nothing on the
realistic remote-agent deployment.

Finally, #183's in-process memory-reservation window (the tension between
`Driver.reservedMB` and the live host-memory query around a `Create` call's
boundary) is narrowed, not closed — the issue's own analysis found no clean
full fix. The create script now releases its reservation immediately after
`Start-VM` succeeds rather than holding it through a trailing, now-separate
ID-lookup call, shrinking the over-reservation window from the whole `Create`
duration to one fast `Get-VM` call; and `release()` now delays its decrement
by a 5s grace period, deliberately biasing toward the safer failure direction
(a stale-high reservation can only cause a spurious, harmless
`CapacityError` on an immediately-following sequential `Create`, never let
one overcommit the host). A sequential `Create` arriving after that grace
period but before the OS counter catches up can still overcommit — #183
stays open, documenting this residual gap rather than being closed as fully
fixed.

Full design: `docs/superpowers/specs/2026-08-13-hyperv-create-failure-hardening-design.md`.

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

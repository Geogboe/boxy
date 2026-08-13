# Design: Hyper-V host memory preflight + reservation (#173, Phase A)

Status: Approved for planning
Date: 2026-08-12

## Problem

`hyperv.Driver.Create` builds a combined `New-VHD`/`New-VM`/`Start-VM`
PowerShell script and runs it without ever asking whether the host can
actually satisfy the VM's configured memory. When it can't, `Start-VM` fails
with a raw provider error (`0x8007000E`, "Not enough memory resources are
available to complete this operation") after Boxy has already committed to
the VHD/VM objects, and two concurrent `Create` calls can both pass a
point-in-time check (if one existed) and both lose the race against the host.

## Goal

Before running the create script, `hyperv.Driver` must:

- Know how much memory the host actually has free right now.
- Reject a request that can't fit with a clear, typed error instead of the
  raw PowerShell text.
- Atomically reserve memory across concurrent `Create` calls on the same
  agent process, so two requests that both individually fit can't both
  succeed against the same headroom.

## Non-goals (this PR)

Filed as separate follow-up issues, not silently dropped — see "Follow-ups."

- **Wire-level reporting.** Sending availability up to the server over the
  existing gRPC transport (e.g. on `Heartbeat`) so the server has live data.
- **Scheduler use.** `pool.Manager` picking an agent by reported
  availability. This overlaps with #124's job-scheduler design discussion
  and deserves its own dedicated brainstorm, not a rider on this bugfix.
- **`boxy status`/`boxy agent list` surfacing.** Already scoped out of #173
  before this design started.
- **The "stale inventory" observation** from the original issue (a ready
  resource existing while the request path attempted to create another VM).
  Already scoped out before this design started.
- **Making the host-reserve headroom user-configurable.** `boxy agent
  serve` has no provider-config plumbing at all today (see "Known gap"
  below) — a config field nothing can reach isn't a working feature.
- **Bringing `devfactory` to full parity as a general-purpose provider
  stub.** This PR gives it a minimal, config-driven `Availability()` stub
  (see "Components") — enough to be a second real implementer of the new
  interface. Auditing every optional interface every real driver implements
  and mirroring all of them in devfactory is a separate, unbounded project.

## Known gap surfaced by this work (not fixed here)

`boxy agent serve` (the standalone remote-agent CLI command) has no flag or
file for provider-level `Config` — `buildAgentDrivers` in
`internal/cli/agent_serve.go` always instantiates drivers with the
zero-value config. Only the embedded agent inside `boxy serve`
(`buildDrivers`, driven by `boxy.yaml`'s provider instances) actually
decodes config. This affects every provider with config fields, not just
Hyper-V (e.g. `docker.Config.Host` has the identical gap) — it's a
pre-existing architectural gap, surfaced here because it's the reason the
host-reserve headroom can't be a `Config` field yet. Worth its own issue.

`reserveMemory`'s reservation only closes the TOCTOU gap between `Create`
calls that overlap in time. `release()` runs as soon as `Create` returns
(success or failure), and the host's live free-memory counter is not
guaranteed to already reflect a just-started VM's consumption at that
instant — so two `Create` calls issued back-to-back (not concurrently, e.g.
during a pool fill from 0 to `min_ready`) can each pass their own live
preflight check and still overcommit the host. Found during PR review
(#182). A real fix means changing what the reservation lifecycle tracks —
e.g. holding it per-VM until `Delete` releases it, not until `Create`
returns — which is a bigger change than this PR's scope. Tracked as a
follow-up issue rather than fixed here; the primary failure #173 reports
(a host that can't fit even one more VM) is fixed regardless, since that
case is caught by a single `Create`'s own live query.

## Decisions confirmed during spec review

- `defaultHostReserveMB = 512`.
- The error type is `hyperv.CapacityError`, not `hyperv.HyperVCapacityError`
  — matches this codebase's convention of not repeating the package name in
  exported type names (`pool.MaxTotalReachedError`).
- `reserveMemory` holds its mutex across the full live PowerShell query
  (not just the accounting), fully serializing concurrent `Create` calls'
  capacity-check step on one host — accepted as the simplest zero-TOCTOU-gap
  option, not a bottleneck relative to `Create`'s existing sequential
  PowerShell round-trips.

## Design

### Architecture

A new optional interface in `providersdk`, `AvailabilityReporter`, that any
driver can implement — mirroring the existing optional-interface pattern
already in this codebase (`GuestPersonalizer`, type-asserted where needed
rather than forced onto every driver). Two real implementers land in this
PR: `hyperv.Driver` (real capacity data) and `devfactory.Driver` (a static,
config-driven stub) — enough to justify a shared interface without
speculating about a shape that has no consumer yet.

```go
// pkg/providersdk/availability.go
type ResourceAvailability struct {
    MemoryMB int64
}

type AvailabilityReporter interface {
    Availability(ctx context.Context) (*ResourceAvailability, error)
}
```

Reservation atomicity is *not* part of this interface — it's an internal
mechanism of `hyperv.Driver`'s own `Create`, not something any other layer
needs to know about. One agent process talks to exactly one Hyper-V host,
so the only thing that needs to serialize against concurrent `Create` calls
is that process's own in-memory state.

### Components

**`hyperv` package:**

- `defaultHostReserveMB = 512` — unexported constant, not a `Config` field
  (see "Known gap"). Headroom reserved for the host OS/other processes.
- `Driver` gains `mu sync.Mutex` and `reservedMB int64` (memory committed to
  in-flight `Create` calls that a live host query doesn't reflect yet).
- `func (d *Driver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error)`:
  queries live free memory via the existing `d.ps` seam — the same
  injectable PowerShell-exec function `checkHostHealth` already uses, so no
  new test-mocking mechanism is needed — using
  `(Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory`. This is the
  actual number the host has free right now, and already reflects every VM
  currently running (Boxy-managed or not, static or dynamic memory),
  because a running Hyper-V VM consumes real host RAM regardless of how it
  was started. **`FreePhysicalMemory` is reported in kilobytes, not
  megabytes or bytes** — the query result must be divided by 1024 before
  comparing against `requestedMB`/`defaultHostReserveMB`/`reservedMB`,
  which are all in MB. Nets out `defaultHostReserveMB` (confirmed at
  512 — see "Decisions") and the current `reservedMB` (read under the
  lock).
- `func (d *Driver) reserveMemory(ctx context.Context, requestedMB int64) (release func(), err error)`:
  locks, computes current headroom (live query minus `defaultHostReserveMB`
  minus `reservedMB`), and either returns `*CapacityError` or adds
  `requestedMB` to `reservedMB` and returns a `release` closure that
  subtracts it back out under the lock. Called from `Create`, released via
  `defer` on every exit path (success or failure) — on success, the next
  live query already reflects the new VM's real consumption, so releasing
  the temporary hold doesn't double-count anything; on failure, nothing was
  ever actually consumed, so releasing just undoes the reservation. **The
  mutex is held across the entire live PowerShell query**, not just the
  accounting — two concurrent `Create` calls on the same host fully
  serialize their capacity-check step rather than checking in parallel.
  Deliberate: it's the only construct with zero TOCTOU gap, and `Create`
  already does several more sequential PowerShell round-trips afterward
  regardless, so this isn't a new bottleneck relative to today.
- `CapacityError{RequestedMemoryMB, AvailableMemoryMB int64}` —
  modeled on `pool.MaxTotalReachedError`'s shape. `AvailableMemoryMB` is
  already net of the reserve and in-flight reservations, so the error
  doesn't need to separately expose the internal reserve constant.

**`devfactory` package:**

- `Config` gains `AvailableMemoryMB int64` (`yaml:"available_memory_mb"
  json:"available_memory_mb"`; 0 means unlimited, matching the zero-value-
  friendly pattern its other fields already use).
- `Driver` implements `Availability`, returning the configured value, or
  `math.MaxInt64` when `AvailableMemoryMB` is zero/unset (unlimited —
  consistent with `FailCreate`'s zero value meaning "don't fail").
- No `Create`-level enforcement — devfactory's `Create` doesn't decode a
  per-call memory request today, and adding that modeling purely to satisfy
  this PR would be scope creep on top of scope creep. This gives the
  interface a second, honest implementer without pretending devfactory
  enforces something it doesn't.

### Data flow (`hyperv.Driver.Create`)

1. Decode config, apply defaults (unchanged).
2. `checkHostHealth` (unchanged).
3. **New:** `release, err := d.reserveMemory(ctx, requestedMB)`. On
   insufficient capacity, return `*CapacityError` immediately —
   before `New-VHD`/`New-VM` ever run, so a doomed request doesn't churn a
   differencing disk or a VM object first.
4. `defer release()`.
5. Existing create script runs (`New-VHD`/`New-VM`/`Set-VM`/`Start-VM`).
6. On failure, existing `deleteBestEffort` still runs, then `release()`
   fires via defer regardless of outcome.

### Error handling

`CapacityError` is a plain Go error returned through
`providersdk.Driver.Create`'s existing `(*providersdk.Resource, error)`
signature — nothing generic needs to type-assert it specially yet (there's
no existing precedent of the pool layer inspecting driver-returned errors;
`MaxTotalReachedError`/`DrainedPoolError` originate from `pool.Manager`
itself, not from a driver call). A caller that wants to special-case it can
`errors.As` for `*hyperv.CapacityError`, same as any other typed
error in this codebase.

### Testing

- Extend `mockDriver`'s injected `psExec` function to switch on script
  content, the same pattern already required today (`checkHostHealth`'s
  probe and the create script both go through one mock function per test).
- Insufficient memory returns `*CapacityError` without the mock ever
  seeing a `New-VM` call.
- Concurrent `Create` calls (real goroutines) against a fake with limited
  free memory: only as many succeed as capacity allows — a genuine race
  test, same spirit as the `remote.go` TOCTOU regression tests from the
  `#153/#154/#158` session.
- A failed `Create`'s reservation doesn't leak: a second `Create` still
  succeeds after a first one fails.
- `defaultHostReserveMB` is subtracted correctly.
- `devfactory.Driver.Availability` returns the configured value, and
  `math.MaxInt64` when `AvailableMemoryMB` is unset.

## Follow-ups (file as separate issues after this ships)

1. Wire `AvailabilityReporter` data up to the server via `Heartbeat`,
   stored on `AgentRegistry`.
2. `pool.Manager` uses reported availability to pick agents — likely folds
   into #124's job-scheduler design rather than standing alone.
3. `boxy agent serve` has no provider-config plumbing at all (affects every
   provider, e.g. `docker.Config.Host`) — needed before `host_reserve_mb`
   (or any other Hyper-V config) can be made user-configurable on the
   realistic remote-agent deployment path.
4. Bring `devfactory` to full parity as a general-purpose stub for every
   provider capability (not just availability) — a separate, unbounded
   project.

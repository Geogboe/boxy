# ADR 0011: DevFactory persistence backend, config-relative provider paths, and provisioning-lock hardening

Status: Accepted

## Context

Three related, architecturally-significant decisions landed on the same
branch (#181 follow-ups, 2026-08) without an ADR at the time — a gap this
records after the fact. All three are also described in
`docs/superpowers/specs/2026-08-21-devfactory-parity-scope-design.md`
("Persistence backend and DataDir resolution" and "Follow-ups"); this ADR
is the durable record per this project's own convention (see AGENTS.md:
"When decisions are made, save them as ADR documents").

1. `devfactory`'s JSON state store (`pkg/providersdk/providers/devfactory/store.go`)
   originally wrote its file with a direct `os.WriteFile`, not the
   write-tmp-then-rename pattern `pkg/store/disk.go` already used for
   `state.json` — not atomic, and a crash mid-write could corrupt it.
2. Provider `Config` types sometimes have a filesystem path field
   (devfactory's `DataDir`) that's conceptually relative to the boxy config
   file it was declared in, but nothing resolved it that way — it silently
   resolved against whatever the process's ambient working directory
   happened to be, inconsistent with how `.boxy/state.json` itself resolves
   (`internal/cli/serve.go`'s `serveStatePath`).
3. Devfactory implementing `providersdk.ResourceLister` (#181) exposed a
   race: `Manager`'s provision actuator calls a driver's `Create` and only
   writes the resulting resource to the store afterward, while
   `ReconcileAgent`'s periodic sweep (#133/#174) treats any ID a driver's
   `List()` reports that the store doesn't yet know about as an orphan to
   adopt. A driver whose `Create()` writes its own internal state fast
   enough (devfactory, unlike docker or hyperv, returns in well under a
   millisecond) makes that window reliably hittable in principle.

## Decision

### 1. Generic atomic JSON store — `pkg/diskjson`

Added `pkg/diskjson.Store[T]`: a generic, mutex-guarded,
write-tmp-then-`os.Rename` JSON file store for a single value
(`Load`/`Save`/`Update`). It generalizes the same pattern
`pkg/store.DiskStore` already used, so a package that needs "persist one
JSON blob to disk safely" doesn't reimplement it (weaker) on its own.
`devfactory`'s store is rebuilt on it.

`diskjson.Store[T]` is deliberately stateless between calls (every call
re-reads from disk) rather than caching like `DiskStore` does — right for a
low-volume debug/reference store an operator might `cat`/`jq` mid-run,
wrong for `state.json`'s hot path. `pkg/store.DiskStore` itself has **not**
been migrated onto it — that's an open, not-yet-decided follow-up, not an
oversight.

### 2. `providersdk.RelativePathResolver`

Added an optional provider-`Config` capability,
`ResolveRelativePaths(baseDir string)`, detected by type assertion like
every other capability in `pkg/providersdk`. `Registry.NewDriverFromInstance`
calls it, if implemented, right after decoding config and before
constructing the driver, so a relative path resolves against the boxy
config file's own directory — not the process's ambient working directory.

`devfactory.Config.DataDir` implements it. This is opt-in per `Config`
type, not a default for every path-shaped field: docker's socket path and
hyperv's VHD/template paths are real host locations an operator points at
explicitly, not directories conceptually owned by the config file the way
devfactory's `DataDir` is.

Both real call sites are threaded with the correct `baseDir`:
`internal/cli/serve.go`'s `buildDrivers` (daemon, the `--config` file's
directory) and `internal/cli/agent_serve.go`'s `buildAgentDrivers` (remote
agent). The agent path has a subtlety: `agent service install --config
boxy.yaml` persists the resolved base directory into the installed
`service.yaml` (`agentServiceConfig.ProviderConfigsBaseDir`, added
2026-08-22) specifically so a later `agent serve --service-config
service.yaml` resolves relative provider paths against `boxy.yaml`'s
directory, not `service.yaml`'s own — those are different files in
different directories. An earlier version of this fix recomputed the base
directory from `service.yaml`'s own path at serve time, which was wrong
whenever an operator supplied `--config` at install time; that gap is what
`ProviderConfigsBaseDir` closes.

### 3. Provisioning-lock hardening — `pool.LockedProvisioner`

Added `internal/pool.LockedProvisioner`, an optional `Provisioner`
capability implemented by `AgentProvisioner.ProvisionLocked`, and
`AgentRegistry.LockProvisioning` (implements `pool.ProvisionLocker`) — a
per-agent `sync.Mutex` keyed by agent ID.

`ProvisionLocked` acquires `AgentRegistry.LockProvisioning(agentID)`
**before** calling the agent's `Create`, holds it through a `persist`
callback `Manager` supplies (which finishes building the resource and
writes it to the store), and releases only after `persist` returns.
`ReconcileAgent`'s sweep acquires the same per-agent lock across its
combined `List()`-and-store-read snapshot. Whichever side runs first, the
other sees a fully settled view.

The lock is deliberately acquired *inside* `ProvisionLocked`, not by
`Manager` beforehand: `Manager` doesn't know which agent a pool will
resolve to until the provisioner resolves it, and for `AgentProvisioner`
that resolution is itself stateful and non-repeatable
(`AgentRegistry.Resolve` round-robins across every agent advertising a
provider type, so resolving twice for one `Provision` call could silently
select two different agents). Only the provisioner can correctly acquire a
lock keyed to the exact resolution its own `Create` call will use.

`Manager.Provision`'s actuator prefers `ProvisionLocked` via a type
assertion; a `Provisioner` that doesn't implement it (the deprecated
`DriverProvisioner`, or a test fake) falls back to acquiring
`ProvisionLocker` only around `Manager`'s own store write — a narrower
guarantee, since such a provisioner has no per-agent concept for the lock
to protect `Create` with in the first place.

An initial version of this fix (2026-08-21) acquired the lock only around
`Manager`'s store write, immediately before `PutResource`, not around
`Provision`/`Create` itself — leaving the exact window described in
Context §3 open for a fast `ResourceLister` driver, since `List()`
visibility happens inside `Create`, before `Manager` regains control to
acquire anything. `TestAgentProvisioner_ProvisionLocked_HoldsLockThroughCreate`
(added 2026-08-22) proves the corrected scope: it fails against the
narrower version and passes against the current one.

### Whether the race was ever observed live

Investigated directly: an initial "reproduction" turned out to be an
artifact of a relative `data_dir` resolving against the wrong process CWD
(fixed by decision §2 above), contaminating results across runs. A proper
isolated A/B repro (patched vs. unpatched, absolute `data_dir`, full state
cleanup between runs) found **zero** live occurrences either way — a cold
`boxy serve` start runs pool initialization synchronously before the
reconciliation goroutine starts, closing the window at startup. What
remains true, proven with real goroutines and a real mutex
(`TestReconcileAgent_HoldsProvisionLockAcrossItsSweep` and the
`ProvisionLocked` test above), is that the two code paths can interleave
dangerously during any *ongoing* reconciliation tick after startup — the
periodic ticker in `RunAgentReconciliation` runs for the daemon's entire
lifetime, not just once at cold start, so the window recurs on every tick
that happens to land during a fast driver's `Create` call. This is
defensive hardening for a real, live-during-normal-operation window, not
only a hypothetical future-refactor risk.

## Non-goals / explicitly out of scope

- `pkg/store.DiskStore` was not migrated onto `pkg/diskjson.Store[T]` —
  open follow-up.
- `RelativePathResolver` was not made a default capability for every
  path-shaped provider config field — opt-in per `Config` type only.
- The provisioning lock does not attempt to serialize provisioning across
  *different* agents, or protect against races with anything other than
  `ReconcileAgent`'s sweep for the same agent ID.

## Consequences

- `Registry.NewDriverFromInstance`'s signature gained a `baseDir string`
  parameter — a source-breaking change to a public `pkg/providersdk` API,
  accepted because both real call sites needed updating anyway and the
  package has no external consumers yet to break.
- `AgentProvisioner.Provision` now always acquires and releases
  `AgentRegistry.LockProvisioning` around its `Create` call (as a thin
  wrapper over `ProvisionLocked` with no `persist` callback) — negligible
  overhead on an uncontended mutex, but every caller of `Provision`
  directly (not just `Manager`) now gets the same lock protection.
- A persist failure *after* a successful `Create` (e.g. a transient store
  write error) is recorded differently from a `Create` failure itself:
  `Manager` uses `ProvisionLocked`'s `created` return value, not its `err`
  return value, to decide between `recordProvisionSuccess` and
  `recordProvisionFailure` — a persist-only failure must not count against
  the pool's provisioning backoff the way a real `Create` failure does.

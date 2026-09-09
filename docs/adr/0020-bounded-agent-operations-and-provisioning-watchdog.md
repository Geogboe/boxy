# ADR-0020: Bounded agent operations and a stuck-provisioning watchdog

## Status

Accepted (2026-09-09).

## Context

Two related production bugs (#333, #337) were filed against live Hyper-V
runs: a hung remote-agent RPC (`Create`, `PersonalizeGuest`, `Delete`) could
silently freeze the entire sandbox create/delete path with no timeout, and a
resource stuck mid-personalization had no watchdog to recover it. These are
the same failure class at two layers — an unbounded call, and no backstop if
bounding it still leaves a resource stranded — so they were designed
together to keep their recovery actions from racing each other.

Two blocking points needed different safe recovery behaviors:

1. **Admission-time personalize** (`AdmissionHandler.Handle` during
   `Prepare`/`EnsureReady`). A timeout here already surfaces as an ordinary
   error, routed to `ResourceStateError` by existing plumbing — no new
   rollback-safety concern.
2. **Allocation-time personalize** (`AgentProvisioner.Allocate` during
   `Transaction.Fulfill`). A timeout here previously triggered
   `Fulfiller.rollbackAllocation`, returning the resource to Ready inventory.
   That is unsafe once a guest-credential rotation may be in flight: per
   ADR-0010, the guest's real credential state after a timed-out rotation is
   unknown, so handing a possibly-rotated-credential resource to the next
   caller risks giving them a resource they can't authenticate against, or
   worse, a stale credential still valid for the *previous* caller.

## Decision

### Per-operation-class timeouts, not one global knob

`internal/config.AgentTimeoutsSpec` (`server.agent_timeouts` in config)
bounds `Create` (5m default), `PersonalizeGuest` (3m default), `Delete` (2m
default), and a reserved `Default` (30s, no current consumer) — separately,
because provisioning a whole VM/container and rotating a guest credential
have very different realistic durations. `internal/pool.AgentProvisioner`
wraps each bounded call site with `context.WithTimeout` using resolved
`time.Duration` values (`AgentOperationTimeouts`), not raw config strings —
keeping `internal/pool` decoupled from `internal/config`, the same pattern
already used for other injected fields like `Now func() time.Time`.

`ProvisionLocked`'s `agent.Create` call is bounded specifically because it
runs while holding `LockProvisioning` (ADR-0011) — an unbounded hang there
blocks `ReconcileAgent`'s orphan sweep too, not just the caller.

### Quarantine, not rollback, on an allocation-time personalize timeout

An allocation-time `PersonalizeGuest` timeout now quarantines the resource
(`ResourceStateError`, with the stored credential deleted) instead of
returning it to Ready inventory. `GuestPersonalizationTimeoutError`
(`internal/pool/agent_timeouts.go`) carries resource ID, pool, agent ID,
elapsed time, configured timeout, and whether the credential was
successfully deleted — this is a safety-relevant event, not a routine
failure, so every field needed to investigate it without cross-referencing
other events is on the error itself.

### Fulfiller loop isolation

Bounding the RPCs is necessary but not sufficient: a single fulfiller pass
iterating N sandboxes, each taking minutes to time out, could still starve
the deleter/pool-reconcile/session-sweeper for that whole window. Each
`reconcileSandbox` call is wrapped in its own `context.WithTimeout`, derived
from `AgentTimeoutsSpec.EffectiveSandboxFulfillTimeout()` — the *sum* of
Create+PersonalizeGuest+Delete, not the reserved `Default` field, since a
single small bound would fire before a cold pool's own `Create` even
completes. A stuck sandbox logs and the pass continues; the whole pass still
bails cleanly if its own outer context is cancelled.

### The watchdog is a distinct backstop, not a duplicate of the timeouts

`server.pool_provisioning_watchdog_threshold` (default 15m) finds resources
still `Provisioning` after that long and destroys-and-replaces them via the
existing `destroyAndMark` path (already deletes guest credentials) — not
`retryRetainedResource`, which would reset the resource's `UpdatedAt` and
make the watchdog never re-fire for a resource that's genuinely stuck.
Replacement provisioning falls out for free from the existing `toProvision`
loop in the same reconcile pass.

**Invariant, enforced at config validation:** the watchdog threshold must
exceed the largest configured timeout, *including* the derived
sandbox-fulfillment bound — not just the raw per-operation timeouts. If the
watchdog could fire before a legitimately-slow-but-still-progressing
operation's own timeout would, the two recovery paths would contend for the
same resource. This is why the watchdog isn't simply "a bit longer than
`PersonalizeGuest`" — it has to be longer than the *sum* a single fulfiller
pass can legitimately take.

## Consequences

- A hung agent RPC now fails fast and predictably instead of freezing a
  whole reconcile/fulfillment pass indefinitely.
- An allocation-time timeout trades resource availability (the resource is
  quarantined, not recycled back to Ready) for credential-safety — consistent
  with ADR-0010's existing risk posture on guest credentials.
- The watchdog is a true backstop for a class of failure the timeouts don't
  cover (e.g. a resource whose state update was lost to a crash mid-op), not
  a second layer of the same protection — the validated threshold-vs-timeout
  invariant keeps the two from disagreeing about whether a resource is
  actually stuck.
- No live-Hyper-V validation was possible from the development host (see
  AGENTS.md's "This development host cannot run Hyper-V VMs" note); all of
  the above was validated with devfactory pools, fake guest executors, and
  timing-controlled unit/integration tests — not live guest timing.

## Change log

- 2026-09-09: Initial decision, implemented as part of the post-0.1.66 batch
  (PR #362). Filed follow-up #350 for one non-blocking design gap surfaced
  during implementation: a fixed per-sandbox timeout doesn't scale with
  resource count in a multi-resource sandbox request — scoped separately,
  not a regression of this decision.

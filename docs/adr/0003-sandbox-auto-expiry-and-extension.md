# ADR 0003: Sandbox Auto-Expiry and Extension Semantics

Status: Accepted

## Context

`model.SandboxPolicies.AutoDestroyAfter` (e.g. `"30m"`, `"8h"`) has existed in
the sandbox model since early on, but was never read by anything outside
tests — sandboxes never actually expired regardless of what a caller set.
Fixing this (tracked as the real remaining gap in #34) required deciding:

- Where expiry state lives and when it's computed.
- What "extend" means: reset the clock from now, or push the existing
  deadline further out?
- Whether expiry needs its own background worker, or can reuse something
  that already exists.
- How this interacts with the daemon's existing async-deletion flow
  (`internal/sandbox/deleter.go`, added for #121's async sandbox delete
  lifecycle).

## Decision

1. **Compute an absolute `ExpiresAt` at creation time**, not a relative TTL
   checked on read. `model.Sandbox.ExpiresAt *time.Time` is set once, from
   `Policies.AutoDestroyAfter`, when the sandbox is created
   (`sandbox.Manager.CreateRequested`/`CreateFromPool`). Nil means no expiry.
   Invalid or non-positive durations are rejected at creation time (fail
   fast), not silently ignored.

2. **Reuse the existing `DeletionReconciler`, don't add a new background
   loop.** `internal/sandbox/deleter.go` already runs every 10s (via the same
   `serveReconcilePass` ticker that drives pool reconciliation) to clean up
   sandboxes marked `SandboxStatusDeleting`. It now also checks, before its
   normal cleanup pass, whether any non-deleting sandbox's `ExpiresAt` has
   passed; if so, it flips that sandbox to `Deleting` and falls through into
   the same cleanup path. One reconciler, one responsibility ("make sandbox
   state converge toward not-existing when it should"), rather than two
   loops that both mutate sandbox lifecycle state.

3. **`extend` pushes the deadline out from the current `ExpiresAt`, not from
   `now`.** `RequestExtend(id, duration)` computes `newExpiry =
   sb.ExpiresAt.Add(duration)`. This means calling extend twice compounds
   (two 15-minute extends add 30 minutes total), which matches the mental
   model of "give me more time" rather than "reset my time budget" — the
   latter would let a caller who extends slightly late lose some of their
   remaining budget silently.

4. **A sandbox with no expiry cannot be extended** (`ErrNoExpiry`, HTTP 409).
   There's no sensible duration to extend *from* if `AutoDestroyAfter` was
   never set, and silently starting a fresh countdown from `now` would be a
   surprising side effect of calling `extend`.

## Consequences

- **Behavior change for existing users**: anyone who already had
  `auto_destroy_after` set in a sandbox request, assuming it did nothing
  (because it never did), will now have that sandbox actually expire. This
  is called out in the PR/release notes, not silently shipped.
- Expiry resolution is bounded by the reconcile tick interval (10s), same as
  every other daemon-driven state transition in this codebase — acceptable
  for "auto-destroy after N minutes/hours," not intended for sub-second SLAs.
- `boxy sandbox extend <id> <duration>` was added to the *existing* `sandbox`
  command tree rather than a new `boxy client` command surface, deliberately:
  #124 (a same-session design discussion) proposes reframing "sandbox"
  toward a job/scheduler model, and standing up a brand-new CLI surface with
  today's vocabulary right before that decision risks near-term rename
  churn. The existing `sandbox` tree isn't going anywhere regardless of how
  #124 resolves, so extending it was the lower-risk choice.

## Alternatives Considered

1. **Reset expiry to `now + duration` on extend.**
   Rejected: makes "extend" behave inconsistently depending on how close to
   expiry the caller is, and a caller who calls extend "too early" would
   lose time compared to calling it right before expiry.

2. **A dedicated expiry-sweeper goroutine, separate from `DeletionReconciler`.**
   Rejected: would duplicate the same "list sandboxes, decide, mutate
   status" shape that already exists, for no isolation benefit — both
   ultimately do the same thing (drive a sandbox toward deletion).

# Release progress notes (2026-09-04, next-release-pool-ops)

Working notes for the in-progress next-release work, kept so a session that
gets interrupted can resume without re-deriving state. Not a design doc —
delete or fold into commit history once the release ships.

## Branch / commits so far

Branch: `codex/next-release-pool-ops`. Checkpoint commits from before this
session (see AGENTS.md-adjacent context) plus, this session:

- `e5e4cf8 feat(pool): add Save-and-Apply configuration API and UI`
- `37ea8c0 feat(pool): add opt-in retain-for-debug retry, correct spec text`

## Decisions made this session (user-confirmed)

1. **Issue #306 (package-manager bootstrap)**: SKIP implementation. It's
   `research`-labeled with explicit "design required before implementation"
   language and unresolved security questions (installer trust, checksum
   policy, credential handling, rollback). Verified existing code has no
   bootstrap capability anywhere (matches the issue's own description of
   current behavior) — nothing further to do here this release.
2. **Retry semantics spec/code contradiction**: the spec said manual Retry
   reuses the same VM/credential; the code always destroyed+recreated.
   Resolved by (a) correcting the spec text to describe the real default
   (destroy-and-recreate), AND (b) adding a genuinely new opt-in feature the
   user asked for: `policy.debug.retain_failed_resources` (local-config-only
   pool policy). When set, `FailAdmission` leaves the VM/credential alive
   instead of tearing down, and `RetryResource` re-admits the retained
   resource in place instead of destroying+recreating. Done, tested,
   committed (`37ea8c0`).
3. **Diagnostics gap**: user chose the full option — wire jobs->diagnostics
   producer bridge, add the missing `Provider` field end-to-end, and
   instrument the hyperv driver with structured events. This is IN PROGRESS
   (see below) — the largest remaining piece of work.

## Key architectural discovery (diagnostics)

`boxy serve` (internal/cli/serve.go:242) sets the **global default slog
logger** to `diagnostics.NewHandler(...)`. This means ANY `slog.Info/Warn/
Error` call anywhere in the server process, using recognized attribute keys
(see `pkg/diagnostics/handler.go`'s `safeField()`: component, pool, agent,
resource, request, operation, job, step, status, attempt, error_code,
error_summary), automatically lands in the diagnostics store with no extra
plumbing. There is NOT yet a `provider`/`provider_type` case in `safeField()`
or a `Provider` field on `diagnostics.Event` — that's the mechanical gap.

The actual producer gap: `pkg/jobs.jobReporter.Record` (pkg/jobs/jobs.go:397)
only appends to `job.Steps`; it never calls slog. The two call sites that
drive pool job steps are `internal/server/api_pools.go`:
`startPoolMaintenanceJob` (Fill/Drain, ~line 246) and `startPoolResourceJob`
(Retry/Destroy, ~line 190) — both build a `jobs.Step` and call
`reporter.Record(...)`. Neither currently also emits a diagnostics-shaped
slog call, and neither is the job's true "nested per-resource steps" source
(that would need to reach deeper into `internal/pool.Manager`'s
reconcile/admission code, which doesn't currently know which job (if any)
triggered it).

## Plan for the diagnostics bridge (not yet fully implemented)

1. `pkg/diagnostics`: add `Provider` field to `Event`, add `"provider"`/
   `"provider_type"` case to `safeField()`, thread through `Query` filtering,
   `export.go`, and both UI templates
   (`internal/server/templates/diagnostics.html`,
   `internal/server/ui_diagnostics*.go`).
2. `pkg/jobs`: add a `JobID() ID` method to the `Reporter` interface (still
   fully domain-neutral — no pool/agent/provider knowledge) so callers can
   correlate their own slog calls to the job.
3. `internal/server/api_pools.go` (and `api_agents.go` for agent-log jobs):
   at job start/success/failure, emit `slog.Info`/`slog.Warn` with
   component="pool" (or "agent"/"diagnostics" for the log-pull job),
   operation=kind, job=reporter.JobID(), pool=poolName, resource=subject
   (for resource jobs), step=step.Code, status=step.Status,
   attempt=step.Attempt, error_code=step.ErrorCode. This alone gets every
   job kind showing up in the diagnostics timeline correctly grouped by job.
4. For real *nested per-resource* steps inside a Fill job's story (not just
   the outer start/success/fail), the job ID needs to reach
   `internal/pool.Manager`'s reconcile/admission/hyperv code. Plan: thread it
   via `context.Context` using a small neutral helper (e.g.
   `pkg/diagnostics.WithJobID`/`JobIDFromContext`, or a bound `*slog.Logger`
   stored in context) set once in the API job handler and read wherever
   pool/admission/hyperv code already has access to `ctx` when it logs.
   Pick a few high-value instrumentation points rather than every line:
   resource provisioning start/success/fail, `FailAdmission`,
   `RetryResource`, `DestroyResource`, blocked-pool detection.
5. `pkg/providersdk/providers/hyperv` + `pkg/providersdk/guest_personalization.go`:
   add structured slog calls (component="hyperv") at password rotation,
   memory admission failure/retry, template source digest mismatch, and
   guest-personalization failure — using the same phase/operation/step/
   status/error_code vocabulary.
6. Add/extend tests: `pkg/diagnostics` for the new field and filtering,
   `internal/server` for the job-step-emits-diagnostics-event behavior
   (something like capturing slog output via a test handler, or asserting
   against a fake `diagnostics.Store` if the server test harness already
   wires one), and whatever hyperv unit tests already fake the driver's
   guest executor.
7. Run `internal/server/ui_diagnostics_timeline_test.go`-style coverage —
   this file doesn't exist yet per the audit; consider adding minimal
   coverage for the nested-story grouping function if time allows.

## Diagnostics bridge: done (2026-09-04, later in session)

Completed in three commits:

- `a27ba11 feat(diagnostics): add spec-required Provider field end-to-end`
- `655c614 feat(diagnostics): bridge pool and agent job steps into diagnostics`
- `4ad2796 feat(hyperv): emit structured diagnostics for personalization/memory failures`

What's now real: `pool.fill`/`pool.drain`/`pool.retry`/`pool.destroy`/
`agent.logs` jobs emit started/succeeded/failed structured events
correlated by job ID (`internal/server/api_pools.go`'s `logPoolJobStep`,
`api_agents.go`'s `logAgentJobStep`, both riding the pre-existing global
slog->diagnostics bridge from `internal/cli/serve.go:242`). `hyperv.Driver`
emits classified personalization failures (network_apply/rotate/verify/
prepare) and memory-admission failures. `Provider` is a first-class field
end-to-end (Event/Query/export/both UI templates).

**Known, deliberate scope boundary**: per-resource Hyper-V events
(`personalizeGuestLocked`'s events, memory-reserve events) do NOT carry the
triggering job's ID, so they don't nest under a Fill job's story in
`buildDiagnosticsTimeline` (`internal/server/ui_diagnostics_timeline.go`) —
they show as their own flat/individual entries instead, correlated by
`resource`/`pool` rather than `job`. Doing better requires threading the job
ID through `internal/pool.Manager`'s reconcile/admission call chain down
into the hyperv driver via context, touching many function signatures. Ruled
out this session as materially more invasive for a further nice-to-have,
not the "story renders completely empty" gap the audit actually flagged
(that gap is fixed: the outer Fill/Retry/Destroy/agent-log job itself now
always produces a real story). Revisit as its own follow-up if wanted.

## Other audit findings, not yet actioned

- Agent-log-pull diagnostics UI refresh uses a full-page `location.reload()`
  every 1.5s (`internal/server/templates/diagnostics.html:4`) instead of
  HTMX like the rest of the UI. Minor, cosmetic-consistency-only; low
  priority relative to the producer-bridge gap above.
- `internal/server/ui_diagnostics_timeline.go` has no dedicated test file.

## Also landed this session, not in the original plan

- **Issue #336** (BLOCKING, user-requested mid-session): hyperv
  `PersonalizeGuest` had no per-resource lock, so concurrent invocations
  (preheat + allocation, or any retry) could race the same guest's
  network-apply/rotation sequence and strand a VM on APIPA with its
  original password. Fixed with a per-VM-ID mutex map
  (`Driver.personalizeLocks`), cleaned up on confirmed `Delete`. Commit
  `60f7840`, includes a concurrency regression test.

## Remaining checklist from the original task (unchanged from the request)

- Diagnostics bridge: DONE (see above) modulo the one deliberate scope
  boundary noted above.
- UI validation pass with Playwright/Firefox per the original task's screenshot
  list — NOT STARTED yet this session.
- Full validation gate (`task fmt/generate/test/lint/ci:validate`, WSL race
  script, Firefox E2E, PII/secrets) — NOT STARTED yet this session (only
  focused package tests have been run so far, after each slice).
- Manual Hyper-V validation on `wks01` — NOT STARTED yet this session.
- PR #330: confirmed its Windows failure
  (`TestRemoteAgent_Availability_LaterHeartbeatWhollyReplacesSnapshot` in
  `pkg/agentsdk`) is pre-existing/unrelated (also failing on recent `main`
  pushes, and PR #330's diff never touches `pkg/agentsdk`). Left untouched
  per instructions — no fix attempted, not in scope for this batch.
- Dependabot PR #307: confirmed already merged into `main`. No action needed.
- Ship-it workflow (merge/build/sign) — NOT STARTED; do only after the above
  is green and the user has explicitly signed off, per this repo's
  release-cadence conventions (AGENTS.md "Release cadence" section).

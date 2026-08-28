# AGENTS.md

If `AGENTS.override.md` exists, read it after this file for host-specific
development guidance. It is intentionally gitignored and must not be committed.

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Project

- **Module:** `github.com/Geogboe/boxy`
- **Go version:** 1.25
- **Dependencies:** cobra (CLI), yaml.v3 (config parsing), go-keyring (OS credential storage)

## Issue Tracking

Features, bugs, and roadmap items are tracked as GitHub issues on `Geogboe/boxy`. There is no separate roadmap file — GitHub issues are the single source of truth for planned work. Use `gh issue list` to see current priorities and `gh issue view <number>` for details.

## Commands

**Always check `Taskfile.yml` for existing tasks before running raw commands.** Use `task <name>` instead of raw `go test`, `golangci-lint run`, etc.

```bash
task build            # Build ./boxy binary
task test             # Run all tests
task ci:validate      # Run all CI-equivalent checks locally before pushing
task lint             # Run golangci-lint (same as CI)
task secrets:scan     # Scan git history with Betterleaks
task pii:scan         # Scan git history for non-controlled PII
task pii:scan:stdin   # Scan piped public text for non-controlled PII
task pii:authors      # Report Git author identities for separate review
task fmt              # Format all Go source files
task serve            # Run boxy serve (daemon mode)
task serve:once       # Run boxy serve --once (single reconciliation pass)
task skills:check     # Run bundled skill drift/install checks
task skills:install:dev # Install bundled skill into ./.tmp/skills for inspection
task go:run -- <args> # Run boxy via go run with arbitrary args
task release:check    # Validate GoReleaser config via the pinned tools module
```

## Project Structure

```
cmd/
  boxy/               # Main CLI entry point
  devfactory/         # DevFactory provider standalone CLI (reference/testing)
  schema-gen/         # JSON schema generator for config files
internal/
  cli/                # CLI command implementations
  config/             # Configuration parsing and pool/sandbox specs
  pool/               # Pool manager and provisioner
  sandbox/            # Sandbox manager and ID generation
  skills/             # Bundled coding-agent skill assets and installer/link logic
pkg/
  agentsdk/           # Agent interface (embedded or remote)
  model/              # Core domain models (Resource, Pool, Sandbox, Profile)
  policycontroller/   # Reconciler that maintains desired pool state
  providersdk/        # Provider driver SDK, registry, and built-in drivers
  resourcepool/       # Resource pool primitives
  store/              # Data persistence (memory and disk backends)
examples/             # Example configuration files
docs/adr/             # Architecture Decision Records
```

## Architectural Notes (Living)

### Pools and Resources

- Pools are homogeneous inventories of resources (see `pkg/model/pool.go` and `pkg/model/resource_collection.go`).
- **Resources are single-use:** when a resource is allocated into a sandbox, it is never returned to a pool. (ADR-0002)
- Docker pool provisioning auto-pulls a configured image when it is missing locally; first-run Docker pools should not require a manual `docker pull`.
- `model.Resource.OriginPool` is immutable provenance: it records which pool provisioned the resource, and `pool.preheat.max_total` is enforced against all non-destroyed resources with that origin, not just current ready inventory.
- The daemon reconcile loop runs pool reconciliation both before and after sandbox fulfillment so preheat targets are restored in the same tick after allocations drain a pool.
- `computeToProvisionCount` (`internal/pool/planning.go`) measures the min_ready gap against `totalCount` (all non-stale, non-destroyed resources tracked for the pool), not `readyCount` (only `ResourceStateReady`). A resource this pool already provisioned but that hasn't reached `Ready` yet (mid-admission — e.g. guest personalization via `AdmissionHandler`) is real inventory-in-progress and must count toward the target; comparing against `readyCount` alone made every reconcile tick before admission finished re-request the *full* remaining gap, overshooting `min_ready` by one extra resource per tick until `max_total` capped it (#258, 2026-08).
- Resource destroy paths (recycle-by-max-age, drain, sandbox-triggered `DestroyResource`) persist a transient state (`recycling` or `destroying`) *before* calling the provisioner, so a resource mid-teardown is observable via the REST API (`GET /api/v1/resources[/{id}]`) and `.boxy/state.json` instead of just vanishing once the destroy completes. No CLI command surfaces individual pool-resource state today (`boxy status` only aggregates ready-inventory counts; `boxy debug pool` only has `drain`/`fill`) — that's deliberately out of scope, see ADR-0006's Non-goals. An orphan sweep in `internal/pool/manager.go` recovers resources left stuck in a transient state by a crash; both sweep sites cover both transient states (safe because every driver's `Delete` is idempotent on an already-gone resource) — see ADR-0006 for why an earlier, narrower split turned out to leave a real recovery gap.

### Sandboxes

- A sandbox is an environment that can be as small as a single resource or as large as a full lab.
- Sandbox creation via the REST API is asynchronous: `POST /api/v1/sandboxes` persists a sandbox request in `pending`, the daemon fulfillment loop provisions/allocates resources, and the sandbox transitions to `ready` or `failed`.
- `POST /api/v1/sandboxes` accepts typed `requests`, not sandbox-spec `resources`; the handler rejects unknown fields so stale clients fail fast instead of silently sending the wrong shape.
- `boxy serve` persists runtime state in `.boxy/state.json` next to the active config (or under the working directory when no config file is used), so accepted async sandbox requests survive normal daemon restarts.
- `boxy sandbox create -f ...` is daemon-backed: the CLI loads a sandbox spec, resolves named pools from the daemon pool catalog, submits async `requests`, and waits for `ready`/`failed` by default. Use `--no-wait` to return after the daemon accepts the request.
- `Policies.AutoDestroyAfter` (`auto_destroy_after` in requests) is enforced (as of 2026-07): sandbox creation computes a real `ExpiresAt`, and the existing async-deletion reconciler (`internal/sandbox/deleter.go`, ticked every 10s alongside everything else) promotes expired sandboxes into deletion. `POST /api/v1/sandboxes/{id}/extend` / `boxy sandbox extend <id> <duration>` push the deadline out, compounding from the current `ExpiresAt` rather than resetting from now. See ADR-0003.
- `POST /api/v1/sandboxes/{id}/exec` and `boxy sandbox exec` execute non-interactive commands only in ready sandboxes. Single-resource sandboxes select implicitly; multi-resource sandboxes require `resource_id`/`--resource`. `stream=true`/`--stream` gives live NDJSON with bounded output and timeout enforcement; interactive stdin/PTY is intentionally out of scope. See [ADR-0008](docs/adr/0008-streaming-command-execution.md) for the full status-code contract (413/504/500), streaming-vs-buffered semantics, and a known test-coverage gap in `pkg/psdirect`.
- Preferred phrasing when describing compositions:
  - "container sandbox" (1 container)
  - "3 VM lab sandbox" (multi-VM lab)
  - "2 container, 3 VM, 1 share sandbox" (heterogeneous composition)

### Three-Mode Binary

```
boxy serve              # Daemon: pool reconciliation, REST API, gRPC server
boxy <command>          # CLI: talks to daemon via REST
boxy agent              # Agent: distributed, connects to daemon via gRPC
```

### Provider / Driver / Agent Model

- **Provider**: an external system that supplies resources (Docker, Hyper-V, etc.)
- **Driver**: code implementing CRUD operations for a specific provider type (`pkg/providersdk/driver.go`)
- **Agent**: execution layer for driver operations — can be embedded (in-process) or remote (gRPC) (`pkg/agentsdk/`)
- **PolicyController**: reconciler that compares desired vs actual pool state and issues driver operations (`pkg/policycontroller/`)
- **`pkg/agentsdk` (live) vs. a config-declared `agents:` list (removed, 2026-07)**: don't confuse these. `pkg/agentsdk.EmbeddedAgent` is real, in-process code wired into `boxy serve` today. A separate `Config.Agents`/`AgentSpec` field once existed for a *pull-model* remote agent (server dials out to a static agent address) but was dead code — never read anywhere — and has been deleted. The actual remote-agent design is a *push* model, **implemented 2026-07** (#37/#62) per [ADR-0005](docs/adr/0005-remote-agent-transport-and-registration.md): `boxy agent serve` dials the daemon over gRPC bidirectional streaming with full mTLS from a boxy-owned private CA, bootstrapped by a single-use token exchanged for a client cert. Per-resource agent provenance (`ProviderRef.AgentID`) ensures `Destroy`/`Allocate` route to the exact agent that created a resource rather than any agent offering the same provider type; `PoolSpec.Agent` pins a pool to a specific agent.
- `boxy debug provider *` (drives the in-process `devfactory` reference driver directly, bypassing the daemon) is compiled only with `-tags devtools` and is absent from release binaries. `boxy debug pool drain/fill` is a separate, always-available command that does go through the daemon's HTTP API.
- Streaming is an optional `providersdk.StreamingDriver`/`agentsdk.StreamingAgent` capability routed through `pkg/eventstream`; Docker, devfactory, SSH guests, and PowerShell Direct guests can emit live events. Unsupported custom providers return a capability error instead of buffering unary output as a fake stream.
- `devfactory` is the generic deterministic provider simulator: it exercises
  Boxy's lifecycle, persistence, latency, failure, availability, streaming,
  and resource-listing plumbing without claiming fidelity to a real provider.
  Future provider simulators should implement the existing `providersdk.Driver`
  contract plus the optional capabilities they model (for example `hyperv-sim`
  implementing `GuestPersonalizer`). Keep simulator provider types explicit
  so they cannot be mistaken for the real provider; generate conformance
  scaffolding and capability fixtures, not provider semantics.
- **devfactory's optional-capability scope is a deliberate, written decision
  (#181), not "implement everything eventually."** As of 2026-08 it
  implements `StreamingDriver`, `AvailabilityReporter` (including a real
  zero/insufficient-capacity value via `Config.AvailableMemoryZero` — see
  below), and `ResourceLister` (reflects its own JSON store, sorted by ID).
  `Config.FailCreateAs` (`"capacity"` / `"orphaned_resource"`) makes `Create`
  return `providersdk.CapacityError`/`OrphanedResourceError` on demand, so a
  consumer can exercise typed-error handling (`ErrorTyper`, RemoteAgent/gRPC
  propagation, pool quarantine-and-cleanup) against a reference driver
  without real infrastructure. **`GuestPersonalizer` is an intentional
  non-goal**, not a gap to close later by default: it currently has exactly
  one implementation (`hyperv.Driver`) and no second real provider to
  validate a simulated credential-rotation contract against, so simulating
  it would mean inventing Hyper-V-specific semantics wearing a
  generic-looking interface. Revisit only for a concrete testing need with
  its own scoping writeup — don't fold it into an unrelated change. See
  `docs/superpowers/specs/2026-08-21-devfactory-parity-scope-design.md` for
  the full capability comparison and reasoning, including why the
  "unlimited" `Availability()` sentinel is a large finite constant and not
  `math.MaxInt64` (byte-conversion overflow risk in real drivers).
- **`pkg/diskjson`** is a generic, mutex-guarded, atomically-written
  (write-tmp-then-`os.Rename`) JSON file store for a single value —
  `diskjson.Store[T]` with `Load`/`Save`/`Update`. It generalizes the same
  pattern `pkg/store.DiskStore` already used for `state.json`, so a
  package that needs "persist one JSON blob to disk safely" doesn't
  reimplement it (weaker) on its own — `devfactory`'s own store
  (`pkg/providersdk/providers/devfactory/store.go`) is built on it as of
  2026-08 (#181 follow-up), replacing a direct-overwrite `os.WriteFile`
  that wasn't atomic. `diskjson.Store[T]` is deliberately stateless
  between calls (every call re-reads from disk) rather than caching like
  `DiskStore` does — right for a low-volume debug/reference store you can
  `cat`/`jq` mid-run, wrong for `state.json`'s hot path; don't merge the
  two without a deliberate reason to. `pkg/store.DiskStore` itself hasn't
  been migrated onto it — that's an open, not-yet-decided follow-up, not
  an oversight. See [ADR-0011](docs/adr/0011-devfactory-persistence-config-paths-and-provisioning-lock.md).
- **`providersdk.RelativePathResolver`** is an optional provider-`Config`
  capability (`ResolveRelativePaths(baseDir string)`), detected by type
  assertion like every other capability in this package. `Registry.
  NewDriverFromInstance(instance, baseDir)` calls it, if implemented,
  right after decoding config and before constructing the driver — it
  exists so a relative path in a provider config resolves against *the
  boxy config file's own directory*, matching how `.boxy/state.json`
  already resolves (`internal/cli/serve.go`'s `serveStatePath`), instead
  of silently resolving against the process's ambient working directory.
  `devfactory.Config.DataDir` implements it (2026-08, #181 follow-up); it's
  opt-in per `Config` type, not a default for every path-shaped field
  (docker's socket path and hyperv's VHD/template paths are real host
  locations an operator points at explicitly, not directories conceptually
  owned by the config file). `hyperv.Config.DataDir` implements it too
  (2026-08, #222) — unlike devfactory's, an empty `DataDir` there defaults
  to `.boxy-agent/hyperv` *before* anchoring against `baseDir`, since a lost
  ledger reproduces #222's own address-collision bug rather than just losing
  a debug store; see [ADR-0012](docs/adr/0012-hyperv-range-based-ip-allocation.md).
  Both real call sites are threaded: `internal/cli/serve.go`'s `buildDrivers` (daemon,
  `cfgPath`) and `internal/cli/agent_serve.go`'s `buildAgentDrivers`
  (remote agent, `agentServeOpts.providerConfigsBaseDir` — tracks whether
  provider instances came from `--config` or `--service-config`, since
  those are different files with different base directories). `agent
  service install --config ...` persists the resolved base directory into
  the installed `service.yaml` (`ProviderConfigsBaseDir`) so a later
  `agent serve --service-config service.yaml` resolves against the
  original `--config` file's directory, not `service.yaml`'s own — an
  earlier version recomputed it from `service.yaml`'s path instead, which
  was wrong whenever `--config` was given at install time. See
  [ADR-0011](docs/adr/0011-devfactory-persistence-config-paths-and-provisioning-lock.md).
- **`pool.LockedProvisioner`** (`AgentProvisioner.ProvisionLocked`) is an
  optional `Provisioner` capability that acquires
  `AgentRegistry.LockProvisioning(agentID)` — implements
  `pool.ProvisionLocker` — *before* calling the agent's `Create`, not just
  around the caller's own subsequent store write. This closes a race
  devfactory implementing `providersdk.ResourceLister` exposed: a fast
  driver's `Create()` can make a resource visible via a concurrent agent's
  `List()` before the caller (`Manager`'s provision actuator) regains
  control to acquire anything, which `ReconcileAgent`'s periodic sweep
  (#133/#174) could then misclassify as an orphan. The lock is acquired
  *inside* the provisioner, not by `Manager` beforehand, because only the
  provisioner knows which agent a pool will resolve to without a second,
  observably-different resolution (`AgentRegistry.Resolve` round-robins
  across agents advertising the same type). `Manager.Provision` prefers
  `ProvisionLocked` via a type assertion, falling back to locking only
  around its own store write for a `Provisioner` with no per-agent concept
  (the deprecated `DriverProvisioner`, or a test fake). See
  [ADR-0011](docs/adr/0011-devfactory-persistence-config-paths-and-provisioning-lock.md)
  for the full race analysis, including why an earlier version of this fix
  (locking only around the store write) left the exact window open that
  this one closes.
- **`providersdk.NetworkRangeReporter`** (`NetworkRanges(ctx, switchName)`)
  is an optional capability, detected by type assertion like every other
  one in this package, that discovers a named switch's real IPv4 range from
  the host's own `vEthernet (<switch name>)` adapter rather than trusting
  an operator-declared `network.range` blindly (2026-08, #223). `hyperv.
  Driver` is the only implementation — `devfactory` deliberately does not
  implement it, same reasoning as `GuestPersonalizer`'s devfactory-parity
  non-goal above (one real consumer, no second provider to validate a
  simulated contract against). `Driver.Create` is the sole caller: it
  validates the declared address falls *within* (not equal to) a discovered
  range whenever `config.switch` and either `network.range` or
  `network.static_ip` are set (both modes get this check, not just
  `range` — a `static_ip` pool is exactly as exposed to a typo/drifted
  address), right after `cc.Network.validate()` and before `reserveMemory`
  so a bad config fails before reserving host memory. The discovery query
  itself is bounded by `d.memQueryTimeout()`, reused rather than adding a
  fourth overridable duration field. A positive contradiction is a hard
  `Create` error; an indeterminate result (query failure, or nothing
  discoverable — e.g. a Private switch has no host vNIC) logs a warning and
  proceeds, since this host cannot validate the PowerShell against a real
  switch — see [ADR-0013](docs/adr/0013-hyperv-network-range-reporter.md)
  for the full design, including why validation lives in `Create` rather
  than at pool registration/reconcile time as originally proposed
  (`PoolSpec.Config` is an undecoded `map[string]any` outside the driver,
  and the driver itself normally runs on a remote agent, not the daemon
  that owns pool reconciliation).

### Guest Credentials

- Guest bootstrap credentials and caller-facing credentials have different
  lifetimes. The server-owned per-pool bootstrap value is used only to
  personalize a new Hyper-V guest; the rotated `providersdk.GuestCredential`
  is opaque, process-local, and one-time-delivered to the caller. Never add
  either secret to `model.Resource.Properties`, VM notes, API logs, or remote
  agent configuration. See [ADR-0010](docs/adr/0010-guest-credential-delivery.md).
- The remote bootstrap lookup is authorized from the mTLS agent identity plus
  the resource's recorded `OriginPool` and `Provider.AgentID`. Keep this
  ownership check server-side; do not trust pool or agent claims supplied by a
  remote agent.
- Drivers that depend on a reconnectable remote transport must resolve the
  current connection at operation time. Do not capture a gRPC connection when
  constructing a driver; reconnects replace it.
- Test this path with fake guest executors, injected keyring backends, agent
  wire tests, and the devfactory provider for control-plane orchestration. This
  host cannot perform live Hyper-V VM validation.

### Bundled Agent Skill

- Bundled skill assets live under `internal/skills/assets/boxy-cli/` and are embedded into the binary.
- The canonical installed copy lives at `~/.config/boxy/skills/boxy-cli/` on all platforms.
- `boxy skills install` links or copies that canonical skill into agent-specific directories such as `~/.agents/skills/`.
- `boxy help all` is the machine-readable command surface the bundled skill points agents to when they need current syntax.

## Lessons Learned

- **Line endings**: `.gitattributes` declares `* text=auto eol=lf` for the whole
  repo, but that only normalizes files as they're touched — it does not
  retroactively rewrite existing blobs. Several files (e.g. `release.yml`
  before 2026-07) still have CRLF stored in their git blob from before that
  policy existed. If you edit such a file with a full-file rewrite (rather
  than an in-place string replacement), you'll silently flip it to LF and the
  diff will show the *entire file* as changed instead of your actual edit —
  reviewers can't see what you changed. Prefer targeted in-place edits over
  full-file rewrites for any existing file, especially in `.github/workflows/`.
  If you must rewrite a whole file, check `git show HEAD:<path> | head -c 100 | xxd`
  first to see whether it's CRLF, and match it.
- **Merging PRs that touch `.github/workflows/`**: the `gh` CLI's default
  OAuth token often lacks the `workflow` scope, and `gh pr merge` fails with
  a GraphQL permission error in that case. This requires the repo owner to
  run `gh auth refresh -s workflow` themselves — it's a credential change, not
  something to work around.
- **Git commit signing / push over SSH via 1Password**: if commits or pushes
  fail with `1Password: failed to fill whole buffer` or
  `sign_and_send_pubkey: signing failed`, 1Password's SSH agent needs to be
  unlocked — ask the user rather than reaching for `--no-gpg-sign` or similar
  bypasses.
- **Before creating a throwaway git branch for local testing**, double-check
  the branch name is valid (e.g. no leading `/`) and confirm with
  `git branch --show-current` that the checkout actually succeeded. A failed
  `checkout -b` leaves you on whatever branch you were already on, and
  subsequent commits land there instead — recoverable if caught immediately
  (nothing had been pushed), but worth avoiding.
- **Issue text drifts from reality fast** in an actively-developed repo. Before
  planning work off an issue's description, grep/read the actual current code
  — several issues this project tracked (e.g. #33, #34, #36, #13) turned out
  to already be implemented, sometimes under a different architecture than
  the issue described. Verify, don't assume the issue is current.
- **This file (and README.md / docs/architecture.md) can also drift.** During
  the 2026-07 session that added these notes, README.md and architecture.md
  were found describing gRPC agents, a `bbolt` store, and an `agents:` config
  section as if already built, when none of that existed in the codebase.
  Docs describing "planned" work should say so explicitly and get corrected
  once work actually lands (or is found to be dead/unbuilt) — don't assume
  existing docs are accurate; verify against code before trusting them.
- **Merging a stack of dependent PRs**: when PR B's base branch is PR A's
  feature branch (not `main`), merge A first, then retarget B with
  `gh pr edit <B> --base main` before merging B — `delete_branch_on_merge`
  is `false` in this repo, so GitHub does *not* auto-retarget B onto `main`
  the way it would if A's branch were deleted on merge.
- **Closing issues via merged PRs**: a PR body must say `Closes #N` (or
  `Fixes`/`Resolves`) for GitHub to auto-close the issue on merge. Referencing
  `#N` alone (e.g. "part of #133") does not trigger auto-close — check and
  close manually if it was missed.
- **`pkg/psdirect`'s `Exec.ExecStream` is uncovered by tests, deliberately** —
  PSRP's `StreamResult` has no constructible test double without a
  production interface seam. See [ADR-0008](docs/adr/0008-streaming-command-execution.md)'s
  Consequences for the full reasoning; don't rediscover this from scratch.
- **A feature branch can go stale mid-session** if another PR merges to
  `main` while you're working — a clean `git push` succeeding says nothing
  about whether the PR's base is current. During #162 (2026-08), an
  unrelated PR (#159, a large service-install feature) and a release-please
  release both merged to `main` after this branch's base commit. Before
  opening or merging a PR that's been in progress for a while, `git fetch
  origin main` and check `git log main..origin/main`; if it's non-empty,
  `git merge --no-commit --no-ff origin/main` locally, resolve conflicts,
  and rerun the full test suite before pushing — don't assume a same-repo
  file that merged cleanly actually kept both sides' changes without
  checking (see ADR-0009's file list for what #162 touched that #159 also
  touched: `internal/cli/agent_serve.go`, `agent.go`, `serve.go`, and the
  bundled skill all needed a real 3-way merge, not just a fast-forward).
- **A clean textual rebase can still be semantically broken.** Upstream
  refactors can remove or rename helpers while the affected files auto-merge
  without conflict. After rebasing, run the complete local CI gate on each
  split branch independently; do not trust a clean rebase or a green combined
  branch as proof that every standalone branch still builds and tests.
- **`git rebase --rebase-merges` re-runs the merge algorithm at every replayed
  merge commit — it does not just re-sign/relocate the existing merge
  result.** Bulk-re-signing a branch (e.g. `git -c commit.gpgsign=false commit`
  now, `git rebase --rebase-merges --exec 'git commit --amend --no-edit -S'
  main` later once signing is unblocked) silently reintroduced a bug this
  way (2026-08-27, batch PR #264): an earlier manual fix-up had been folded
  *into* a merge commit via `git commit --amend` after resolving it (see the
  "clean textual rebase" entry above — this was the exact same
  `computeToProvisionCount` 3-arg-vs-2-arg conflict, caught and fixed once
  already). The rebase replayed both branch tips from their original
  pre-fix commits and re-ran `git merge` fresh at that point, which
  reproduced the identical no-conflict-but-wrong auto-resolution — the
  manual amendment wasn't a separate commit, so replay had nothing to
  reapply. Caught before pushing only because the tree was diffed against a
  pre-rebase recovery tag (`git tag recovery/<branch> <sha>` before
  rewriting, `git diff recovery/<branch> <branch>` after) and the diff
  wasn't empty. Fixed by re-applying the fix as a `git commit --fixup=<merge-sha>`
  targeting the specific merge commit, then re-running the rebase with
  `--autosquash` added so the fixup folds into the right commit instead of
  landing as its own; the diff-against-recovery-tag check came back empty
  the second time, confirmed by a full `task ci:validate` pass. The general
  rule: any history-rewriting rebase that touches a merge commit needs the
  same "don't trust it, verify the resulting tree" treatment as a normal
  rebase, and a merge commit that was hand-amended after resolution is the
  specific shape most likely to silently regress on replay — prefer a
  separate follow-up commit over amending a merge commit when a fix-up is
  likely to survive a later rebase.
- **Reconcile history from `origin/main` before changing local `main`.** Fetch
  first, map the commit DAG, and preserve a named recovery ref before resetting
  a diverged local branch. When splitting stacked work, verify which commits
  are already represented by merged PRs so duplicate historical work is not
  reintroduced.
- **Worktree branch tips require their own cleanup decision.** A PR may have
  merged while its original worktree branch continued with unmerged follow-up
  commits. Compare each worktree tip with `origin/main` and inspect its status
  before removing the worktree or branch; never infer that the whole branch is
  disposable from one merged PR.
- **Release Please and GoReleaser both mutate release metadata.** Release
  Please's `prerelease: true` does not protect a release from a later GoReleaser
  update. With a plain tag such as `v0.1.40`, GoReleaser's `prerelease: auto`
  treats it as stable, and its default `make_latest: true` promotes it as the
  latest release. Keep `.goreleaser.yml` explicit (`prerelease: true`,
  `make_latest: false`) until a deliberate config change promotes a stable
  release, and inspect the actual GitHub release flags after each release
  workflow.
- **A GitHub prerelease flag is separate from prerelease SemVer tags.** Explicit
  GoReleaser `prerelease: true` keeps a plain `v0.1.x` release marked
  prerelease, but it does not create a `-rc` or `-beta` suffix. Configure
  Release Please's prerelease versioning separately if prerelease tag names are
  required.
- **This development host cannot run Hyper-V VMs.** Use the `devfactory`
  provider for control-plane, agent, and end-to-end orchestration tests, and
  inject fake guest executors for Hyper-V rotation/exec behavior. Do not claim
  live Hyper-V validation from this environment.
- **Operational command drift can survive a green test suite.** Run the
  documented Taskfile/skill smoke commands, not only package tests. At present
  `task serve:once` advertises `boxy serve --once`, but the command rejects
  that flag; treat this as a stale recipe/documentation defect and use a
  bounded live `serve` smoke run until the command contract is repaired.
- **`task lint` used to pass on a Windows dev host while CI's `Lint` job
  failed on the exact same commit.** `golangci-lint`'s build-tag-sensitive
  checks (e.g. `unused`) only see the invoking host's own `GOOS`; CI's `Lint`
  job always runs on `ubuntu-latest`, so a Windows host compiling
  Windows-tagged files (`//go:build windows`) never saw anything that was
  unused specifically outside Windows. This is exactly how
  `pkg/secrets/secrets.go` shipped three Windows-only-consumed methods in
  #197 that stayed invisible to local lint on this Windows host but broke CI
  immediately (found and fixed in #209, 2026-08). `task lint` (and therefore
  `task ci:validate`) now installs golangci-lint natively but runs it with
  `GOOS=linux` forced, so the analysis matches CI's runner regardless of host
  OS — this class of gap should not recur. If a future change adds a new
  GOOS-gated file family, verify `task lint` still reflects CI by comparing
  against `GOOS=<other target>` explicitly rather than assuming.
- **A PR's red CI check is not automatically evidence of a bug the PR
  introduced.** Before assuming a failing check needs fixing in the branch
  under review, check whether the failing file/behavior is even reachable
  from that branch (`git merge-base --is-ancestor <suspect-commit>
  <branch-tip>`, or `git log <merge-base>..<branch-tip> -- <path>`) — a
  failure can be pre-existing on `main` (unrelated commit, merged earlier)
  or, for Betterleaks specifically, coming from an entirely different open
  branch the scan happened to also see (see the `--log-opts HEAD` note
  above). #208 merged to `main` with two red CI checks (Lint, Betterleaks
  PII); neither was caused by #208's own changes, and conflating "PR is red"
  with "PR broke it" would have led to fixing the wrong branch. Root-cause
  before patching.
- **Fully validate locally before pushing, including the parts that are easy
  to skip because you already predict the result.** `task ci:validate` (or
  the specific local scan/lint commands it wraps) must actually be run and
  its real output checked before every push — not inferred from what a
  similar run did earlier, and not skipped because a failure is "expected"
  (e.g. a known pre-existing issue on a sibling PR). Predicting a CI outcome
  correctly is not the same as verifying it locally; pushing on a prediction
  still burns a CI run and a round trip to find out you were right, and
  leaves no local proof if you were wrong. This also applies per-branch when
  splitting a fix into multiple PRs (see the `--log-opts HEAD` fix, #210):
  each branch's own full local validation surface should be run and read,
  not assumed from the sibling branch's results.
- **A bare `#N` in a commit message or PR title does not close the issue —
  and this repo has already shipped work this way multiple times.** During
  the 2026-08-24 session, four issues (#197, #101, #168, #185) were found
  fully implemented and merged to `main` — in three cases via a direct
  commit to `main` with no PR at all (`94518da`, `e993f04`, `df7794f`) —
  but still open, because the commit messages used a `feat(#N): ...`
  traceability prefix instead of a `Closes #N`/`Fixes #N`/`Resolves #N`
  closing keyword. GitHub only auto-closes on the closing keyword, not a
  bare reference, whether that keyword lives in a merged PR body or in a
  commit pushed straight to the default branch — see the existing "Closing
  issues via merged PRs" entry above, which already covered the PR case;
  this generalizes it to direct commits too. Before triaging or planning
  off the open-issue list, don't trust it at face value for anything that
  looks like it might already be architecturally significant — cross-check
  with `git log --all --oneline --grep="#N"` and read the matching commits,
  same as verifying against code per the "Issue text drifts from reality
  fast" entry. Going forward: (1) self-assign an issue
  (`gh issue edit <N> --add-assignee @me`) when starting real implementation
  work on it, so its state is visible while in progress; (2) always use the
  actual closing keyword (not a bare `#N`) in whichever text GitHub will
  scan — the PR body when there's a PR, the commit message itself when
  committing straight to `main`; (3) when finishing work with no PR
  involved, explicitly verify the issue closed (`gh issue view <N> --json
  state`) and close it manually referencing the commit SHA if it didn't.
  The `ship-it` skill's Phase 10 now covers the no-PR case explicitly.
- **`gh pr view <N> --json comments` does not surface Copilot's (or any
  bot's) PR review or its inline review comments** — only plain
  issue-thread comments. During the #240 fix (PR #250, 2026-08-26), an
  initial "check for reviewer comments" pass read this field, saw an
  empty result, and reported the PR as having no reviews — which was
  wrong; a `copilot-pull-request-reviewer[bot]` review with two inline
  comments existed and was invisible to that query. Checking for bot/human
  PR reviews before asking to merge requires the separate endpoints
  `gh api repos/<owner>/<repo>/pulls/<N>/reviews` (the review verdict and
  summary) and `.../pulls/<N>/comments` (the inline comments attached to
  it) — `gh pr view --json reviews` is a simpler substitute for the first
  (the review verdict/summary) but **not** the second: it does not include
  the per-line inline comments, so `.../pulls/<N>/comments` still has to be
  checked separately to see those. Don't rely on `--json comments` alone
  ever again for this check, and don't assume `--json reviews` alone
  covers inline comments either.
- **An automated reviewer's finding (a code-review subagent's "CONFIRMED"
  verdict, or a bot like Copilot) is a claim to verify against the actual
  code, not a fact to relay or dismiss on authority.** During #240 (PR
  #250, 2026-08-26), a review subagent flagged a real, independently-
  reproducible gap (`EnsureReady`'s background top-up toward `min_ready`
  can still fail a request that was already satisfiable) — worth acting
  on because tracing `internal/pool/manager.go` and
  `internal/sandbox/fulfiller.go` confirmed it, not because the agent
  said "CONFIRMED". Symmetrically, Copilot's review on the same PR flagged
  two doc/comment wording issues; both were adopted only after checking
  them against the code (`DrainedPoolError`, the provisioning-error path)
  independently confirmed the wording was actually wrong, not because
  Copilot proposed a fix. Treat every reviewer's output — human, subagent,
  or bot — as a lead to check, in both directions: don't rubber-stamp a
  finding, and don't dismiss one either without looking.

## ADRs

When decisions are made, save them as ADR documents in /docs/adr. This is a living document, so feel free to update it as needed. When an ADR is updated, add a note at the end of the document describing the change and the date it was made.

## My Values

- DRY
- Clean code
- Good documentation
- Architectural soundness — doesn't necessarily mean "simple" but is well thought out and maintainable as project expands.

## AI-First Workflow Notes

- Cost model differs from human dev cycles: refactors are cheap when an agent can apply wide changes quickly, resolve merges/rebases, and keep `go test ./...` green.
- Bias toward a single source of truth: remove duplication promptly and update all call sites together (avoid parallel “old vs new” models).
- Treat “no regressions” as “no regressions covered by tests”: add/extend targeted tests whenever behavior changes.
- Use `pkg/` for generic contracts, primitives, or shared vocabulary that reduce conceptual load in isolation. Keep Boxy application workflow glue in `internal/`; do not promote daemon/CLI orchestration code to `pkg/` unless a genuinely general concept has emerged.

## Development Notes

- Primary developer has an OOP background — write idiomatic Go (composition over inheritance) while respecting the project's values.
- Don't think about "simple for v1" — I like to think about the entire architecture when designing personal experimental projects like this. Design for sound, maintainable architecture even if features aren't strictly needed for v1.
- Look for clear interface contracts and separation of concerns. If a package is doing too much, consider how to split it up or abstract responsibilities. Make these abstract responsibilities reusable and composable where it makes sense and in public pkg/, but avoid premature generalization. A public package shouldn't feel coupled to the internal application structure it should completely usable outside of boxy. See existing `agentsdk` and `providersdk` packages for examples of this approach. These packages define general concepts like "Agent" and "Driver" that are implemented by the internal application but could also be used by external projects without depending on boxy-specific types or workflows. Developing this way also ensure we are only focusing on one problem at a time. For example, when working on the agent system, we can focus on defining a clean Agent interface and implementation without worrying about how it will be used in the daemon or CLI until we have a solid design for the agent itself.

## CLI Change Checklist

- Any CLI surface change (new command, renamed command, flag/output shape change that affects usage) must update both `docs/cli-wireframe.md` and `internal/skills/assets/boxy-cli/` in the same PR.
- `internal/skills/drift_test.go` enforces command-token coverage for the bundled skill. Keep it green.
- When adding or changing a common skill-related workflow, update `task skills:check` if the validation surface changes.

## Taskfile

Wrap repeated commands in `Taskfile.yml`. If a command is run more than once, add it as a task.
- Use `dir: '{{.USER_WORKING_DIR}}'` for tasks that should execute from the caller's directory, while still referencing repo-root paths with `{{.ROOT_DIR}}` when needed.

## Tools

- `gopls` is available locally for code navigation, refactoring, and linting. Use the Go language server whenever possible when reading, navigating, analyzing, or modifying Go code; prefer its symbol, reference, diagnostic, and refactoring capabilities over broad text searches or manual edits for greater efficiency and correctness.
- `task` (go-task) for running project commands.
- Before pushing a branch, run `task ci:validate` from a clean tracked worktree. It runs the full tests, CI's race/short/devtools matrix, pinned lint, installer smoke tests, build, both full-history Betterleaks scans, generation, and diff checks locally.
- `task lint` mirrors CI by running `golangci-lint` v2 from source via `go run`, so it does not depend on a preinstalled local binary version.
- Tool dependencies used by Taskfile tasks and CI must use explicit pinned versions, kept at the latest stable release compatible with the repository's declared toolchain. Update the pin intentionally in both local and CI workflows and validate the full task/check surface; do not use moving `@latest` references.
- GoReleaser is pinned in the isolated `tools/` module; use `task release:check` and `task release:snapshot` instead of assuming a global `goreleaser` binary is installed.

## Installer Notes

- Release installers live in `scripts/install.ps1` and `scripts/install.sh`.
- Installers target published GitHub release assets, not local source builds.
- Release assets are GoReleaser archives (`boxy_<version>_<os>_<arch>.tar.gz` or `.zip`) plus `checksums.txt`.
- `latest` in installer scripts means the newest published GitHub release, including prereleases.
- Installers verify the downloaded binary against the published `checksums.txt`.
- Release automation also publishes `checksums.txt.sigstore.json`, a keyless
  cosign signature bundle for `checksums.txt` (#55, 2026-08, ADR-0014).
  Installer-side automatic verification of it is deliberately deferred — see
  #231 — installers today still verify only the checksum.
- Default install locations are user-local:
  - Windows: `%LOCALAPPDATA%\Programs\boxy\bin`
  - Linux: `$HOME/.local/bin`
- Linux installer prints PATH update instructions instead of editing shell startup files automatically.

## CI / CD Workflow Notes

### Merging PRs

- `main` has no branch protection (`gh api repos/Geogboe/boxy/branches/main/protection` → 404). Merges are gated by convention and green CI, not by GitHub-enforced required checks.
- History uses merge commits, not squash, for every PR including release-please PRs — use `gh pr merge --merge --body ""`. The trailing `--body ""` is load-bearing, not cosmetic — see the changelog-duplication note below.
- **Every regular fix/feat PR's changelog entry was appearing twice in release-please's notes until 2026-08-27 — always pass `--body ""`.** Root cause: the repo's merge-commit template (`merge_commit_message: "PR_TITLE"`) puts the PR title into the merge commit's *body*, and PR titles here are themselves Conventional-Commit-formatted (`fix(pool): ...`) — identical in shape to the underlying fix commit already in history. release-please's commit parser walks every commit including merge commits, so it picked up both as separate entries for the same change (see `v0.1.50`'s release notes for a live example: `#240`'s fix is listed twice, once per commit SHA). There is **no repo-settings fix** for this: GitHub only allows three `(merge_commit_title, merge_commit_message)` combinations — `(PR_TITLE, PR_BODY)`, `(PR_TITLE, BLANK)`, `(MERGE_MESSAGE, PR_TITLE)` — and every combination other than the current one either keeps the duplicate body or moves the same duplicate text into the merge commit's *subject* instead. The fix has to happen per-merge: `gh pr merge --merge --body ""` explicitly overrides the template with an empty body via the API, leaving the merge commit as subject-only (`Merge pull request #N from owner/branch`), which doesn't match Conventional-Commit shape and isn't picked up as its own entry. This only prevents *future* duplication — releases already cut (`v0.1.50` and earlier) keep their duplicated notes unless hand-edited separately.
- release-please PRs reliably show their `CI` check as `action_required` with zero jobs run (seen for 0.1.27 and 0.1.29). This is a known, harmless quirk of that workflow's trigger conditions, not a real gate — safe to merge through.
- **Batching several small, independent fixes into one local integration
  branch before opening any PR** (rather than one branch-and-PR per issue
  against a moving `main`) is a deliberate, requested pattern, not just a
  shortcut — see PR #264 (2026-08-27, six issues: #241/#251/#258/#249/#213/#104).
  Build each fix on its own short-lived topic branch off the integration
  branch as usual (keeps commit messages/`Closes #N` and, if plans change,
  the option to cherry-pick just one fix out later), `git merge` it into the
  integration branch immediately, and **run the full build/test/`task
  ci:validate` gate again after every single merge**, not just once at the
  end — this caught two separate real semantic-merge regressions in the
  same session (see the two entries above) that a single end-of-batch check
  would have found much later and made harder to bisect. If the integration
  branch already has open PRs from an earlier attempt at per-issue branches
  covering some of the same commits, close those as superseded (referencing
  the new batch PR) once the batch PR exists — don't leave both live.
- **Push/PR/CI cadence is deliberately low, separate from the batching
  pattern above (2026-08-28 decision).** The mechanics above (topic branch
  per fix, merged into one local integration branch, full `task ci:validate`
  after every merge) are the unit of work; pushing that branch, opening a
  PR, and letting GitHub Actions run is a separate, coarser-grained step —
  and the point of doing all validation locally first is specifically to
  avoid needing that remote round-trip more than necessary. Default to
  working through as much of the backlog as is actually batch-shaped (see
  the per-issue scoping judgment used for #264: skip anything needing live
  Hyper-V/macOS validation this host can't do, skip anything design/ADR-
  shaped, skip anything with no real scope yet) on one local branch across
  a whole session or more, closing a real batch of issues, before pushing
  and opening a PR at all. Push once that batch is substantial, not after
  every few items — local `task ci:validate` is what actually needs to pass
  before every push either way, so batching doesn't lower validation rigor,
  it just moves the GitHub Actions run (and the release-please/GoReleaser
  cadence question — see "Release cadence" above) to fire less often.
- Merging a release-please PR triggers `release.yml` on push to `main`. The `release-please` job tags and completes quickly; the `goreleaser` job (5 platforms + SBOMs + checksums + cosign signing, ~3 min of actual runtime) then **pauses indefinitely on a `release-signing` GitHub Environment approval** (#55, 2026-08, ADR-0014) before it starts — it will not run to completion on its own. Go approve it in the Actions run's UI, then wait for the run to complete before treating the release as published. Don't mistake the pause for a stalled/failed run.
- **Release cadence is deliberately not "cut a release after every merged fix" (2026-08-27 decision).** The project stays prerelease (`prerelease: true` in both `release-please-config.json` and `.goreleaser.yml`) until the owner says otherwise, but a prerelease that ships after a single one-line bugfix is still a wasted release: a full 5-platform GoReleaser run (SBOMs, checksums, cosign signing, a manual approval click) for one commit's worth of change. release-please already supports this — it keeps exactly one open release PR that accumulates every commit landed on `main` since the last release, updating in place, until that PR is merged. The fix is workflow discipline, not tooling: **don't merge the release-please PR just because it appeared.** Let multiple fix/feat PRs land on `main` first so the pending release PR accumulates a real batch of changelog-worthy entries, and only merge it — cutting the actual tagged release — when there's enough substance to justify a release, or when the owner explicitly asks for one. This overrides the ship-it skill's Phase 8 default (which treats "approve and merge the release PR" as an automatic follow-on to a self-approved fix PR merge) — ask before merging a release-please PR rather than doing it on autopilot.

### GitHub Actions Node 24 migration — done

All actions in `ci.yml` and `release.yml` are pinned to Node 24-compatible
versions as of 2026-07 (`release-please-action` v5.0.0, `goreleaser-action`
v7.2.3). The `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24` workaround has been removed —
it's no longer needed. See #100.

### Action pinning and updates

- Every third-party action in `ci.yml` and `release.yml` is pinned to a full
  commit SHA (not a mutable tag), per #55. The tag is kept as a trailing
  comment (`@<sha> # vX.Y.Z`) for readability.
- `.github/dependabot.yml` watches the `github-actions` ecosystem and opens
  PRs to bump these pins — don't hand-edit them without also checking whether
  Dependabot would have caught the same update.
- `.github/CODEOWNERS` requires owner review on any `.github/workflows/`
  change.
- A `betterleaks` job runs in `ci.yml` on every push/PR and scans the full Git
  history of the checked-out ref with pinned Betterleaks `v1.8.1`. `task
  secrets:scan` installs and runs the same pinned version locally. Do not
  replace it with directory mode because that would miss secrets that were
  committed and later removed. Both the CI job and `task secrets:scan`/`task
  pii:scan` pass `--log-opts HEAD` (2026-08, PR #209 follow-up): without it,
  Betterleaks' `git .` source walks every ref reachable in the local
  repository, and `fetch-depth: 0` on `actions/checkout` fetches *all* remote
  branches (`+refs/heads/*`), not just the one being tested — so an unrelated
  open branch elsewhere in the repo (e.g. a stray test IP literal on someone
  else's in-progress PR) could fail the PII job for a PR that never touched
  it, and the same is true locally for any developer with those branches
  fetched. `--log-opts HEAD` still walks the *full* history of the checked-out
  ref back to its initial commit — no coverage of what's actually being
  merged is lost — it only drops sibling branches this run isn't about. On
  Windows,
  `scripts/betterleaks-git.ps1` prepends a narrow Git shim for a native ARM64
  Git-for-Windows compatibility issue: Betterleaks maps Go's `os.DevNull` to
  `NUL` for `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM`, while the
  `clangarm64` Git build rejects `NUL` as a config-file path, including Git
  `2.55.0.windows.3`. Testing on an x64 `mingw64` host did not reproduce it.
  The shim clears only those invalid paths, keeps system config disabled, and
  delegates to the real Git executable; it does not bypass Betterleaks or
  weaken the history scan. Retest native `betterleaks git .` after any
  Betterleaks/Git upgrade before removing the shim. See the independent
  Windows ARM64 reproduction at
  https://github.com/Gentleman-Programming/gentle-ai/issues/2206.

- A separate `pii` job uses `.betterleaks-pii.toml` to scan the checked-out
  ref's full Git history for non-controlled email addresses, private IPs, non-example hostnames,
  usernames, and home-directory paths. `task pii:scan` runs it locally;
  `task pii:scan:stdin` checks proposed public issue, PR, or comment text;
  `task pii:authors` reports Git author identities separately and is
  informational rather than blocking. Run the repository scan and the stdin
  scan before publishing public text. The archived external PII-scanner skill
  is not part of this workflow.
- Test and documentation fixtures must use scanner-recognized placeholders for
  fake credentials, such as `${BOXY_TEST_PASSWORD}`, `${BOXY_TEST_TOKEN}`, or
  `${BOXY_TEST_API_KEY}`. For identity-shaped fixtures, use
  `boxy-test@example.invalid`, `boxy.example.test`, TEST-NET/documentation IP
  ranges, `boxy-test-user`, and `C:\Users\boxy-test-user` or
  `/home/boxy-test-user`. Do not use realistic-looking random strings or
  common password words such as `password`, `changeme`, `testpass`, or
  `foo`/`bar`; those can be valid credentials and should remain visible to
  Betterleaks. Historical secret or PII fixture findings may be recorded only
  as narrow fingerprint-only `.betterleaksignore` entries after review.

### GoReleaser Signing Notes

- GoReleaser publishes `checksums.txt` alongside release binaries and SBOMs,
  and (as of 2026-08, #55) signs it with **keyless cosign**
  (Sigstore/Fulcio/Rekor via the `goreleaser` job's GitHub OIDC token), not a
  GPG subkey — chosen so there is no long-lived private key to generate,
  store, or rotate. `signs:` lives in `.goreleaser.yml`, so `task
  release:check` / `task release:snapshot` exercise the config locally
  (signing itself still fails locally with "cosign: executable file not
  found" unless `cosign` is installed — that's expected; the real binary
  only needs to exist in CI, via the `sigstore/cosign-installer` step in
  `release.yml`). See [ADR-0014](docs/adr/0014-release-signing-with-keyless-cosign.md).
- The `goreleaser` job runs under the `release-signing` GitHub Environment,
  which requires a manual approval click before the *entire* job (not just
  the signature) proceeds — chosen over gating a separate downstream signing
  job specifically so `signs:` could stay locally testable; see ADR-0014 for
  the tradeoff. This environment must be created in repo Settings before a
  release can complete — it does not exist by default, and the job will hang
  waiting for an approval gate that was never configured otherwise.
- Artifact attestations (`actions/attest-build-provenance`) were considered
  and deliberately **not** added alongside cosign signing — both deliver
  overlapping provenance guarantees for the same artifacts, and shipping
  both would be duplicated trust machinery for no added assurance.
  Reconsider only with a concrete reason attestations add something cosign
  doesn't, not by default.
- Installer-side automatic signature verification is still **not
  implemented** — tracked separately as #231, deliberately deferred out of
  #55 (that issue's own text called it "long term" scope). Don't assume
  `scripts/install.sh`/`scripts/install.ps1` verify anything beyond the
  checksum; verify against the actual scripts before claiming otherwise.

## Current Delivery Notes

- Secure REST/CLI management (#154), sandbox command execution (#153), and
  the file-permission-on-rewrite sweep (#158) landed together in #162
  (2026-08). See [ADR-0007](docs/adr/0007-secure-rest-api-and-cli-authentication.md),
  [ADR-0008](docs/adr/0008-streaming-command-execution.md), and
  [ADR-0009](docs/adr/0009-file-permission-hardening-on-rewrite.md) for the
  decisions and their dated change notes.
- REST handlers are currently hand-wired under `internal/server/api_*.go`.
  Keep the route catalog, generated `docs/api.md`, CLI wireframe, and bundled
  skill synchronized when adding routes or commands; use `task generate`.
- API keys are hashed in the daemon store and raw values belong only in the OS
  keyring. The first admin key is loopback-bootstrap-only; TLS uses the Boxy CA
  by default, `--ca-cert` for custom trust, and explicit insecure overrides —
  including for a bare (schemeless) `--server` address, which defaults to
  `https://`, not `http://` (ADR-0007).
- `os.WriteFile`'s mode argument is ignored on rewrite of a pre-existing
  file — any new write site handling sensitive or config material needs an
  explicit `os.Chmod` follow-up unless it uses disk.go's write-tmp-then-rename
  pattern. See ADR-0009 for the full file list and why rename is exempt.
- For feature work, use TDD red/green/blue: add a failing test, implement the
  smallest fix, then review/refactor with `gopls`; finish with `task test`,
  `task lint`, and documentation/drift checks.
- Guest credential delivery for Hyper-V pools (#188/#189) is implemented with
  server-owned bootstrap storage, mTLS-authorized agent resolution, allocation
  time password rotation, one-time sandbox delivery, and caller-supplied exec
  credentials. See [ADR-0010](docs/adr/0010-guest-credential-delivery.md) and
  the design spec for the accepted restart/lost-delivery behavior.

# Deletions

Don't delete files or directories, when you'd do a delete instead move to .archive/

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).

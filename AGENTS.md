# AGENTS.md

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Project

- **Module:** `github.com/Geogboe/boxy`
- **Go version:** 1.25
- **Dependencies:** cobra (CLI), yaml.v3 (config parsing)

## Issue Tracking

Features, bugs, and roadmap items are tracked as GitHub issues on `Geogboe/boxy`. There is no separate roadmap file — GitHub issues are the single source of truth for planned work. Use `gh issue list` to see current priorities and `gh issue view <number>` for details.

## Commands

**Always check `Taskfile.yml` for existing tasks before running raw commands.** Use `task <name>` instead of raw `go test`, `golangci-lint run`, etc.

```bash
task build            # Build ./boxy binary
task test             # Run all tests
task lint             # Run golangci-lint (same as CI)
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

### Sandboxes

- A sandbox is an environment that can be as small as a single resource or as large as a full lab.
- Sandbox creation via the REST API is asynchronous: `POST /api/v1/sandboxes` persists a sandbox request in `pending`, the daemon fulfillment loop provisions/allocates resources, and the sandbox transitions to `ready` or `failed`.
- `POST /api/v1/sandboxes` accepts typed `requests`, not sandbox-spec `resources`; the handler rejects unknown fields so stale clients fail fast instead of silently sending the wrong shape.
- `boxy serve` persists runtime state in `.boxy/state.json` next to the active config (or under the working directory when no config file is used), so accepted async sandbox requests survive normal daemon restarts.
- `boxy sandbox create -f ...` is daemon-backed: the CLI loads a sandbox spec, resolves named pools from the daemon pool catalog, submits async `requests`, and waits for `ready`/`failed` by default. Use `--no-wait` to return after the daemon accepts the request.
- `Policies.AutoDestroyAfter` (`auto_destroy_after` in requests) is enforced (as of 2026-07): sandbox creation computes a real `ExpiresAt`, and the existing async-deletion reconciler (`internal/sandbox/deleter.go`, ticked every 10s alongside everything else) promotes expired sandboxes into deletion. `POST /api/v1/sandboxes/{id}/extend` / `boxy sandbox extend <id> <duration>` push the deadline out, compounding from the current `ExpiresAt` rather than resetting from now. See ADR-0003.
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

- `gopls` is available locally for code navigation, refactoring, and linting.
- `task` (go-task) for running project commands.
- `task lint` mirrors CI by running `golangci-lint` v2 from source via `go run`, so it does not depend on a preinstalled local binary version.
- GoReleaser is pinned in the isolated `tools/` module; use `task release:check` and `task release:snapshot` instead of assuming a global `goreleaser` binary is installed.

## Installer Notes

- Release installers live in `scripts/install.ps1` and `scripts/install.sh`.
- Installers target published GitHub release assets, not local source builds.
- Release assets are GoReleaser archives (`boxy_<version>_<os>_<arch>.tar.gz` or `.zip`) plus `checksums.txt`.
- `latest` in installer scripts means the newest published GitHub release, including prereleases.
- Installers verify the downloaded binary against the published `checksums.txt`.
- Release automation also publishes a signed `checksums.txt.sig`.
- Default install locations are user-local:
  - Windows: `%LOCALAPPDATA%\Programs\boxy\bin`
  - Linux: `$HOME/.local/bin`
- Linux installer prints PATH update instructions instead of editing shell startup files automatically.

## CI / CD Workflow Notes

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
- A `gitleaks` job runs in `ci.yml` on every push/PR.

### GoReleaser Signing Notes

- GoReleaser publishes `checksums.txt` alongside release binaries and SBOMs.
- Release-signing (a dedicated signing subkey, GitHub Environment approval
  gates) is deliberately **not implemented yet** — tracked as remaining scope
  on #55, not done in the 2026-07 security-hardening pass. Don't assume a
  `GPG_PRIVATE_KEY`/`GPG_PASSPHRASE` secret pair exists; verify against the
  actual `release.yml` before referencing signing in docs or code — this
  section itself was previously stale and described signing that was never
  actually wired up.

# Deletions

Don't delete files or directories, when you'd do a delete instead move to .archive/

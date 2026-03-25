# AGENTS.md

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Project

- **Module:** `github.com/Geogboe/boxy`
- **Go version:** 1.25
- **Dependencies:** cobra (CLI), yaml.v3 (config parsing)

## Commands

```bash
task build            # Build ./boxy binary
task serve            # Run boxy serve (daemon mode)
task serve:once       # Run boxy serve --once (single reconciliation pass)
task go:run -- <args> # Run boxy via go run with arbitrary args
go test ./...         # Run all tests
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

### Sandboxes

- A sandbox is an environment that can be as small as a single resource or as large as a full lab.
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

## Development Notes

- Primary developer has an OOP background — write idiomatic Go (composition over inheritance) while respecting the project's values.
- Don't think about "simple for v1" — I like to think about the entire architecture when designing personal experimental projects like this. Design for sound, maintainable architecture even if features aren't strictly needed for v1.

## Taskfile

Wrap repeated commands in `Taskfile.yml`. If a command is run more than once, add it as a task.
- Use `dir: '{{.USER_WORKING_DIR}}'` for tasks that should execute from the caller's directory, while still referencing repo-root paths with `{{.ROOT_DIR}}` when needed.

## Tools

- `gopls` is available locally for code navigation, refactoring, and linting.
- `task` (go-task) for running project commands.
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

### GitHub Actions Node 20 → 24 Migration Status

All actions in `ci.yml` and `release.yml` are Node 24-compatible **except** for
`googleapis/release-please-action`:

| Workflow | Action | Node Runtime |
|---|---|---|
| ci.yml | `actions/checkout@v5` | Node 24 ✅ |
| ci.yml | `actions/setup-go@v6` | Node 24 ✅ |
| ci.yml | `golangci/golangci-lint-action@v9.2.0` | Node 24 ✅ |
| release.yml | `actions/checkout@v5` | Node 24 ✅ |
| release.yml | `actions/setup-go@v6` | Node 24 ✅ |
| release.yml | `googleapis/release-please-action@v4.4.0` | Node 20 ⚠️ (see below) |

**Blocker — `googleapis/release-please-action@v4.4.0` (Node 20):**

- Latest upstream release as of March 2026 is v4.4.0; it still declares
  `runs.using: node20` in its `action.yml`.
- Upstream tracking issue: `googleapis/release-please-action#1162`.
- `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24: "true"` is set in `release.yml` as a
  mitigation — this makes the runner execute the action with Node 24, but GitHub
  still emits a `##[warning]` annotation because the action's metadata declares
  `node20`.  The action runs correctly under Node 24.
- **No action needed until June 2, 2026**, when GitHub will enforce Node 24 as
  the default and may break Node 20 actions.
- **Next step:** When `googleapis/release-please-action` publishes a Node 24
  release (v4.x patch or v5), update the pin in `release.yml` and remove the
  `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24` env var.

### GoReleaser Signing Notes

- GoReleaser signs the published `checksums.txt` file in CI.
- `release.yml` imports the CI signing key with `crazy-max/ghaction-import-gpg@v6`.
- Required repository secrets:
  - `GPG_PRIVATE_KEY`
  - `GPG_PASSPHRASE`

# Deletions

Don't delete files or directories, when you'd do a delete instead move to .archive/

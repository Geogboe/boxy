# AGENTS.md

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Project

- **Module:** `github.com/Geogboe/boxy`
- **Go version:** 1.22
- **Dependencies:** cobra (CLI), yaml.v3 (config parsing)

## Commands

```bash
task build            # Build ./boxy binary
task serve            # Run boxy serve (daemon mode)
task serve:once       # Run boxy serve --once (single reconciliation pass)
task go:run -- <args> # Run boxy via go run with arbitrary args
go test ./...         # Run all tests
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

# Deletions

Don't delete files or directories, when you'd do a delete instead move to .archive/

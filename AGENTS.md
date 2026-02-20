# AGENTS.md

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Architectural Notes (Living)

### Pools and Resources

- Pools are homogeneous inventories of resources (see `internal/core/model/pool.go` and `internal/core/model/resource_collection.go`).
- Pools are homogeneous inventories of resources (see `pkg/boxy/model/pool.go` and `pkg/boxy/model/resource_collection.go`).
- **Resources are single-use:** when a resource is allocated into a sandbox, it is never returned to a pool. (ADR-0002)

### Sandboxes

- A sandbox is an environment that can be as small as a single resource or as large as a full lab.
- Preferred phrasing when describing compositions:
  - "container sandbox" (1 container)
  - "3 VM lab sandbox" (multi-VM lab)
  - "2 container, 3 VM, 1 share sandbox" (heterogeneous composition)


## ADRs

When decisions are made, save them as ADR documents in /docs/adr. This is a living document, so feel free to update it as needed. When an ADR is updated, add a note at the end of the document describing the change and the date it was made.

## My Values

- DRY
- Clean code
- Good documentation
- Architectural sounds which doesn't necesasrily mean "simple" but is well thought out and maintainable as project expands.

## AI-First Workflow Notes

- Cost model differs from human dev cycles: refactors are cheap when an agent can apply wide changes quickly, resolve merges/rebases, and keep `go test ./...` green.
- Bias toward a single source of truth: remove duplication promptly and update all call sites together (avoid parallel “old vs new” models).
- Treat “no regressions” as “no regressions covered by tests”: add/extend targeted tests whenever behavior changes.

## Development Notes

The primary developer comes from an OOP background, so there may be a bias toward OOP patterns. However, the codebase is in Go, which encourages composition over inheritance and has its own idiomatic patterns. The agent should be mindful of this and strive to write idiomatic Go code while adhering to the project's values and architectural guidelines.

Don't think about "simple for v1" I like to to think about the entire architecture when I'm designing personal experiemental projects like this. I want to make sure that the architecture is sound and can support future growth and changes. This means that I may choose to implement certain features or patterns that may not be strictly necessary for v1, but will make the codebase more maintainable and scalable in the long run.

## Taskfile

Wrap repeated commands in Taskfile.yaml for ease of use. For example, if there are common commands for running tests, building the project, or deploying, they can be added to the Taskfile for quick execution. This also helps maintain consistency across the team and reduces the likelihood of errors when running complex commands.

## Tools

gopls is available locally for code navigation, refactoring, and linting.
# AGENTS.md

Periodically update this document with guidelines, architectural decisions, lessons learned, and development workflows for AI assistants contributing to the Boxy project.

## Architectural Notes (Living)

### Pools and Resources

- Pools are homogeneous inventories of resources (see `internal/core/model/pool.go` and `internal/core/model/resource_collection.go`).
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
# ADR-009: Preheating and Recycling Semantics

## History

```yaml
Origin: "docs/v1-prerelease/02-preheating-recycling.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
CreatedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Derived from v1-preheating plan and promoted to ADR-009 for canonical semantics."
```

**Date**: 2025-11-23
**Status**: Proposed

## Context

The v1-prerelease included a preheating and recycling model to balance cost and startup latency for resources. Several terms have been used in docs: "warm pools", "preheating", "preheated resources", and "recycling". We need a single canonical model and semantics so the pool and allocator systems behave predictably and developers can reason about resource lifecycle.

## Decision

Adopt the following semantics and API for pools:

- `cold` (Provisioned): A resource that has been created but is not started; provisioning may be slow.
- `ready` or `warm`: A resource that is started and ready to be allocated instantly.
- `min_ready`: Pool configuration parameter to define the minimum number of `ready` resources to maintain.
- `preheating`: Optional pool configuration that controls whether `min_ready` is enforced (true/false). If `preheating.enabled` is false, `min_ready` can be 0 and resources remain cold until allocation.
- `recycling`: Pool policy specifying whether and how often warm resources are rotated (e.g., `recycle_interval`) to prevent drift and ensure a clean baseline.

## Behavior

1. During `boxy serve`, each pool enforces `min_ready` with an asynchronous replenishment process if `preheating.enabled` is true.
2. Allocation prefers `ready` resources; if none are available and `preheating` is disabled or `min_ready` is not sufficient, allocate from `cold` resources by performing a start or boot path.
3. On expiration or destroy, resources are always destroyed and not returned to `ready` (security requirement).
4. `recycling.recycle_interval` when set will trigger a background rotation where a `ready` resource is destroyed and replaced to keep images fresh; this is asynchronous and does not affect allocation availability as replacement occurs before the old resource is destroyed.

## Implementation Notes

- Add `preheating` config blocks to `schema` and docs, with `enabled: true/false` and `count`.
- Implement a background worker to enforce `min_ready` per pool.
- Add a `recycler` background worker implementing `recycle_interval` to safely refresh resources.
- Tests and E2E coverage must show that allocation behaves correctly in cold/warm scenarios.

## Consequences

- Predictable user experience: `ready` resources are allocated instantly; `cold` resources incur boot time.
- Operators can tune cost vs latency by setting preheating behavior.
- Increased complexity in pool managers, requiring robust testing of background workers and concurrency controls.

## References

- `docs/v1-prerelease/02-preheating-recycling.md`
- `AGENTS.md` (preheating mentions)
- `internal/core/pool` code for current semantics

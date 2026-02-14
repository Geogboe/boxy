# ADR 0002: Resources Never Return to a Pool

Status: Accepted

## Context

Pools can maintain a preheated inventory of resources for fast sandbox creation.
When a resource is taken from a pool and used in a sandbox, we need to define
whether it can ever be returned to the pool (recycled) or must always be
destroyed.

## Decision

Resources are single-use and are never returned to a pool.

- Pool inventory is a supply of *unused* resources.
- When a resource is allocated into a sandbox, it leaves the pool permanently.
- When the sandbox is destroyed, its resources are destroyed (not recycled).

## Rationale

- Stronger isolation guarantees (less cross-contamination between sandboxes).
- Simpler mental model: pools preheat, sandboxes consume.
- Avoids complex "cleanup to safe baseline" logic that recycling requires.

## Consequences

- Pools must replenish inventory continuously to maintain `min_ready`.
- Resource lifecycle does not include a "return to pool" path.
- Any future "reuse" capability would need a new concept and ADR.


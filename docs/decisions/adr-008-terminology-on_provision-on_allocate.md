# ADR-008: Terminology: `on_provision` and `on_allocate`

## History

```yaml
Origin: "docs/v1-prerelease/03-terminology-updates.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
CreatedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Derived from v1-prerelease terminology update; promotes consistent hook names across docs and code."
```

**Date**: 2025-11-23
**Status**: Proposed

## Context

During the v1-prerelease, a change in hook terminology was proposed to make intent clearer for lifecycle hooks run by the system. The previous naming was inconsistent across docs and examples (`hooks`, `after_provision`, `before_allocate`, `personalization`, `finalization`).

This project includes two hook phases conceptually:

- A hook that runs when a resource is provisioned for the pool (cold state), used for heavy finalization tasks that run once per resource.
- A hook that runs at allocation time (fast, per-user personalization) to prepare a resource for a specific sandbox or user.

## Decision

We adopt the following terminology across docs and code going forward:

- `on_provision` — run after a provider has provisioned a resource and while it is in a cold state (this is slow, background work; run as part of pool warming). Finalization tasks such as OS setup, snapshot creation, or long-running validation belong here.
- `on_allocate` — run when a resource is allocated to a sandbox (fast, short-lived personalization). This is used for per-user tasks such as creating users, adding credentials, granting access, or setting hostnames.

Update: `AGENTS.md`, `docs/architecture/HOOKS.md`, and `docs/guides/getting-started.md` will be updated to use `on_provision` and `on_allocate` consistently. Implementations (provider hooks / executor) should accept `on_provision` / `on_allocate` names in pool configuration.

## Consequences

- Documentation and sample configs will be updated to use `on_provision` and `on_allocate`.
- Tooling (example configs, README) may require migration notes for existing scripts that use old hook names.
- Backward compatibility: The hook executor should continue to support deprecated names for a transition period and log warnings about new names.

## Implementation

- Task: Update `AGENTS.md` and all sample configs to use `on_provision` and `on_allocate`.
- Task: Update the hook executor to support both names with a deprecation warning.
- Task: Add a migration guide entry in `docs/v1-prerelease` and link to ADR-008.

## Rationale

Using `on_provision` and `on_allocate` clearly states the lifecycle timing and expected semantics of each hook, helping maintainers and contributors reason about performance and expected behavior.

## References

- `docs/v1-prerelease/03-terminology-updates.md`
- `docs/architecture/HOOKS.md`
- `AGENTS.md`

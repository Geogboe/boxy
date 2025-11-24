# ADR-011: Configuration File Location & Discovery

**Date**: 2025-11-23
**Status**: Proposed

## Context

v1-prerelease planning proposed changing Boxy's default configuration location from `~/.config/boxy/boxy.yaml` to a project-local `./boxy.yaml`. The code currently uses `viper` and falls back to `~/.config/boxy/boxy.yaml` (see `internal/config/config.go`).

We must balance expectations for developer users (local project configs) vs server/service deployments (system or per-user config locations).

## Decision

We adopt a **search-order-based discovery** approach that preserves backwards compatibility and enables both developer-local and service-deployment behaviors:

1. **Keep current discovery order** (explicit flag > current dir `./boxy.yaml` > `~/.config/boxy/boxy.yaml` > `/etc/boxy/boxy.yaml`) — this preserves existing behavior.
2. **Make `./boxy.yaml` the preferred project local file** for interactive and dev workflows and the default for `boxy serve` when invoked from a working directory.
3. **Add a deprecation path**: display a clear deprecation warning when `~/.config/boxy/boxy.yaml` is used by `boxy serve` and encourage migration to `./boxy.yaml` (or the explicit `--config` flag) for server deployments.
4. **Server deployments** (systemd/docker) are encouraged to provide an explicit `--config` path or use a system path (e.g., `/etc/boxy/boxy.yaml`) as needed.

## Implementation

- Keep `internal/config`'s search order (no runtime behavior change) while updating the `boxy init` command to default to `./boxy.yaml` in interactive mode.
- Display deprecation warnings when `~/.config/boxy/boxy.yaml` is used by `boxy serve` (non-local server runs)
- Add documentation about recommended patterns for configs in `docs/guides/getting-started.md` and `docs/planning/v1-prerelease/11-config-location.md`.

## Consequences

- Migration: Admins and users using `~/.config/boxy` will be warned and guided to move to `./boxy.yaml` when appropriate.
- Backwards compatibility: None broken in v1.

## History

```yaml
Origin: "docs/v1-prerelease/11-config-location.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Proposed ADR for config location; keep current search behavior, update init to create `./boxy.yaml`, and deprecate legacy user location."
```

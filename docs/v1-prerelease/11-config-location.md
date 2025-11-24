# 11: Config File Location

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/11-config-location.md` for planning and migration details.

## History

```yaml
Origin: "docs/v1-prerelease/11-config-location.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning content moved to `docs/planning/v1-prerelease/11-config-location.md`."
```

---

## Metadata

```yaml
feature: "Config Location Fix"
slug: "config-location"
status: "done"
priority: "high"
type: "fix"
effort: "small"
breaking_change: true
```

---

## Overview

**Current problem**: Config at `~/.config/boxy/boxy.yaml` (user-specific)
**Correct approach**: Config at `./boxy.yaml` (project-specific)

**Rationale**: Boxy is a **service orchestrator**, not a user CLI tool. Config should be project-specific like Docker Compose, not user-specific like Git.

---

## Config Discovery Strategy

**Priority order:**

1. **Explicit flag** (highest priority)
    `boxy serve --config /path/to/custom.yaml`

2. **Environment variable**
    `export BOXY_CONFIG=/path/to/custom.yaml`

3. **Current directory** (project-specific)
    - `./boxy.yaml`
    - `./boxy.yml` (fallback)

4. **Deprecated user directory** (with warning)
    - `~/.config/boxy/boxy.yaml`
    - If found, print a clear deprecation warning.

5. **Error if not found**
    - If none of the above are found, error out with a helpful message.

---

## Implementation

The implementation will update the config loader in `internal/config/loader.go` to follow the discovery strategy above. It will also update the `boxy init` command to create the config in the current directory.

## Migration from Old Location

**v1.0 (This release)**: Support both locations, but show a prominent deprecation warning when the old location is used.
**v1.1 (Future release)**: Remove support for `~/.config/boxy/` entirely.

Users will be guided to move their configuration file from `~/.config/boxy/boxy.yaml` to their project directory (e.g., `./boxy.yaml`) and to update any absolute paths (like for the database) to be relative.

See [migration-guide.md](migration-guide.md) for detailed steps.

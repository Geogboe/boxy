# [v1] Config File Location Fix - Move to Current Directory

**From**: [V1_IMPLEMENTATION_PLAN.md Section 11](../V1_IMPLEMENTATION_PLAN.md#11-config-file-location-fix)
**Priority**: CRITICAL - Breaks Docker patterns, affects all users
**Labels**: v1, breaking-change, critical

## Problem

Current config location (`~/.config/boxy/boxy.yaml`) is not standard for service-oriented tools and causes issues:

- ❌ Doesn't work well with Docker volume mounts
- ❌ Difficult to manage multiple Boxy instances
- ❌ Awkward for CI/CD pipelines
- ❌ Not clear which config is active when running from different directories

## Solution

Move config to current working directory: `./boxy.yaml`

## Implementation Tasks

### Phase 1: Config Loader Updates

- [ ] Update `internal/config/loader.go` with new discovery logic:
  1. `--config` flag (highest priority)
  2. `BOXY_CONFIG` environment variable
  3. `./boxy.yaml` (current directory)
  4. `./boxy.yml` (fallback)
  5. Error if none found
- [ ] Add backward compatibility for `~/.config/boxy/boxy.yaml` with deprecation warning
- [ ] Add environment variable overrides for Docker

### Phase 2: CLI Command Updates

- [ ] Update `boxy init` to create `./boxy.yaml` in current directory
- [ ] Update default `--config` flag to use `./boxy.yaml`
- [ ] Update `--db` flag default to use `./boxy.db`

### Phase 3: Documentation Updates

- [ ] Update README.md - all config path examples
- [ ] Update V1_IMPLEMENTATION_PLAN.md - all config examples
- [ ] Update docs/CONFIG_REFERENCE.md - config location docs
- [ ] Update docs/DEBUGGING_GUIDE.md - log/config paths
- [ ] Update boxy.example.yaml - header comments
- [ ] Update all code examples in docs/

### Phase 4: Testing

- [ ] Unit tests for config discovery logic
- [ ] Test backward compatibility warning
- [ ] Test environment variable overrides
- [ ] E2E tests with new config location
- [ ] Docker deployment tests

## Acceptance Criteria

- [ ] `boxy init` creates `./boxy.yaml` in current directory
- [ ] Config discovery follows documented priority order
- [ ] Backward compatibility warning shown for old location
- [ ] All documentation updated with new paths
- [ ] Docker examples work with new config location
- [ ] All existing tests pass
- [ ] New tests for config discovery

## Migration Plan

**v1.0**: Support both locations, warn on old location
**v1.1**: Remove support for `~/.config/boxy/`, require `./boxy.yaml`

## Files to Update

Search and replace `~/.config/boxy` → `./boxy.yaml`:

```bash
grep -r "~/.config/boxy" docs/
grep -r ".config/boxy" internal/
grep -r "config/boxy" cmd/
```

## Related Issues

- Depends on: Docker/Compose support (#TBD)
- Blocks: Production deployment guide (#TBD)

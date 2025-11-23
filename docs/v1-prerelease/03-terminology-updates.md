# 03: Terminology Updates

---

## Metadata

```yaml
feature: "Terminology Updates"
slug: "terminology-updates"
status: "not-started"
priority: "high"
type: "refactor"
effort: "small"
depends_on: []
enables: ["consistency", "clarity"]
testing: ["unit"]
breaking_change: true
week: "1-2"
related_docs:
  - "01-architecture-refactor.md"
  - "02-preheating-recycling.md"
```

---

## Overview

Update terminology across codebase and documentation for consistency and clarity:
- Hook names: `after_provision` → `on_provision`, `before_allocate` → `on_allocate`
- Pool terminology: "warm pools" → "preheated resources"
- Resource state names: Align with new lifecycle

---

## Changes

### 1. Hook Naming

**Decision**: Use clear, action-based names

**Old naming:**
- `after_provision` → Finalization hook
- `before_allocate` → Personalization hook

**New naming:**
- `on_provision` → Runs after provider creates resource (cold state)
- `on_allocate` → Runs when user requests resource

**Rationale:**
- "on_X" is clearer than "after_X" or "before_X"
- Describes WHEN it runs, not position in sequence
- Matches common event naming patterns

**Update in:**
```go
// internal/hooks/hooks.go
type HookPoint string

const (
    HookPointOnProvision HookPoint = "on_provision"  // Was: after_provision
    HookPointOnAllocate  HookPoint = "on_allocate"   // Was: before_allocate
)
```

**Configuration:**
```yaml
# OLD
hooks:
  after_provision:
    - type: script
      inline: echo "provisioned"
  before_allocate:
    - type: script
      inline: echo "allocating"

# NEW
hooks:
  on_provision:
    - type: script
      inline: echo "provisioned"
  on_allocate:
    - type: script
      inline: echo "allocating"
```

---

### 2. Pool Terminology

**Old**: "Warm pools" vs "Cold pools"
**New**: "Preheated resources" vs "Cold resources"

**Rationale:**
- Pools don't have temperature, resources do
- "Preheated" is clearer than "warm" (matches oven metaphor)
- "Cold" means stopped/provisioned but not running
- Pools can have BOTH cold and preheated resources

**Examples:**
```markdown
# OLD (confusing)
"Create a warm pool for instant allocation"
"Cold pools reduce costs but add latency"

# NEW (clear)
"Enable preheating to keep some resources warm for instant allocation"
"Resources start cold; preheating keeps N warm"
"Pool maintains mix of cold and preheated resources"
```

**Update in:**
- CLAUDE.md
- README.md
- All docs/
- Code comments

---

### 3. Resource State Names

See [02-preheating-recycling.md](02-preheating-recycling.md) for complete state definitions.

**New states:**
```go
const (
    StateProvisioned  ResourceState = "provisioned"   // Created but cold (stopped)
    StateWarming      ResourceState = "warming"       // Starting up
    StateReady        ResourceState = "ready"         // Running and warm
    StateAllocating   ResourceState = "allocating"    // Being allocated
    StateAllocated    ResourceState = "allocated"     // In use
    StateRecycling    ResourceState = "recycling"     // Being recycled
    StateDestroyed    ResourceState = "destroyed"     // Gone
    StateError        ResourceState = "error"         // Failed
)
```

**Clarity improvements:**
- "Provisioned" = cold/stopped (not "ready")
- "Ready" = warm/running (available for allocation)
- "Warming" = transitioning from cold to ready

---

## Implementation Tasks

### Task 3.1: Update Hook Constants

**File**: `internal/hooks/hooks.go`

```go
type HookPoint string

const (
    HookPointOnProvision HookPoint = "on_provision"
    HookPointOnAllocate  HookPoint = "on_allocate"

    // DEPRECATED: Backwards compatibility
    HookPointAfterProvision  HookPoint = "after_provision"  // Use on_provision
    HookPointBeforeAllocate  HookPoint = "before_allocate"  // Use on_allocate
)

// Normalize converts old names to new names with warning
func (h HookPoint) Normalize(logger *logrus.Logger) HookPoint {
    switch h {
    case HookPointAfterProvision:
        logger.Warn("Hook 'after_provision' is deprecated, use 'on_provision'")
        return HookPointOnProvision
    case HookPointBeforeAllocate:
        logger.Warn("Hook 'before_allocate' is deprecated, use 'on_allocate'")
        return HookPointOnAllocate
    default:
        return h
    }
}
```

---

### Task 3.2: Update Configuration Parsing

**File**: `internal/config/hooks.go`

```go
func parseHooks(raw map[string]interface{}) (HookConfig, error) {
    config := HookConfig{}

    // Parse on_provision
    if val, ok := raw["on_provision"]; ok {
        config.OnProvision = parseHookList(val)
    }

    // Parse on_allocate
    if val, ok := raw["on_allocate"]; ok {
        config.OnAllocate = parseHookList(val)
    }

    // BACKWARDS COMPATIBILITY
    if val, ok := raw["after_provision"]; ok {
        log.Warn("'after_provision' is deprecated, use 'on_provision'")
        config.OnProvision = parseHookList(val)
    }
    if val, ok := raw["before_allocate"]; ok {
        log.Warn("'before_allocate' is deprecated, use 'on_allocate'")
        config.OnAllocate = parseHookList(val)
    }

    return config, nil
}
```

---

### Task 3.3: Update Example Configs

**Files**: All `examples/*.yaml`

```yaml
# examples/docker-pool.yaml
pools:
  - name: ubuntu-containers
    type: container
    backend: docker

    hooks:
      on_provision:  # UPDATED
        - type: script
          shell: bash
          inline: |
            echo "Resource provisioned"

      on_allocate:  # UPDATED
        - type: script
          shell: bash
          inline: |
            echo "Resource allocated to sandbox"
```

---

### Task 3.4: Update Documentation

**Global search and replace:**

```bash
# Find all references to old terminology
grep -r "after_provision" docs/
grep -r "before_allocate" docs/
grep -r "warm pool" docs/
grep -r "cold pool" docs/

# Replace (review each change)
sed -i 's/after_provision/on_provision/g' docs/**/*.md
sed -i 's/before_allocate/on_allocate/g' docs/**/*.md
sed -i 's/warm pool/preheated resource/g' docs/**/*.md
sed -i 's/cold pool/cold resource/g' docs/**/*.md
```

**Key files to update:**
- `CLAUDE.md` - Core concepts section
- `docs/architecture/HOOKS.md` - Hook documentation
- `docs/architecture/MVP_DESIGN.md` - Component descriptions
- `README.md` - Quick start examples

---

## Migration Path

### Backwards Compatibility

**Phase 1 (v1.0-v1.2)**: Both old and new names work
```yaml
# Both work, but old names log warnings
hooks:
  after_provision: [...]   # Works with warning
  on_provision: [...]      # Preferred
```

**Phase 2 (v1.3+)**: Deprecation warnings become errors
```yaml
# Old names cause config validation errors
hooks:
  after_provision: [...]   # ERROR: Use 'on_provision'
```

**Phase 3 (v2.0)**: Old names removed completely

---

## Testing

### Unit Tests

```go
// internal/hooks/hooks_test.go
func TestHookPoint_Normalize(t *testing.T) {
    tests := []struct {
        input    HookPoint
        expected HookPoint
        warns    bool
    }{
        {"on_provision", "on_provision", false},
        {"on_allocate", "on_allocate", false},
        {"after_provision", "on_provision", true},  // Deprecated
        {"before_allocate", "on_allocate", true},   // Deprecated
    }

    for _, tt := range tests {
        logger := &mockLogger{}
        result := tt.input.Normalize(logger)
        assert.Equal(t, tt.expected, result)
        if tt.warns {
            assert.True(t, logger.HasWarning())
        }
    }
}

// internal/config/hooks_test.go
func TestParseHooks_BackwardsCompatibility(t *testing.T) {
    // Test old hook names still work with warnings
    raw := map[string]interface{}{
        "after_provision": []interface{}{
            map[string]interface{}{"type": "script"},
        },
    }

    config, err := parseHooks(raw)
    assert.NoError(t, err)
    assert.Len(t, config.OnProvision, 1)  // Mapped to new name
}
```

---

## Success Criteria

- ✅ New hook names implemented in code
- ✅ Backwards compatibility maintained (old names work with warnings)
- ✅ All example configs updated
- ✅ All documentation updated
- ✅ Unit tests pass
- ✅ Config validation tests pass
- ✅ No breaking changes for users (warnings only)

---

## User Impact

### For Existing Users

**Good news**: Your configs still work!

**Action required** (eventually):
```yaml
# Update your configs when convenient
# Old names work in v1.0-v1.2, but log warnings
hooks:
  on_provision: [...]   # Use this
  on_allocate: [...]    # Use this
```

### For New Users

Use new names from the start:
- `on_provision` - When resource is created
- `on_allocate` - When resource is allocated to sandbox

---

## Related Documents

- [02: Preheating & Recycling](02-preheating-recycling.md) - New resource states
- [HOOKS.md](../architecture/HOOKS.md) - Hook documentation
- [migration-guide.md](migration-guide.md) - Breaking changes guide

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None (can be done anytime)
**Blocking**: None (improves clarity but not required for other features)

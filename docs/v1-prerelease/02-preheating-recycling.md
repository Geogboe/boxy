# 02: Preheating & Recycling System

---

## Metadata

```yaml
feature: "Preheating & Recycling"
slug: "preheating-recycling"
status: "not-started"
priority: "critical"
type: "feature"
effort: "large"
depends_on: ["architecture-refactor"]
enables: ["resource-efficiency", "instant-allocation"]
testing: ["unit", "integration", "e2e"]
breaking_change: true
week: "1-2"
related_docs:
  - "01-architecture-refactor.md"
  - "03-terminology-updates.md"
```

---

## Overview

Implement cold vs warm resource management:

- **Cold resources**: Provisioned but stopped (low cost, slower allocation)
- **Warm resources**: Running and ready (instant allocation, higher cost)
- **Preheating**: Keep N resources warm for instant allocation
- **Recycling**: Periodically refresh resources to prevent drift

**Value**: Balance cost vs speed. Pools maintain mix of cold/warm resources based on demand.

---

## Concept: Cold vs Warm Resources

```text
Pool Resources State Distribution:
┌────────────────────────────────────────┐
│  min_ready: 10  (total resources)     │
│  ├─ Cold:  7 (provisioned but stopped)│
│  └─ Warm:  3 (running/preheated)      │
└────────────────────────────────────────┘

On allocation request:
  - If warm resource available → instant (< 5 sec)
  - If only cold available → start resource → allocate (30-60 sec)
```

---

## New Resource States

```go
type ResourceState string

const (
    StateProvisioned  ResourceState = "provisioned"   // Created but cold (stopped)
    StateWarming      ResourceState = "warming"       // Starting up
    StateReady        ResourceState = "ready"         // Running and warm (available)
    StateAllocating   ResourceState = "allocating"    // Being allocated
    StateAllocated    ResourceState = "allocated"     // In use
    StateRecycling    ResourceState = "recycling"     // Being recycled
    StateDestroyed    ResourceState = "destroyed"     // Gone
    StateError        ResourceState = "error"         // Failed
)
```

**State Transitions:**

```text
Provisioned → Warming → Ready → Allocating → Allocated → Destroyed
                ↑
                └────── Recycling ────────┘
```

---

## Configuration

```yaml
pools:
  - name: win-test-vms
    type: vm
    backend: hyperv
    min_ready: 10        # Total resources (cold + warm)
    max_total: 20

    preheating:
      enabled: true
      count: 3           # Keep 3 running/warm
      recycle_interval: 1h
      recycle_strategy: rolling  # "rolling" or "all-at-once"
      warmup_timeout: 5m
```

---

## Implementation Tasks

See V1_IMPLEMENTATION_PLAN.md sections 2.1-2.5 for detailed implementation:

- Preheating worker (maintains warm count)
- Recycling worker (refreshes resources)  
- Updated provisionOne (creates cold resources)
- warmResource method (cold → warm transition)

---

## Testing

```go
// Integration test
func TestPreheating_DockerContainers(t *testing.T) {
    // 1. Create pool with preheating enabled
    // 2. Verify cold resources created
    // 3. Verify preheating worker warms resources
    // 4. Allocate warm resource (fast < 5s)
    // 5. Allocate cold resource (slower but works)
}
```

---

## Success Criteria

- ✅ Resources created as cold (Provisioned state)
- ✅ Preheating worker maintains warm count
- ✅ Warm resources allocated instantly
- ✅ Cold resources can be warmed on-demand
- ✅ Recycling worker refreshes resources
- ✅ Rolling strategy maintains availability
- ✅ All-at-once strategy works

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: 01-architecture-refactor
**Blocking**: None

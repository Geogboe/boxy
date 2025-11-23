# ADR-005: Pool and Sandbox as Peer Components with Internal Allocator

**Date**: 2024-11-22
**Status**: Accepted
**Supersedes**: None
**Related**: ADR-002 (Provider Architecture)

---

## Context

The original Boxy architecture had tight coupling between Pool and Sandbox components:

**Problems identified:**

1. **Tight coupling**: Sandbox called `pool.Allocate()` directly, creating dependency
2. **Dual ownership**: Both Pool and Sandbox tracked resource state, causing potential inconsistencies
3. **Unclear responsibility**: Resource lifecycle management split across components
4. **Limited Pool management**: Pool couldn't be managed independently via CLI/API
5. **Scalability concerns**: Difficult to add features like advanced scheduling or multi-pool allocation

**Original architecture:**

```text
┌──────────────┐
│   Sandbox    │ ← Depends on Pool (calls pool.Allocate())
│              │
└──────┬───────┘
       ↓
┌──────────────┐
│    Pool      │ ← Owns resources even after allocation
│              │
│ resources:   │
│  [res-1]     │ ← res-1.SandboxID = "sb-123" (dual ownership!)
│  [res-2]     │
└──────────────┘
```

**Specific issues:**

- Resource `res-1` exists in Pool's resource list
- BUT also has `res-1.SandboxID = "sb-123"` pointing to Sandbox
- Sandbox has `ResourceIDs = ["res-1"]` pointing to Resource
- Three-way reference creates confusion about ownership

---

## Decision

We will refactor to a **peer architecture** with an internal **Allocator** component that orchestrates resource movement between Pool and Sandbox.

### New Architecture

```text
User-Facing Components (Equal Peers):
┌──────────────┐          ┌──────────────┐
│    Pool      │          │   Sandbox    │
│              │          │              │
│ Manages      │          │ Manages      │
│ unallocated  │          │ allocated    │
│ resources    │          │ resources    │
└──────┬───────┘          └──────┬───────┘
       │                         │
       │  Both query/update      │
       └────────┬─────────┬──────┘
                ↓         ↓
Internal Components:
         ┌──────────────────────┐
         │     Allocator        │ ← Internal orchestrator
         │                      │    (not user-facing!)
         │ • Tracks all         │
         │   resources          │
         │ • Manages Pool →     │
         │   Sandbox transitions│
         │ • Runs on_allocate   │
         │   hooks              │
         │ • Single source of   │
         │   truth for ownership│
         └──────┬───────────────┘
                ↓
         ┌──────────────────────┐
         │  ResourceRepository  │
         │  (Database/Storage)  │
         └──────────────────────┘
```

### Component Responsibilities

#### Pool (User-Facing)

**Manages unallocated resources:**

- Provisions resources via Provider
- Runs `on_provision` hooks
- Maintains `min_ready` count
- Health checking
- Preheating (keeps some resources warm)
- Recycling (refreshes resources)
- Provides query interface for available resources

**Does NOT:**

- Allocate resources to sandboxes
- Track sandbox ownership
- Run `on_allocate` hooks

#### Sandbox (User-Facing)

**Manages allocated resources:**

- Creates sandbox records
- Coordinates multi-resource allocation (via Allocator)
- Tracks lifecycle (Creating, Ready, Expiring, Destroyed)
- Auto-cleanup of expired sandboxes

**Does NOT:**

- Know about Pool internals
- Track resources directly (queries via Allocator)
- Call Provider directly

#### Allocator (Internal)

**Orchestrates resource movement:**

- Tracks resource ownership (pool vs sandbox)
- Allocates resources from Pool to Sandbox
- Runs `on_allocate` hooks
- Releases resources from Sandbox (destroys them)
- Provides query interface for both Pool and Sandbox
- Single source of truth for resource state

**Key point:** Allocator is **100% internal** - users never interact with it directly.

### Resource Ownership Model

| Resource State | Owned By | Tracked By | SandboxID |
| ---------------- | ---------- | ------------ | ----------- |
| Provisioned | Pool | Allocator | nil |
| Ready | Pool | Allocator | nil |
| Allocated | Sandbox | Allocator | set |
| Destroyed | None | Repository | nil |

**Clear ownership rules:**

- Unallocated resources (Provisioned, Ready) → Pool owns
- Allocated resources → Sandbox owns
- Allocator tracks all, acts as intermediary

### API/CLI Impact

**User-facing commands remain unchanged:**

```bash
# Sandbox operations (no change)
boxy sandbox create -p pool:1 -d 1h
boxy sandbox destroy <id>

# Pool operations (existing)
boxy pool ls
boxy pool stats <pool-name>

# Pool operations (NEW in v1)
boxy pool create --config pool.yaml
boxy pool start <pool-name>
boxy pool stop <pool-name>
boxy pool scale <pool-name> --min-ready 5 --preheated 2
boxy pool inspect <pool-name>
boxy pool resources <pool-name>
boxy pool recycle <pool-name>
```

**Internal changes:**

- `boxy sandbox create` → calls `sandboxManager.Create()` → calls `allocator.AllocateFromPool()`
- `boxy pool stats` → calls `pool.GetStats()` → queries `allocator` for resource counts

---

## Rationale

### Why Peer Architecture?

**Benefits:**

1. **Separation of concerns**: Each component has clear, distinct responsibility
2. **Independent management**: Pool can be managed via CLI without affecting Sandbox
3. **Testability**: Can test Pool and Sandbox independently
4. **Extensibility**: Easy to add features like multi-pool allocation, advanced scheduling
5. **No circular dependencies**: Clean dependency graph

**Comparison with alternatives:**

| Approach | Coupling | Clarity | Extensibility |
| ---------- | ---------- | --------- | --------------- |
| Original (Sandbox → Pool) | High | Medium | Low |
| Peer + Allocator | Low | High | High |
| Sandbox owns everything | Medium | Low | Medium |

### Why Internal Allocator?

**Considered alternatives:**

**Alternative 1: Sandbox owns Pool**

```text
Sandbox → Pool (dependency reversed)
```

- ❌ Pool still can't be managed independently
- ❌ Doesn't solve ownership problem
- ❌ Just moves the coupling

**Alternative 2: Both depend on shared ResourceManager**

```text
Sandbox → ResourceManager ← Pool
```

- ✅ Decoupled
- ❌ "ResourceManager" is generic, unclear purpose
- ❌ Feels too enterprise-y

**Alternative 3: Event-driven with message queue**

```text
Pool publishes → Queue → Sandbox subscribes
```

- ✅ Fully decoupled
- ❌ Over-engineered for v1
- ❌ Adds operational complexity
- ❌ Overkill for single-host deployment

**Chosen: Allocator (internal orchestrator)**

- ✅ Clear purpose (allocates resources)
- ✅ Simple to understand
- ✅ Internal (no API surface bloat)
- ✅ Single source of truth
- ✅ Easy to test
- ✅ Room to evolve (can add queue later if needed)

### Why Not User-Facing?

**Question:** Should users interact with Allocator?

**Answer:** No, keep it internal.

**Reasoning:**

- Users think in terms of "Pools" and "Sandboxes"
- Allocator is an implementation detail
- Exposing it would complicate the mental model
- CLI remains simple: `boxy pool ...` and `boxy sandbox ...`
- Allocator can evolve internally without breaking API

---

## Consequences

### Positive

- ✅ **Clear separation of concerns**: Pool and Sandbox have distinct responsibilities
- ✅ **Single source of truth**: Allocator tracks resource ownership definitively
- ✅ **Pool is first-class**: Can be managed independently via CLI/API
- ✅ **Better testability**: Each component can be tested in isolation
- ✅ **Extensibility**: Easy to add features like:
  - Multi-pool allocation strategies
  - Advanced scheduling (capacity-aware, cost-optimized)
  - Resource migration between pools
- ✅ **No API breakage**: User-facing commands remain the same
- ✅ **Future-proof**: Architecture supports distributed agents (v2)

### Negative

- ❌ **Additional abstraction**: One more component to understand
- ❌ **Refactor required**: Need to update existing Pool and Sandbox code
- ❌ **Migration needed**: Minimal (internal only, no user impact)
- ❌ **Slightly more complex**: Three components instead of two

### Risks and Mitigations

| Risk | Impact | Mitigation |
| ------ | -------- | ------------ |
| Refactor introduces bugs | High | Comprehensive tests (unit, integration, E2E) |
| Performance overhead | Low | Allocator is in-memory, negligible latency |
| Complexity confuses contributors | Medium | Clear documentation, architecture diagrams |
| Migration breaks existing deployments | High | Backwards compatibility, migration tool |

---

## Implementation

### Phase 1: Create Allocator

**New file:** `internal/core/allocator/allocator.go`

```go
package allocator

type Allocator struct {
    repository ResourceRepository
    pools      map[string]*pool.Manager
    logger     *logrus.Logger
    mu         sync.RWMutex
}

func NewAllocator(
    repository ResourceRepository,
    pools map[string]*pool.Manager,
    logger *logrus.Logger,
) *Allocator

// Core methods
func (a *Allocator) AllocateFromPool(ctx context.Context, poolName, sandboxID string) (*resource.Resource, error)
func (a *Allocator) ReleaseResources(ctx context.Context, sandboxID string) error
func (a *Allocator) GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*ResourceWithConnection, error)
func (a *Allocator) GetResourcesForPool(ctx context.Context, poolName string) ([]*resource.Resource, error)
```

### Phase 2: Refactor Pool

**Changes to `internal/core/pool/manager.go`:**

**Remove:**

- `Allocate()` method
- `Release()` method

**Add:**

- `GetAvailableResources()` - Query method for Allocator
- `GetAllResources()` - Query method for pool stats

**Keep:**

- `Start()`, `Stop()` - Lifecycle
- `provisionOne()` - Resource creation
- `ensureMinReady()` - Pool replenishment
- Workers (replenishment, health, preheating, recycling)

### Phase 3: Refactor Sandbox

**Changes to `internal/core/sandbox/manager.go`:**

**Constructor:**

```go
// OLD
func NewManager(
    pools map[string]PoolAllocator,
    sandboxRepo SandboxRepository,
    ...
) *Manager

// NEW
func NewManager(
    allocator *allocator.Allocator,  // Takes Allocator instead of pools
    sandboxRepo SandboxRepository,
    ...
) *Manager
```

**Update methods:**

- `allocateResourcesAsync()` - Calls `allocator.AllocateFromPool()`
- `Destroy()` - Calls `allocator.ReleaseResources()`
- `GetResourcesForSandbox()` - Calls `allocator.GetResourcesForSandbox()`

### Phase 4: Update Service

**Changes to `cmd/boxy/commands/serve.go`:**

```go
func (s *Service) Start() error {
    // 1. Load pool configs
    pools := make(map[string]*pool.Manager)
    for _, poolCfg := range s.config.Pools {
        pool, err := pool.NewManager(poolCfg, provider, repo, logger)
        if err != nil {
            return err
        }
        pool.Start()
        pools[poolCfg.Name] = pool
    }

    // 2. Create allocator with all pools
    allocator := allocator.NewAllocator(repo, pools, logger)

    // 3. Create sandbox manager with allocator
    sandboxMgr := sandbox.NewManager(allocator, sandboxRepo, logger)
    sandboxMgr.Start()

    return nil
}
```

### Testing Strategy

**Unit tests:**

- Test Allocator in isolation with mock pools and repository
- Test Pool without Allocator (queries only)
- Test Sandbox with mock Allocator

**Integration tests:**

- Test full flow: Pool provision → Allocator allocate → Sandbox manage
- Test with real Docker provider
- Test concurrent allocations

**E2E tests:**

- Full user workflow: create sandbox, use resource, destroy
- Verify resource ownership transitions correctly

---

## Migration Guide

### For Existing Deployments

**Good news:** No breaking changes!

1. **Code changes are internal** - no API/CLI changes
2. **Database schema unchanged** - existing data works as-is
3. **Configuration unchanged** - existing `boxy.yaml` works

**Migration steps:**

1. Update Boxy binary
2. Restart service
3. Done!

**Rollback:** If issues occur, rollback to previous binary. No data migration needed.

### For Contributors

**What changed:**

- Pool no longer has `Allocate()` / `Release()` methods
- Sandbox takes `Allocator` instead of `map[string]PoolAllocator`
- New `internal/core/allocator` package

**How to update code:**

- Replace `pool.Allocate()` → `allocator.AllocateFromPool()`
- Replace `pool.Release()` → `allocator.ReleaseResources()`
- Query resources via Allocator, not Pool directly

---

## Examples

### Before (Tight Coupling)

```go
// Sandbox directly calls Pool
func (m *SandboxManager) allocateResourcesAsync(sandboxID string, reqs []ResourceRequest) {
    for _, req := range reqs {
        pool, ok := m.pools[req.PoolName]  // Direct pool access
        if !ok {
            return errors.New("pool not found")
        }

        res, err := pool.Allocate(ctx, sandboxID)  // Pool does allocation
        // ...
    }
}
```

### After (Peer Architecture)

```go
// Sandbox calls Allocator (decoupled from Pool)
func (m *SandboxManager) allocateResourcesAsync(sandboxID string, reqs []ResourceRequest) {
    for _, req := range reqs {
        res, err := m.allocator.AllocateFromPool(ctx, req.PoolName, sandboxID)  // Allocator coordinates
        // ...
    }
}
```

**Pool doesn't know about Sandbox. Sandbox doesn't know about Pool. Allocator coordinates.**

---

## Future Enhancements Enabled

This architecture makes the following features easier to implement:

### v1

- ✅ Pool as first-class component (CLI management)
- ✅ Advanced pool inspection
- ✅ Resource recycling

### v2

- Multi-pool allocation strategies
- Capacity-aware scheduling (allocate from least-loaded pool)
- Cost-optimized allocation (allocate from cheapest pool)
- Resource migration (move between pools)

### v3

- Distributed scheduling (allocate across multiple Boxy servers)
- Advanced reservation system
- Resource preemption (priority-based allocation)

---

## Related Documents

- [V1 Implementation Plan](../V1_IMPLEMENTATION_PLAN.md) - Complete v1 specification
- [ADR-002: Provider Architecture](adr-002-provider-architecture.md) - Provider interface design
- [ADR-003: Configuration & State Storage](adr-003-configuration-state-storage.md) - Database schema
- [ADR-004: Distributed Agent Architecture](adr-004-distributed-agent-architecture.md) - v2 distributed agents

---

## Decision Log

| Date | Decision | Rationale |
| ------ | ---------- | ----------- |
| 2024-11-22 | Peer architecture with Allocator | Clear separation of concerns, extensibility |
| 2024-11-22 | Allocator is internal (not user-facing) | Simplifies mental model, cleaner API |
| 2024-11-22 | No database migration needed | Existing schema supports new model |

---

**Status**: ✅ Accepted
**Implementation**: See [V1_IMPLEMENTATION_PLAN.md](../V1_IMPLEMENTATION_PLAN.md)
**Review Date**: After v1 completion (estimate: 3 weeks)

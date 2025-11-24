# 01: Architecture Refactor - Pool/Sandbox Peer Architecture

## History

```yaml
Origin: "docs/v1-prerelease/01-architecture-refactor.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Refactor proposal migrated into planning; see ADR-005 for the accepted decision"
```

> Status: proposed — See `docs/decisions/adr-005-pool-sandbox-peer-architecture.md` for the finalized ADR.

---

## Metadata

```yaml
feature: "Architecture Refactor"
slug: "architecture-refactor"
status: "not-started"
priority: "critical"
type: "refactor"
effort: "large"
depends_on: []
enables:
  - "preheating-recycling"
  - "multi-tenancy"
  - "pool-cli-commands"
  - "distributed-agents"
testing: ["unit", "integration", "e2e"]
breaking_change: false
week: "1-2"
related_docs:
  - "../decisions/adr-005-pool-sandbox-peer-architecture.md"
  - "../architecture/MVP_DESIGN.md"
```

---

## Overview

Refactor Boxy architecture from tight Pool→Sandbox coupling to **peer architecture** with internal **Allocator** component.

### Current Problem

```text
┌──────────────┐
│   Sandbox    │ ← Depends on Pool
│              │
│  calls       │
│  pool.Allocate()
└──────┬───────┘
       ↓
┌──────────────┐
│    Pool      │ ← Owns resources even after allocation
│              │
│ resources:   │
│  [res-1]     │ ← Dual ownership problem
│  [res-2]     │
└──────────────┘
```

**Issues:**

- Tight coupling between Sandbox and Pool
- Dual ownership of resources (both Pool and Sandbox track them)
- Unclear state management
- Pool can't be managed independently

### New Solution

```text
User-Facing Components:
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
         │                      │    (users never see this)
         │ • Tracks all         │
         │   resources          │
         │ • Manages            │
         │   Pool → Sandbox     │
         │   transitions        │
         └──────┬───────────────┘
                ↓
         ┌──────────────────────┐
         │  ResourceRepository  │
         │  (Database/Storage)  │
         └──────────────────────┘
```

---

## Implementation Tasks

### Task 1.1: Create Allocator Component

**File**: `internal/core/allocator/allocator.go`

**Interface:**

```go
package allocator

// Allocator orchestrates resource movement between pools and sandboxes
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

// AllocateFromPool allocates a resource from specified pool to sandbox
func (a *Allocator) AllocateFromPool(
    ctx context.Context,
    poolName string,
    sandboxID string,
) (*resource.Resource, error)

// ReleaseResources releases all resources from sandbox back to destruction
func (a *Allocator) ReleaseResources(
    ctx context.Context,
    sandboxID string,
) error

// GetResourcesForSandbox retrieves all resources and connection info
func (a *Allocator) GetResourcesForSandbox(
    ctx context.Context,
    sandboxID string,
) ([]*ResourceWithConnection, error)

// GetResourcesForPool retrieves all resources in pool (any state)
func (a *Allocator) GetResourcesForPool(
    ctx context.Context,
    poolName string,
) ([]*resource.Resource, error)
```

**Key Points:**

- ✅ **Internal only** - users never interact with Allocator directly
- ✅ **Single source of truth** - all resource queries go through Allocator
- ✅ **Clear ownership** - Pool owns unallocated, Sandbox owns allocated
- ✅ **Clean separation** - Pool and Sandbox don't know about each other

**Tests:**

```go
// internal/core/allocator/allocator_test.go
func TestAllocator_AllocateFromPool(t *testing.T)
func TestAllocator_ReleaseResources(t *testing.T)
func TestAllocator_ConcurrentAllocations(t *testing.T)
func TestAllocator_GetResourcesForSandbox(t *testing.T)
func TestAllocator_GetResourcesForPool(t *testing.T)
```

---

### Task 1.2: Refactor Pool

**File**: `internal/core/pool/manager.go`

**Changes:**

**REMOVE:**

- `Allocate()` method - moves to Allocator
- `Release()` method - moves to Allocator

**KEEP:**

- `Start()`, `Stop()` - Pool lifecycle
- `provisionOne()` - Resource creation
- `ensureMinReady()` - Pool replenishment
- Workers (replenishment, health checking)

**ADD:**

```go
// Query methods for Allocator
func (m *Manager) GetAvailableResources(ctx context.Context) ([]*resource.Resource, error) {
    // Return resources that are Ready and not allocated
}

func (m *Manager) GetAllResources(ctx context.Context) ([]*resource.Resource, error) {
    // Return all resources for this pool (any state)
}
```

**Pool now focuses on:**

- ✅ Provisioning resources (via provider)
- ✅ Running on_provision hooks
- ✅ Maintaining min_ready count
- ✅ Health checking
- ✅ Preheating (in 02-preheating-recycling)
- ✅ Recycling (in 02-preheating-recycling)

**Pool does NOT:**

- ❌ Allocate resources to sandboxes (Allocator does this)
- ❌ Track sandbox ownership (Allocator does this)
- ❌ Run on_allocate hooks (Allocator does this)

**Tests:**

```go
// internal/core/pool/manager_test.go
func TestPool_GetAvailableResources(t *testing.T)
func TestPool_GetAllResources(t *testing.T)
func TestPool_ProvisionOne_WithoutAllocator(t *testing.T)
```

---

### Task 1.3: Refactor Sandbox

**File**: `internal/core/sandbox/manager.go`

**Changes:**

**Constructor:**

```go
// OLD
func NewManager(
    pools map[string]PoolAllocator,
    sandboxRepo SandboxRepository,
    logger *logrus.Logger,
) *Manager

// NEW
func NewManager(
    allocator *allocator.Allocator,  // Takes Allocator instead of pools
    sandboxRepo SandboxRepository,
    logger *logrus.Logger,
) *Manager
```

**Update methods:**

```go
// allocateResourcesAsync now calls Allocator
func (m *Manager) allocateResourcesAsync(sandboxID string, resourceReqs []ResourceRequest) {
    for _, resReq := range resourceReqs {
        for i := 0; i < resReq.Count; i++ {
            // OLD: res, err := pool.Allocate(ctx, sandboxID)
            // NEW: res, err := m.allocator.AllocateFromPool(ctx, resReq.PoolName, sandboxID)
        }
    }
}

// Destroy calls Allocator
func (m *Manager) Destroy(ctx context.Context, id string) error {
    // OLD: pool.Release(ctx, res.ID)
    // NEW: m.allocator.ReleaseResources(ctx, id)
}

// GetResourcesForSandbox calls Allocator
func (m *Manager) GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*ResourceWithConnection, error) {
    // NEW: return m.allocator.GetResourcesForSandbox(ctx, sandboxID)
}
```

**Sandbox now focuses on:**

- ✅ Creating sandbox records
- ✅ Tracking sandbox lifecycle (Creating, Ready, Expiring, Destroyed)
- ✅ Auto-cleanup of expired sandboxes
- ✅ Coordinating multi-resource allocation

**Sandbox does NOT:**

- ❌ Know about Pool internals
- ❌ Track resources directly (queries Allocator)
- ❌ Call Provider directly

**Tests:**

```go
// internal/core/sandbox/manager_test.go
func TestSandbox_CreateWithAllocator(t *testing.T)
func TestSandbox_DestroyCallsAllocator(t *testing.T)
func TestSandbox_GetResourcesFromAllocator(t *testing.T)
```

---

### Task 1.4: Update Service Wiring

**File**: `cmd/boxy/commands/serve.go`

**Changes:**

```go
func (s *Service) Start() error {
    // 1. Load pool configs and create pool managers
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

---

## Resource Ownership Model

| Resource State | Owned By | Tracked By | SandboxID |
| ---------------- | ---------- | ------------ | ----------- |
| Provisioned | Pool | Allocator | nil |
| Ready | Pool | Allocator | nil |
| Allocated | Sandbox | Allocator | set |
| Destroyed | None | Repository | nil |

**Database Schema (no changes needed):**

```sql
CREATE TABLE resources (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL,
    sandbox_id TEXT,           -- NULL if not allocated
    state TEXT NOT NULL,       -- Provisioned, Ready, Allocated, etc.
    provider_id TEXT,
    metadata JSONB,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- Query available resources for pool:
SELECT * FROM resources
WHERE pool_id = ? AND state = 'Ready' AND sandbox_id IS NULL;

-- Query allocated resources for sandbox:
SELECT * FROM resources WHERE sandbox_id = ?;
```

**No migration needed** - existing schema supports this model!

---

## User Impact

### CLI/API Impact

**No changes to user-facing commands!**

```bash
# These commands remain exactly the same
boxy pool ls
boxy pool stats <pool-name>
boxy sandbox create -p pool:1 -d 1h
boxy sandbox destroy <id>
```

**Internally:**

- `boxy pool stats` → calls `pool.GetStats()` → queries Allocator for resource counts
- `boxy sandbox create` → calls `sandboxManager.Create()` → calls `allocator.AllocateFromPool()`

**User sees no difference** - cleaner architecture under the hood!

---

## Testing Strategy

### Unit Tests

- Test Allocator in isolation with mock pools and repository
- Test Pool without Allocator (queries only)
- Test Sandbox with mock Allocator
- **Coverage target**: > 90% for Allocator

### Integration Tests

```go
// tests/integration/allocator_test.go
func TestIntegration_FullAllocationFlow(t *testing.T) {
    // 1. Create pool with Docker provider
    // 2. Create allocator
    // 3. Allocate resource
    // 4. Verify state transitions
    // 5. Release resource
    // 6. Verify cleanup
}
```

### E2E Tests

```go
// tests/e2e/architecture_refactor_test.go
func TestE2E_SandboxWithNewArchitecture(t *testing.T) {
    // 1. Start Boxy service
    // 2. Create sandbox (triggers allocation)
    // 3. Verify resources allocated
    // 4. Destroy sandbox
    // 5. Verify cleanup
}
```

**Regression Tests:**

- All existing E2E tests must still pass
- No functionality lost relative to the previous baseline

---

## Migration Path

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

---

## Benefits

- ✅ **Clear separation of concerns**: Each component has distinct responsibility
- ✅ **Single source of truth**: Allocator tracks resource ownership
- ✅ **Pool is first-class**: Can be managed independently via CLI
- ✅ **Better testability**: Components tested in isolation
- ✅ **Extensibility**: Easy to add features (multi-pool allocation, advanced scheduling)
- ✅ **No API breakage**: User-facing commands unchanged
- ✅ **Future-proof**: Supports distributed agents (v2)

---

## Success Criteria

- ✅ Allocator component implemented with full test coverage
- ✅ Pool refactored to remove Allocate/Release methods
- ✅ Sandbox refactored to use Allocator
- ✅ All unit tests pass
- ✅ All integration tests pass
- ✅ All E2E tests pass (no regressions)
- ✅ Code review completed
- ✅ Documentation updated

---

## Related Documents

- [ADR-005: Pool/Sandbox Peer Architecture](../decisions/adr-005-pool-sandbox-peer-architecture.md)
- [02: Preheating & Recycling](02-preheating-recycling.md) - Builds on this architecture
- [04: Multi-Tenancy](04-multi-tenancy.md) - Requires Allocator
- [06: Pool CLI Commands](06-pool-cli-commands.md) - Enabled by this refactor

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None (foundation feature)
**Blocking**: 02, 04, 06, 07

# Boxy v1 Implementation Plan

> ⚠️ ARCHIVED: This document is retained for historical reference and is no longer actively maintained. See `docs/` for current plans and migration guidance.

**Version:** v1.0
**Date:** 2024-11-22
**Status:** Ready for Implementation
**Session:** To be implemented in NEW session (this document provides complete specification)

---

## Executive Summary

This document provides a comprehensive implementation plan for Boxy v1, incorporating critical architectural refactors, new features, and consistency updates based on architectural review.

**Key Changes:**

1. ✅ **Security fix applied** - Password generation now uses `crypto/rand` (already committed)
2. 🏗️ **Architecture refactor** - Pool and Sandbox as equal peers with internal Allocator
3. 🔥 **Preheating system** - Cold vs warm resources with configurable preheating
4. ♻️ **Recycling system** - Automatic resource refresh for all resources
5. 📝 **Terminology updates** - Clarified hook naming and resource states
6. 👥 **Multi-tenancy** - Users, teams, API tokens, ownership tracking
7. 🌐 **Distributed agents** - gRPC/mTLS agents for Hyper-V on Windows from Linux server
8. 📋 **Config schema** - Formal YAML schema definition
9. 🔍 **CLI/API schemas** - Complete interface documentation for review/regression testing
10. 🐛 **Debugging docs** - Comprehensive debugging and troubleshooting guide
11. 📂 **Config location fix** - Move from `~/.config/boxy/` to `./boxy.yaml` (current directory)
12. 🐳 **Docker/Compose support** - Run Boxy server in containers with examples
13. 📖 **Documentation** - Complete consistency review and updates

---

## Table of Contents

1. [Architectural Refactor](#1-architectural-refactor)
2. [Preheating & Recycling System](#2-preheating--recycling-system)
3. [Terminology Updates](#3-terminology-updates)
4. [Multi-Tenancy](#4-multi-tenancy)
5. [Base Image Validation](#5-base-image-validation)
6. [Pool as First-Class Component](#6-pool-as-first-class-component)
7. [Distributed Agent Architecture](#7-distributed-agent-architecture)
8. [Config File Schema](#8-config-file-schema)
9. [CLI/API Schema Documentation](#9-cliapi-schema-documentation)
10. [Debugging Documentation](#10-debugging-documentation)
11. [Config File Location Fix](#11-config-file-location-fix)
12. [Docker and Docker Compose Support](#12-docker-and-docker-compose-support)
13. [Documentation Updates](#13-documentation-updates)
14. [Testing Strategy](#14-testing-strategy)
15. [Migration Guide](#15-migration-guide)
16. [Implementation Checklist](#16-implementation-checklist)
17. [Success Criteria](#17-success-criteria)

---

## 1. Architectural Refactor

### Current Architecture (Problems)

```text
┌──────────────┐
│   Sandbox    │
│              │ ← Depends on Pool
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

### New Architecture (v1)

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

### Implementation Details

#### 1.1 New Component: Allocator

**Location:** `internal/core/allocator/allocator.go`

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
) (*resource.Resource, error) {
    // 1. Query pool for available resources
    // 2. Run on_allocate hooks
    // 3. Update resource: state = Allocated, sandboxID = sandboxID
    // 4. Return resource to caller
}

// ReleaseResources releases all resources from sandbox back to destruction
func (a *Allocator) ReleaseResources(
    ctx context.Context,
    sandboxID string,
) error {
    // 1. Get all resources for sandbox
    // 2. Destroy each (no reuse!)
    // 3. Update resource: state = Destroyed
    // 4. Pool replenishment happens automatically via pool workers
}

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

#### 1.2 Refactor Pool

**Changes to `internal/core/pool/manager.go`:**

```go
// REMOVE: Allocate() method - moves to Allocator
// REMOVE: Release() method - moves to Allocator

// KEEP: Pool management methods
func (m *Manager) Start() error                    // Start pool workers
func (m *Manager) Stop() error                     // Stop pool workers
func (m *Manager) GetStats(ctx context.Context) (*PoolStats, error)
func (m *Manager) ensureMinReady(ctx context.Context) error
func (m *Manager) provisionOne(ctx context.Context) error

// ADD: Query methods for Allocator
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
- ✅ Preheating (new!)
- ✅ Recycling (new!)

**Pool does NOT:**

- ❌ Allocate resources to sandboxes (Allocator does this)
- ❌ Track sandbox ownership (Allocator does this)
- ❌ Run on_allocate hooks (Allocator does this)

#### 1.3 Refactor Sandbox

**Changes to `internal/core/sandbox/manager.go`:**

```go
// CHANGE: Constructor takes Allocator instead of map of pools
func NewManager(
    allocator *allocator.Allocator,  // NEW: takes Allocator
    sandboxRepo SandboxRepository,
    logger *logrus.Logger,
) *Manager

// CHANGE: allocateResourcesAsync now calls Allocator
func (m *Manager) allocateResourcesAsync(sandboxID string, resourceReqs []ResourceRequest) {
    for _, resReq := range resourceReqs {
        for i := 0; i < resReq.Count; i++ {
            // OLD: res, err := pool.Allocate(ctx, sandboxID)
            // NEW: res, err := m.allocator.AllocateFromPool(ctx, resReq.PoolName, sandboxID)
        }
    }
}

// CHANGE: Destroy calls Allocator
func (m *Manager) Destroy(ctx context.Context, id string) error {
    // OLD: pool.Release(ctx, res.ID)
    // NEW: m.allocator.ReleaseResources(ctx, id)
}

// CHANGE: GetResourcesForSandbox calls Allocator
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

#### 1.4 Resource Ownership Model

**Ownership Rules:**

| Resource State | Owned By | Tracked By | SandboxID |
| ---------------- | ---------- | ------------ | ----------- |
| Provisioned | Pool | Pool | nil |
| Ready | Pool | Pool | nil |
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
SELECT * FROM resources WHERE pool_id = ? AND state = 'Ready' AND sandbox_id IS NULL;

-- Query allocated resources for sandbox:
SELECT * FROM resources WHERE sandbox_id = ?;
```

**No migration needed** - existing schema supports this model!

#### 1.5 CLI/API Impact

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

## 2. Preheating & Recycling System

### Concept: Cold vs Warm Resources

**Current model:** All resources in pool are "ready" (running)
**New model:** Resources can be cold (stopped) or warm (running/preheated)

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

### 2.1 New Resource States

**Update `internal/core/resource/resource.go`:**

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

### 2.2 Pool Configuration Schema

**Update pool config in `internal/core/pool/config.go`:**

```go
type PoolConfig struct {
    Name     string
    Type     resource.ResourceType
    Backend  string
    Image    ImageConfig

    // Resource counts
    MinReady int  // Total resources (cold + warm)
    MaxTotal int  // Hard cap

    // Preheating configuration (NEW!)
    Preheating PreheatingConfig

    // Existing fields...
    CPUs      int
    MemoryMB  int
    DiskGB    int
    Labels    map[string]string
    Environment map[string]string

    Hooks    HookConfig
    Timeouts TimeoutConfig
    HealthCheckInterval time.Duration
}

type PreheatingConfig struct {
    Enabled          bool          `yaml:"enabled"`
    Count            int           `yaml:"count"`              // How many to keep warm
    RecycleInterval  time.Duration `yaml:"recycle_interval"`   // How often to recycle
    RecycleStrategy  string        `yaml:"recycle_strategy"`   // "rolling" or "all-at-once"
    WarmupTimeout    time.Duration `yaml:"warmup_timeout"`     // Max time to warm a resource
}
```

**Example YAML:**

```yaml
pools:
  - name: win-test-vms
    type: vm
    backend: hyperv
    image:
      source: windows-11-base.vhdx

    min_ready: 10        # Total resources
    max_total: 20

    preheating:
      enabled: true
      count: 3           # Keep 3 running/warm
      recycle_interval: 1h
      recycle_strategy: rolling
      warmup_timeout: 5m

    cpus: 4
    memory_mb: 8192
```

### 2.3 Preheating Worker

**Add to `internal/core/pool/manager.go`:**

```go
// preheatingWorker maintains preheated resource count
func (m *Manager) preheatingWorker() {
    defer m.wg.Done()
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-m.ctx.Done():
            return
        case <-ticker.C:
            if err := m.ensurePreheated(m.ctx); err != nil {
                m.logger.WithError(err).Warn("Preheating check failed")
            }
        }
    }
}

func (m *Manager) ensurePreheated(ctx context.Context) error {
    if !m.config.Preheating.Enabled {
        return nil // Preheating disabled
    }

    m.mu.Lock()
    defer m.mu.Unlock()

    // Count warm resources (Ready state)
    ready, err := m.repository.GetByState(ctx, m.config.Name, resource.StateReady)
    if err != nil {
        return err
    }

    warmCount := len(ready)
    needed := m.config.Preheating.Count - warmCount

    if needed <= 0 {
        return nil // Already have enough warm resources
    }

    // Get cold resources (Provisioned state)
    provisioned, err := m.repository.GetByState(ctx, m.config.Name, resource.StateProvisioned)
    if err != nil {
        return err
    }

    // Warm up cold resources
    for i := 0; i < needed && i < len(provisioned); i++ {
        if err := m.warmResource(ctx, provisioned[i]); err != nil {
            m.logger.WithError(err).Error("Failed to warm resource")
            continue
        }
    }

    return nil
}

func (m *Manager) warmResource(ctx context.Context, res *resource.Resource) error {
    m.logger.WithField("resource_id", res.ID).Info("Warming resource")

    res.State = resource.StateWarming
    _ = m.repository.Update(ctx, res)

    // Start the resource via provider
    warmCtx, cancel := context.WithTimeout(ctx, m.config.Preheating.WarmupTimeout)
    defer cancel()

    // Provider-specific start operation
    update := provider.ResourceUpdate{
        PowerState: &provider.PowerStateRunning,
    }

    if err := m.provider.Update(warmCtx, res, update); err != nil {
        res.State = resource.StateError
        res.Metadata["warmup_error"] = err.Error()
        _ = m.repository.Update(ctx, res)
        return fmt.Errorf("failed to warm resource: %w", err)
    }

    // Mark as ready
    res.State = resource.StateReady
    res.UpdatedAt = time.Now()

    if err := m.repository.Update(ctx, res); err != nil {
        return err
    }

    m.logger.WithField("resource_id", res.ID).Info("Resource warmed and ready")
    return nil
}
```

### 2.4 Recycling Worker

**Recycling applies to ALL resources, not just preheated!**

```go
// recyclingWorker periodically recycles resources
func (m *Manager) recyclingWorker() {
    defer m.wg.Done()

    if !m.config.Preheating.Enabled || m.config.Preheating.RecycleInterval == 0 {
        return // Recycling disabled
    }

    ticker := time.NewTicker(m.config.Preheating.RecycleInterval)
    defer ticker.Stop()

    for {
        select {
        case <-m.ctx.Done():
            return
        case <-ticker.C:
            if err := m.recycleResources(m.ctx); err != nil {
                m.logger.WithError(err).Warn("Resource recycling failed")
            }
        }
    }
}

func (m *Manager) recycleResources(ctx context.Context) error {
    m.logger.Info("Starting resource recycling")

    // Get ALL unallocated resources (both cold and warm)
    all, err := m.repository.GetByPoolID(ctx, m.config.Name)
    if err != nil {
        return err
    }

    var unallocated []*resource.Resource
    for _, res := range all {
        if res.SandboxID == nil && (res.State == resource.StateProvisioned || res.State == resource.StateReady) {
            unallocated = append(unallocated, res)
        }
    }

    if len(unallocated) == 0 {
        return nil
    }

    strategy := m.config.Preheating.RecycleStrategy

    switch strategy {
    case "rolling":
        return m.recycleRolling(ctx, unallocated)
    case "all-at-once":
        return m.recycleAllAtOnce(ctx, unallocated)
    default:
        return m.recycleRolling(ctx, unallocated) // Default to rolling
    }
}

func (m *Manager) recycleRolling(ctx context.Context, resources []*resource.Resource) error {
    // Recycle one at a time to maintain availability
    for _, res := range resources {
        if err := m.recycleOne(ctx, res); err != nil {
            m.logger.WithError(err).WithField("resource_id", res.ID).Error("Failed to recycle resource")
            continue
        }

        // Wait a bit before recycling next
        time.Sleep(5 * time.Second)
    }
    return nil
}

func (m *Manager) recycleAllAtOnce(ctx context.Context, resources []*resource.Resource) error {
    // Recycle all simultaneously (brief unavailability)
    var wg sync.WaitGroup
    for _, res := range resources {
        wg.Add(1)
        go func(r *resource.Resource) {
            defer wg.Done()
            if err := m.recycleOne(ctx, r); err != nil {
                m.logger.WithError(err).WithField("resource_id", r.ID).Error("Failed to recycle resource")
            }
        }(res)
    }
    wg.Wait()
    return nil
}

func (m *Manager) recycleOne(ctx context.Context, res *resource.Resource) error {
    m.logger.WithField("resource_id", res.ID).Info("Recycling resource")

    res.State = resource.StateRecycling
    _ = m.repository.Update(ctx, res)

    wasWarm := res.State == resource.StateReady

    // Destroy the resource
    if err := m.provider.Destroy(ctx, res); err != nil {
        m.logger.WithError(err).Error("Failed to destroy resource during recycling")
        res.State = resource.StateError
        _ = m.repository.Update(ctx, res)
        return err
    }

    res.State = resource.StateDestroyed
    _ = m.repository.Update(ctx, res)

    // Provision replacement
    if err := m.provisionOne(ctx); err != nil {
        return fmt.Errorf("failed to provision replacement: %w", err)
    }

    // If it was warm, warm the replacement
    if wasWarm && m.config.Preheating.Enabled {
        // Preheating worker will pick it up on next cycle
    }

    m.logger.WithField("resource_id", res.ID).Info("Resource recycled")
    return nil
}
```

### 2.5 Updated provisionOne

**Modify `provisionOne` to create resources in Provisioned state (cold):**

```go
func (m *Manager) provisionOne(ctx context.Context) error {
    spec := m.config.ToResourceSpec()

    // Create resource in provisioning state
    res := resource.NewResource(m.config.Name, spec.Type, spec.ProviderType)
    res.State = resource.StateProvisioning  // Temporary state during creation

    if err := m.repository.Create(ctx, res); err != nil {
        return err
    }

    // Provision via provider
    provisioned, err := m.provider.Provision(provCtx, spec)
    if err != nil {
        res.State = resource.StateError
        _ = m.repository.Update(ctx, res)
        return err
    }

    res.ProviderID = provisioned.ProviderID
    res.Metadata = provisioned.Metadata

    // Run on_provision hooks (if any)
    if len(m.config.Hooks.OnProvision) > 0 {
        // ... execute hooks ...
    }

    // Mark as Provisioned (COLD) - not Ready!
    res.State = resource.StateProvisioned  // NEW: Cold state
    res.UpdatedAt = time.Now()

    if err := m.repository.Update(ctx, res); err != nil {
        _ = m.provider.Destroy(ctx, res)
        return err
    }

    m.logger.WithField("resource_id", res.ID).Info("Resource provisioned (cold)")

    // Preheating worker will warm it up if needed
    return nil
}
```

**Key change:** Resources start as `StateProvisioned` (cold), not `StateReady` (warm).

---

## 3. Terminology Updates

### 3.1 Hook Naming

**Decision:** Use Option A (two hooks)

**Old naming:**

- `after_provision` → Finalization
- `before_allocate` → Personalization

**New naming:**

- `on_provision` → Runs after provider creates resource (cold state)
- `on_allocate` → Runs when user requests resource

**Update `internal/hooks/hooks.go`:**

```go
type HookPoint string

const (
    HookPointOnProvision HookPoint = "on_provision"  // Was: after_provision
    HookPointOnAllocate  HookPoint = "on_allocate"   // Was: before_allocate
)
```

**Update config schema:**

```yaml
# OLD
hooks:
  after_provision:
    - type: script
      ...
  before_allocate:
    - type: script
      ...

# NEW
hooks:
  on_provision:
    - type: script
      ...
  on_allocate:
    - type: script
      ...
```

**Migration:** Add backwards compatibility for old names (log warning, map to new names).

### 3.2 Pool Terminology

**Old:** "Warm pools" vs "Cold pools"
**New:** "Preheated resources" vs "Cold resources"

**Rationale:**

- Pools don't have temperature, resources do
- "Preheated" is clearer than "warm" (matches oven metaphor)
- "Cold" means stopped/provisioned but not running

**Update documentation:** Replace "warm pools" with "pools with preheating enabled".

### 3.3 State Names (Already Updated Above)

See section 2.1 for new resource states.

---

## 4. Multi-Tenancy

### 4.1 User Model

**Create `internal/core/user/user.go`:**

```go
package user

import "time"

type User struct {
    ID        string
    Username  string
    Email     string
    APIToken  string    // For CLI/API authentication
    Role      UserRole
    CreatedAt time.Time
    UpdatedAt time.Time
}

type UserRole string

const (
    RoleAdmin UserRole = "admin"  // Can see all sandboxes, manage users
    RoleUser  UserRole = "user"   // Can only see own sandboxes
)

// UserRepository defines persistence interface
type UserRepository interface {
    CreateUser(ctx context.Context, user *User) error
    GetUserByID(ctx context.Context, id string) (*User, error)
    GetUserByUsername(ctx context.Context, username string) (*User, error)
    GetUserByToken(ctx context.Context, token string) (*User, error)
    UpdateUser(ctx context.Context, user *User) error
    DeleteUser(ctx context.Context, id string) error
    ListUsers(ctx context.Context) ([]*User, error)
}
```

**Database schema:**

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT,
    api_token TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_users_token ON users(api_token);
CREATE INDEX idx_users_username ON users(username);
```

### 4.2 Team Model (Optional for v1)

**Create `internal/core/team/team.go`:**

```go
package team

type Team struct {
    ID      string
    Name    string
    Members []string  // User IDs

    // Quotas
    MaxConcurrentSandboxes int
    MaxTotalResources      int

    CreatedAt time.Time
    UpdatedAt time.Time
}

type TeamRepository interface {
    CreateTeam(ctx context.Context, team *Team) error
    GetTeamByID(ctx context.Context, id string) (*Team, error)
    UpdateTeam(ctx context.Context, team *Team) error
    DeleteTeam(ctx context.Context, id string) error
    ListTeams(ctx context.Context) ([]*Team, error)
}
```

**Database schema:**

```sql
CREATE TABLE teams (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    members JSONB,  -- Array of user IDs
    max_concurrent_sandboxes INTEGER,
    max_total_resources INTEGER,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

### 4.3 Update Sandbox Model

**Add user/team ownership to sandbox:**

```go
type Sandbox struct {
    ID          string
    Name        string
    UserID      string    // NEW: Who created it
    TeamID      string    // NEW: Which team (optional)
    ResourceIDs []string
    State       SandboxState
    CreatedAt   time.Time
    ExpiresAt   *time.Time
    UpdatedAt   time.Time
    Metadata    map[string]string
}
```

**Database migration:**

```sql
ALTER TABLE sandboxes ADD COLUMN user_id TEXT;
ALTER TABLE sandboxes ADD COLUMN team_id TEXT;

CREATE INDEX idx_sandboxes_user_id ON sandboxes(user_id);
CREATE INDEX idx_sandboxes_team_id ON sandboxes(team_id);
```

### 4.4 API Token Generation

**Add CLI command:**

```bash
# Admin creates API token for user
boxy admin create-token --username alice --expires 90d

# Output:
API Token: bxy_abc123xyz789
Keep this token secure! It will not be shown again.
```

**Implementation:**

```go
func generateAPIToken() string {
    // Use crypto/rand for secure token
    bytes := make([]byte, 32)
    if _, err := cryptoRand.Read(bytes); err != nil {
        panic(err)
    }
    return "bxy_" + base64.URLEncoding.EncodeToString(bytes)[:40]
}
```

### 4.5 Authentication Middleware

**HTTP API authentication:**

```go
func AuthMiddleware(userRepo user.UserRepository) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        if token == "" {
            c.JSON(401, gin.H{"error": "missing API token"})
            c.Abort()
            return
        }

        // Remove "Bearer " prefix if present
        token = strings.TrimPrefix(token, "Bearer ")

        user, err := userRepo.GetUserByToken(c.Request.Context(), token)
        if err != nil {
            c.JSON(401, gin.H{"error": "invalid API token"})
            c.Abort()
            return
        }

        // Store user in context
        c.Set("user", user)
        c.Next()
    }
}
```

**CLI authentication:**

```yaml
# ~/.config/boxy/config.yaml
server: https://boxy-server.internal:8443
token: bxy_abc123xyz789
```

### 4.6 Ownership Filtering

**Sandbox list filtered by user:**

```go
func (m *Manager) List(ctx context.Context, userID string, role UserRole) ([]*Sandbox, error) {
    if role == RoleAdmin {
        // Admins see all sandboxes
        return m.sandboxRepo.ListActiveSandboxes(ctx)
    }

    // Regular users only see their own
    return m.sandboxRepo.ListSandboxesByUser(ctx, userID)
}
```

### 4.7 Basic Quotas

**Check quota before creating sandbox:**

```go
func (m *Manager) Create(ctx context.Context, req *CreateRequest, userID string) (*Sandbox, error) {
    // Check user's current sandbox count
    userSandboxes, err := m.sandboxRepo.CountActiveSandboxesByUser(ctx, userID)
    if err != nil {
        return nil, err
    }

    // Get user's quota (hardcoded for v1, configurable in v2)
    maxSandboxes := 10  // TODO: Make configurable per user/team

    if userSandboxes >= maxSandboxes {
        return nil, fmt.Errorf("quota exceeded: max %d concurrent sandboxes", maxSandboxes)
    }

    // Proceed with creation...
}
```

---

## 5. Base Image Validation

### 5.1 Minimal Contract

**What Boxy requires from a base image:**

1. **Ability to execute commands** - SSH, WinRM, or docker exec
2. **Network connectivity** - Reachable from Boxy server
3. **Provider lifecycle support** - Start, stop, destroy

**That's it!** Everything else is user's responsibility.

### 5.2 Validation Configuration

**Add to pool config:**

```yaml
pools:
  - name: win-test-vms
    image:
      source: windows-11-base.vhdx

    validation:
      required:
        - name: "PowerShell with Admin Rights"
          check: |
            $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
            if (-not $isAdmin) { exit 1 }
            echo "Admin rights confirmed"

        - name: "WinRM Enabled"
          check: "Test-WSMan -ErrorAction SilentlyContinue"

      optional:
        - name: "Guest Additions"
          check: "Get-Service vmtools -ErrorAction SilentlyContinue"
```

### 5.3 Validation Tool

**Add CLI command:**

```bash
boxy admin validate-image \
  --pool win-test-vms \
  --config boxy.yaml

# Output:
✓ PowerShell with Admin Rights - OK
✓ WinRM Enabled - OK
⚠ Guest Additions - Not found (optional)

Image is valid for use with Boxy!
```

**Implementation:**

```go
func ValidateImage(ctx context.Context, poolConfig *PoolConfig, provider provider.Provider) error {
    // 1. Provision temporary resource from image
    // 2. Run required checks
    // 3. Run optional checks (warn if fail)
    // 4. Destroy temporary resource
    // 5. Report results
}
```

---

## 6. Pool as First-Class Component

### 6.1 New Pool CLI Commands

**Add these commands to `cmd/boxy/commands/pool.go`:**

```bash
# Lifecycle
boxy pool create --config pool.yaml
boxy pool start <pool-name>
boxy pool stop <pool-name>
boxy pool delete <pool-name>

# Management
boxy pool scale <pool-name> --min-ready 5 --preheated 2
boxy pool drain <pool-name>           # Stop accepting allocations
boxy pool refill <pool-name>          # Resume allocations

# Inspection
boxy pool ls                          # List all pools
boxy pool stats <pool-name>           # Current stats
boxy pool inspect <pool-name>         # Detailed info
boxy pool resources <pool-name>       # List resources in pool

# Maintenance
boxy pool recycle <pool-name>         # Force recycle now
boxy pool validate <pool-name>        # Validate base image
```

### 6.2 Implementation

**Pool lifecycle in service:**

```go
// cmd/boxy/commands/serve.go

func (s *Service) Start() error {
    // Load pool configs from file
    for _, poolCfg := range s.config.Pools {
        pool, err := pool.NewManager(poolCfg, provider, repo, logger)
        if err != nil {
            return err
        }

        // Start pool (workers begin)
        if err := pool.Start(); err != nil {
            return err
        }

        s.pools[poolCfg.Name] = pool
    }

    // Create allocator with all pools
    s.allocator = allocator.NewAllocator(repo, s.pools, logger)

    // Start sandbox manager
    s.sandboxManager = sandbox.NewManager(s.allocator, sandboxRepo, logger)
    s.sandboxManager.Start()

    return nil
}
```

**Pool config file remains primary source:**

```yaml
# ~/.config/boxy/boxy.yaml
pools:
  - name: win-test-vms
    # ... config ...
```

**CLI commands modify running pools, don't persist** (unless --persist flag):

```bash
# Scale pool temporarily (until restart)
boxy pool scale win-test-vms --min-ready 10

# Scale pool permanently (updates config file)
boxy pool scale win-test-vms --min-ready 10 --persist
```

---

## 7. Distributed Agent Architecture

**CRITICAL FOR v1**: Hyper-V is the PRIMARY backend (not Docker), and Hyper-V cannot run on Linux. Therefore, distributed agent architecture is ESSENTIAL for MVP.

### 7.1 Why This is v1 (Not v2)

**User Correction**: "Distributed architecture is v1!!! We need agents now!!"

**Rationale:**

- Hyper-V is PRIMARY backend for production use
- Hyper-V only runs on Windows hosts
- Boxy server typically runs on Linux for flexibility
- Without agents, Hyper-V cannot be used → MVP is blocked

**Therefore**: Distributed agents are NOT optional, they are REQUIRED for v1.

### 7.2 Architecture Overview

See [ADR-004: Distributed Agent Architecture](decisions/adr-004-distributed-agent-architecture.md) for complete specification.

```text
┌─────────────────────────────────────────┐
│     Boxy Server (Linux)                  │
│  ┌────────────────────────────────────┐ │
│  │  Provider Registry                 │ │
│  │  ├─ Docker (embedded, local)       │ │
│  │  └─ Hyper-V (remote via agent)     │ │
│  └────────────────┬───────────────────┘ │
└────────────────────┼─────────────────────┘
                     │ gRPC + mTLS
                     ↓
    ┌────────────────────────────────────┐
    │  Boxy Agent (Windows Host)         │
    │  ├─ Hyper-V Provider (embedded)    │
    │  └─ gRPC Server                    │
    └────────────────────────────────────┘
```

**Key Points:**

- **Single binary**: `boxy` runs as server, agent, or both
- **Transparent proxying**: RemoteProvider implements same Provider interface
- **gRPC**: Efficient, type-safe RPC with Protocol Buffers
- **mTLS**: Mutual authentication with client certificates
- **Backwards compatible**: Local providers work unchanged

### 7.3 Component Design

#### 7.3.1 Binary Modes

```bash
# Run as server (orchestrator)
boxy serve

# Run as agent (provider host)
boxy agent serve \
  --server-url https://boxy-server:8443 \
  --cert /path/to/agent-cert.pem \
  --key /path/to/agent-key.pem \
  --ca /path/to/ca-cert.pem \
  --providers hyperv

# Run as both (server + local agent)
boxy serve --agent-mode
```

#### 7.3.2 Provider Interface (Unchanged)

```go
// pkg/provider/provider.go
type Provider interface {
    Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error)
    Destroy(ctx context.Context, res *resource.Resource) error
    GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error)
    Update(ctx context.Context, res *resource.Resource, update ResourceUpdate) error
    Exec(ctx context.Context, res *resource.Resource, command []string) (*ExecResult, error)
    HealthCheck(ctx context.Context) error
    Name() string
    Type() resource.ResourceType
}
```

**Critical**: Remote providers implement the EXACT same interface → transparent to Pool, Sandbox, Allocator.

#### 7.3.3 Remote Provider Implementation

**Location**: `pkg/provider/remote/remote.go`

```go
type RemoteProvider struct {
    name         string
    resourceType resource.ResourceType
    agentID      string
    client       providerproto.ProviderServiceClient
    conn         *grpc.ClientConn
}

func (r *RemoteProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Translate to gRPC request
    req := &providerproto.ProvisionRequest{
        Spec: specToProto(spec),
    }

    // Call agent via gRPC
    resp, err := r.client.Provision(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("remote provision failed: %w", err)
    }

    // Translate response back
    return protoToResource(resp.Resource), nil
}

// Other methods follow same pattern...
```

#### 7.3.4 Agent Server Implementation

**Location**: `internal/agent/server.go`

```go
type Server struct {
    registry   *provider.Registry  // Local providers
    tlsConfig  *tls.Config
    grpcServer *grpc.Server
}

func (s *Server) Provision(ctx context.Context, req *providerproto.ProvisionRequest) (*providerproto.ProvisionResponse, error) {
    // Get local provider
    prov, ok := s.registry.Get(req.ProviderName)
    if !ok {
        return nil, status.Errorf(codes.NotFound, "provider not found: %s", req.ProviderName)
    }

    // Call local provider
    spec := protoToSpec(req.Spec)
    res, err := prov.Provision(ctx, spec)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "provision failed: %v", err)
    }

    return &providerproto.ProvisionResponse{
        Resource: resourceToProto(res),
    }, nil
}
```

#### 7.3.5 Configuration Schema

```yaml
# ~/.config/boxy/boxy.yaml

# Server configuration
server:
  mode: server
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

# Agent configuration
agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    tls:
      cert_file: /etc/boxy/agents/windows-01-cert.pem
      key_file: /etc/boxy/agents/windows-01-key.pem
      ca_file: /etc/boxy/ca-cert.pem

# Pool configuration
pools:
  - name: win-server-2022
    type: vm
    backend: hyperv
    backend_agent: windows-host-01  # NEW: Routes to remote agent
    image: win-server-2022-template
    min_ready: 3
    max_total: 10

  - name: ubuntu-containers
    type: container
    backend: docker
    # backend_agent: (not specified = local embedded provider)
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20
```

### 7.4 Security Model

#### Certificate Management

```bash
# Server generates CA and issues certificates
boxy admin init-ca \
  --output /etc/boxy/ca

# Generate agent certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --agent-id windows-host-01 \
  --output /etc/boxy/agents/windows-01

# Outputs:
#   windows-01-cert.pem
#   windows-01-key.pem
```

#### mTLS Authentication Flow

1. Agent starts with client certificate
2. Agent connects to server with mTLS
3. Server verifies agent certificate against CA
4. Server extracts agent ID from cert CN (Common Name)
5. Server validates agent is authorized for requested providers
6. Bidirectional authentication established

### 7.5 Protocol Buffers

**Location**: `pkg/provider/proto/provider.proto`

```protobuf
syntax = "proto3";

package provider;

service ProviderService {
  rpc Provision(ProvisionRequest) returns (ProvisionResponse);
  rpc Destroy(DestroyRequest) returns (DestroyResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc GetConnectionInfo(GetConnectionInfoRequest) returns (GetConnectionInfoResponse);
  rpc Update(UpdateRequest) returns (UpdateResponse);
  rpc Exec(ExecRequest) returns (ExecResponse);
  rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse);
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}

message ProvisionRequest {
  string provider_name = 1;
  ResourceSpec spec = 2;
}

message ProvisionResponse {
  Resource resource = 1;
}

// ... other messages
```

### 7.6 Implementation Tasks for v1

**Phase 1: Foundation**

- [ ] Define Protocol Buffers schema (provider.proto)
- [ ] Generate gRPC code (make protobuf)
- [ ] Create RemoteProvider implementation
- [ ] Create Agent Server implementation
- [ ] Unit tests for serialization/deserialization

**Phase 2: Security**

- [ ] Implement CA initialization (`boxy admin init-ca`)
- [ ] Implement certificate issuance (`boxy admin issue-cert`)
- [ ] mTLS configuration for server
- [ ] mTLS configuration for agent
- [ ] Agent registration and authorization
- [ ] Integration tests for mTLS

**Phase 3: Agent Mode**

- [ ] Add `boxy agent serve` command
- [ ] Agent registration on startup
- [ ] Heartbeat mechanism
- [ ] Agent health monitoring
- [ ] Graceful shutdown
- [ ] E2E tests with real agents

**Phase 4: Server Integration**

- [ ] Update configuration schema (backend_agent field)
- [ ] Agent registry in server
- [ ] Remote provider factory
- [ ] Provider routing logic
- [ ] Agent failover handling (basic)
- [ ] Integration tests

**Phase 5: Testing**

- [ ] Unit tests for RemoteProvider
- [ ] Unit tests for Agent Server
- [ ] Integration tests with Docker (testable on Linux)
- [ ] E2E tests with stub Hyper-V provider
- [ ] Real Hyper-V testing (manual on Windows host)

### 7.7 Testing Strategy

**Challenge**: Hyper-V only runs on Windows, but CI runs on Linux.

**Solution**:

```go
// Stub Hyper-V provider for testing on Linux
type StubHyperVProvider struct {
    vms map[string]*stubVM
}

func (s *StubHyperVProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Simulate realistic behavior
    time.Sleep(10 * time.Second) // Simulate provision time
    vm := &stubVM{
        ID:   uuid.New().String(),
        Name: fmt.Sprintf("stub-vm-%s", uuid.New().String()[:8]),
    }
    s.vms[vm.ID] = vm
    return &resource.Resource{
        ID:         uuid.New().String(),
        ProviderID: vm.ID,
        State:      resource.StateProvisioned,
    }, nil
}
```

**Testing Layers**:

1. **Unit tests**: Test RemoteProvider, Agent Server with mocks (Linux OK)
2. **Integration tests**: Test with Docker provider via agent (Linux OK)
3. **E2E tests**: Test with stubbed Hyper-V provider (Linux OK)
4. **Manual tests**: Test with real Hyper-V on Windows host (Windows required)

---

## 8. Config File Schema

**NEW for v1**: Formalize YAML configuration schema for validation, documentation, and IDE support.

### 8.1 Why We Need This

**Problems without formal schema:**

- Users don't know what fields are valid
- Typos in config cause runtime errors
- No IDE autocomplete/validation
- Hard to document all options
- Difficult to version config format

**Solution**: Define formal YAML schema using JSON Schema.

### 8.2 Schema Definition

**Location**: `docs/config-schema.json`

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Boxy Configuration",
  "description": "Complete configuration schema for Boxy v1",
  "type": "object",
  "required": ["storage", "logging", "pools"],
  "properties": {
    "storage": {
      "type": "object",
      "required": ["type"],
      "properties": {
        "type": {
          "type": "string",
          "enum": ["sqlite", "postgres"],
          "description": "Database backend type"
        },
        "path": {
          "type": "string",
          "description": "Path to SQLite database file (required for sqlite)"
        },
        "dsn": {
          "type": "string",
          "description": "Connection string for PostgreSQL (required for postgres)"
        }
      }
    },
    "logging": {
      "type": "object",
      "properties": {
        "level": {
          "type": "string",
          "enum": ["debug", "info", "warn", "error"],
          "default": "info"
        },
        "format": {
          "type": "string",
          "enum": ["text", "json"],
          "default": "text"
        }
      }
    },
    "server": {
      "type": "object",
      "description": "Server configuration for distributed mode",
      "properties": {
        "mode": {
          "type": "string",
          "enum": ["server", "agent", "standalone"],
          "default": "standalone"
        },
        "listen_address": {
          "type": "string",
          "default": "0.0.0.0:8443"
        },
        "tls": {
          "type": "object",
          "required": ["cert_file", "key_file", "ca_file"],
          "properties": {
            "cert_file": {"type": "string"},
            "key_file": {"type": "string"},
            "ca_file": {"type": "string"},
            "client_auth": {
              "type": "string",
              "enum": ["none", "request", "require"],
              "default": "require"
            }
          }
        }
      }
    },
    "agents": {
      "type": "array",
      "description": "Remote agent configurations",
      "items": {
        "type": "object",
        "required": ["id", "address", "providers"],
        "properties": {
          "id": {"type": "string"},
          "address": {"type": "string"},
          "providers": {
            "type": "array",
            "items": {
              "type": "string",
              "enum": ["docker", "hyperv", "kvm", "vmware"]
            }
          },
          "tls": {
            "type": "object",
            "required": ["cert_file", "key_file", "ca_file"],
            "properties": {
              "cert_file": {"type": "string"},
              "key_file": {"type": "string"},
              "ca_file": {"type": "string"}
            }
          }
        }
      }
    },
    "pools": {
      "type": "array",
      "description": "Resource pool configurations",
      "items": {
        "type": "object",
        "required": ["name", "type", "backend", "min_ready", "max_total"],
        "properties": {
          "name": {"type": "string"},
          "type": {
            "type": "string",
            "enum": ["vm", "container", "process"]
          },
          "backend": {
            "type": "string",
            "enum": ["docker", "hyperv", "kvm", "vmware", "podman"]
          },
          "backend_agent": {
            "type": "string",
            "description": "Agent ID for remote provider (optional, uses local if omitted)"
          },
          "image": {
            "oneOf": [
              {"type": "string"},
              {
                "type": "object",
                "required": ["source"],
                "properties": {
                  "source": {"type": "string"},
                  "differencing_disk": {"type": "boolean", "default": true}
                }
              }
            ]
          },
          "min_ready": {
            "type": "integer",
            "minimum": 1,
            "description": "Minimum total resources (cold + warm)"
          },
          "max_total": {
            "type": "integer",
            "minimum": 1,
            "description": "Maximum total resources"
          },
          "preheating": {
            "type": "object",
            "properties": {
              "enabled": {"type": "boolean", "default": false},
              "count": {
                "type": "integer",
                "minimum": 0,
                "description": "How many resources to keep warm"
              },
              "recycle_interval": {
                "type": "string",
                "pattern": "^[0-9]+(s|m|h)$",
                "description": "How often to recycle resources (e.g. 1h, 30m)"
              },
              "recycle_strategy": {
                "type": "string",
                "enum": ["rolling", "all-at-once"],
                "default": "rolling"
              },
              "warmup_timeout": {
                "type": "string",
                "pattern": "^[0-9]+(s|m|h)$",
                "default": "5m"
              }
            }
          },
          "cpus": {"type": "integer", "minimum": 1},
          "memory_mb": {"type": "integer", "minimum": 128},
          "disk_gb": {"type": "integer", "minimum": 1},
          "labels": {
            "type": "object",
            "additionalProperties": {"type": "string"}
          },
          "environment": {
            "type": "object",
            "additionalProperties": {"type": "string"}
          },
          "hooks": {
            "type": "object",
            "properties": {
              "on_provision": {
                "type": "array",
                "items": {"$ref": "#/definitions/hook"}
              },
              "on_allocate": {
                "type": "array",
                "items": {"$ref": "#/definitions/hook"}
              }
            }
          },
          "health_check_interval": {
            "type": "string",
            "pattern": "^[0-9]+(s|m|h)$",
            "default": "30s"
          }
        }
      }
    }
  },
  "definitions": {
    "hook": {
      "type": "object",
      "required": ["type"],
      "properties": {
        "type": {
          "type": "string",
          "enum": ["script", "http"]
        },
        "shell": {
          "type": "string",
          "enum": ["bash", "powershell", "cmd"],
          "description": "Shell to use for script hooks"
        },
        "inline": {
          "type": "string",
          "description": "Inline script content"
        },
        "script_path": {
          "type": "string",
          "description": "Path to external script file"
        },
        "timeout": {
          "type": "string",
          "pattern": "^[0-9]+(s|m|h)$",
          "default": "5m"
        },
        "env": {
          "type": "object",
          "additionalProperties": {"type": "string"},
          "description": "Environment variables for hook execution"
        }
      }
    }
  }
}
```

### 8.3 Schema Validation Tool

**Add CLI command:**

```bash
# Validate config file against schema
boxy admin validate-config --config boxy.yaml

# Output:
✓ Configuration is valid
✓ 3 pools defined
✓ 1 agent configured
⚠ Warning: Pool 'win-server-2022' preheating count (10) > min_ready (5)
```

**Implementation:**

```go
// internal/config/validator.go
func ValidateConfig(configPath, schemaPath string) error {
    // Load schema
    schemaData, err := os.ReadFile(schemaPath)
    if err != nil {
        return fmt.Errorf("failed to load schema: %w", err)
    }

    // Load config
    configData, err := os.ReadFile(configPath)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    // Validate using JSON Schema library
    // (use github.com/xeipuuv/gojsonschema or similar)
    result, err := validateJSONSchema(schemaData, configData)
    if err != nil {
        return err
    }

    if !result.Valid() {
        for _, err := range result.Errors() {
            fmt.Printf("- %s\n", err)
        }
        return fmt.Errorf("config validation failed")
    }

    return nil
}
```

### 8.4 Example Annotated Config

**Location**: `boxy.example.yaml`

```yaml
# Boxy v1 Configuration Example
# Full documentation: https://github.com/Geogboe/boxy/docs/CONFIG_REFERENCE.md

# Storage configuration (REQUIRED)
storage:
  type: sqlite                             # sqlite or postgres
  path: ~/.config/boxy/boxy.db             # Path for SQLite
  # dsn: "postgres://user:pass@host/db"    # PostgreSQL connection string

# Logging configuration
logging:
  level: info                              # debug, info, warn, error
  format: text                             # text or json

# Server configuration (for distributed mode)
server:
  mode: server                             # server, agent, or standalone
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require                   # none, request, or require

# Agent configurations (for remote providers)
agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    tls:
      cert_file: /etc/boxy/agents/windows-01-cert.pem
      key_file: /etc/boxy/agents/windows-01-key.pem
      ca_file: /etc/boxy/ca-cert.pem

# Pool configurations (REQUIRED)
pools:
  # Example: Windows VMs via Hyper-V (remote agent)
  - name: win-server-2022
    type: vm
    backend: hyperv
    backend_agent: windows-host-01         # Routes to remote agent

    image:
      source: win-server-2022-template.vhdx
      differencing_disk: true              # Use differencing disks for fast provisioning

    min_ready: 10                          # Total resources (cold + warm)
    max_total: 20                          # Hard cap

    # Preheating configuration
    preheating:
      enabled: true
      count: 3                             # Keep 3 VMs running/warm
      recycle_interval: 1h                 # Recycle every hour
      recycle_strategy: rolling            # rolling or all-at-once
      warmup_timeout: 5m

    # Resource specifications
    cpus: 4
    memory_mb: 8192
    disk_gb: 80

    # Labels (for filtering/organization)
    labels:
      environment: production
      team: infrastructure

    # Lifecycle hooks
    hooks:
      on_provision:
        - type: script
          shell: powershell
          inline: |
            # Validate VM is accessible
            Test-Connection localhost -Count 1
            # Take snapshot for quick restore
            Checkpoint-VM -Name $env:COMPUTERNAME -SnapshotName "Clean"
          timeout: 10m

      on_allocate:
        - type: script
          shell: powershell
          inline: |
            # Create user account with generated password
            New-LocalUser -Name "${username}" -Password (ConvertTo-SecureString "${password}" -AsPlainText -Force)
            Add-LocalGroupMember -Group "Administrators" -Member "${username}"
            # Enable RDP
            Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name "fDenyTSConnections" -Value 0
          timeout: 2m

    health_check_interval: 30s

  # Example: Docker containers (local provider)
  - name: ubuntu-containers
    type: container
    backend: docker
    # backend_agent not specified = local provider

    image: ubuntu:22.04

    min_ready: 5
    max_total: 20

    preheating:
      enabled: true
      count: 5                             # All preheated (containers start fast)
      recycle_interval: 30m

    cpus: 2
    memory_mb: 512

    environment:
      DEBIAN_FRONTEND: noninteractive

    hooks:
      on_provision:
        - type: script
          shell: bash
          inline: |
            apt-get update
            apt-get install -y curl git
          timeout: 5m

    health_check_interval: 30s
```

### 8.5 Config Reference Documentation

**Create**: `docs/CONFIG_REFERENCE.md`

- Complete field reference
- Examples for each section
- Best practices
- Common patterns
- Troubleshooting

### 8.6 IDE Integration

**VSCode**: Users can reference schema in config file:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Geogboe/boxy/main/docs/config-schema.json

storage:
  type: sqlite  # <-- IDE provides autocomplete and validation
```

**Benefits**:

- Autocomplete for all fields
- Inline documentation
- Real-time validation
- Error highlighting

---

## 9. CLI/API Schema Documentation

**Purpose**: Document all CLI commands and API endpoints for:

- User reference
- Regression testing
- Interface approval before implementation

### 9.1 CLI Command Reference

**Location**: `docs/CLI_REFERENCE.md`

#### Complete CLI Tree

```text
boxy
├── init                      # Initialize configuration
├── serve                     # Start Boxy service
├── agent                     # Agent commands
│   └── serve                 # Start agent mode
├── pool                      # Pool management
│   ├── ls                    # List pools
│   ├── stats <pool>          # Pool statistics
│   ├── inspect <pool>        # Detailed inspection
│   ├── resources <pool>      # List resources in pool
│   ├── create --config file  # Create pool from config
│   ├── start <pool>          # Start pool
│   ├── stop <pool>           # Stop pool
│   ├── delete <pool>         # Delete pool
│   ├── scale <pool>          # Scale pool (--min-ready, --preheated, --persist)
│   ├── drain <pool>          # Stop accepting allocations
│   ├── refill <pool>         # Resume allocations
│   ├── recycle <pool>        # Force recycle now
│   └── validate <pool>       # Validate base image
├── sandbox                   # Sandbox management
│   ├── create                # Create sandbox (-p, -d, -n, --json)
│   ├── ls                    # List sandboxes
│   ├── inspect <id>          # Inspect sandbox
│   ├── extend <id>           # Extend duration
│   └── destroy <id>          # Destroy sandbox
└── admin                     # Administrative commands
    ├── init-ca               # Initialize certificate authority
    ├── issue-cert            # Issue agent certificate
    ├── create-token          # Create API token for user
    ├── validate-config       # Validate config file
    ├── validate-image        # Validate base image
    └── migrate-v1            # Run database migrations
```

#### Detailed Command Schemas

**Pool Commands**:

```bash
# boxy pool ls
# Lists all configured pools
# Flags:
#   --format [table|json]     # Output format (default: table)
#   --filter <expression>     # Filter pools by expression
# Output:
#   Table with columns: NAME, TYPE, BACKEND, IMAGE, READY, ALLOCATED, MIN, MAX, HEALTHY
# Exit Codes:
#   0 - Success
#   1 - Error

# boxy pool stats <pool-name>
# Show detailed statistics for a pool
# Arguments:
#   pool-name (required)      # Name of the pool
# Flags:
#   --format [table|json]     # Output format (default: table)
#   --watch                   # Continuously update stats
# Output:
#   Detailed pool statistics including:
#   - Total resources (cold, warm, allocated)
#   - Allocation rate
#   - Health status
#   - Last replenishment time
#   - Recycling status
# Exit Codes:
#   0 - Success
#   1 - Pool not found
#   2 - Other error

# boxy pool scale <pool-name>
# Scale pool resources
# Arguments:
#   pool-name (required)      # Name of the pool
# Flags:
#   --min-ready <int>         # New min_ready count
#   --preheated <int>         # New preheated count
#   --persist                 # Update config file (default: false, runtime only)
# Output:
#   Confirmation message with new settings
# Exit Codes:
#   0 - Success
#   1 - Invalid arguments
#   2 - Pool not found
```

**Sandbox Commands**:

```bash
# boxy sandbox create
# Create a new sandbox
# Flags:
#   -p, --pool <pool>:<count> # Pool and resource count (repeatable)
#   -d, --duration <duration> # Expiration duration (default: 2h)
#   -n, --name <name>         # Optional sandbox name
#   --json                    # Output JSON format
# Examples:
#   boxy sandbox create -p ubuntu-containers:2 -d 1h
#   boxy sandbox create -p win-vms:1 -p ubuntu:2 -d 4h -n my-lab
# Output (table):
#   SANDBOX_ID: sb-abc123
#   NAME: my-lab
#   RESOURCES: 2
#   EXPIRES_AT: 2024-11-22 15:30:00
#   STATUS: creating
# Output (json):
#   {
#     "id": "sb-abc123",
#     "name": "my-lab",
#     "resource_ids": ["res-1", "res-2"],
#     "state": "creating",
#     "created_at": "2024-11-22T14:30:00Z",
#     "expires_at": "2024-11-22T18:30:00Z"
#   }
# Exit Codes:
#   0 - Success
#   1 - Invalid arguments
#   2 - Pool not found
#   3 - No resources available
#   4 - Quota exceeded

# boxy sandbox ls
# List active sandboxes
# Flags:
#   --format [table|json]     # Output format (default: table)
#   --filter <expression>     # Filter by expression
#   --all                     # Include expired sandboxes
# Output:
#   Table with columns: ID, NAME, RESOURCES, STATE, CREATED_AT, EXPIRES_AT
# Exit Codes:
#   0 - Success
#   1 - Error

# boxy sandbox destroy <sandbox-id>
# Destroy a sandbox
# Arguments:
#   sandbox-id (required)     # ID of the sandbox
# Flags:
#   --force                   # Skip confirmation
# Output:
#   Confirmation message
# Exit Codes:
#   0 - Success
#   1 - Sandbox not found
#   2 - Destroy failed
```

**Admin Commands**:

```bash
# boxy admin init-ca
# Initialize certificate authority
# Flags:
#   --output <dir>            # Output directory (default: /etc/boxy/ca)
#   --validity <duration>     # CA validity period (default: 10y)
# Output:
#   CA certificate and key files
# Exit Codes:
#   0 - Success
#   1 - Output directory exists
#   2 - Generation failed

# boxy admin issue-cert
# Issue certificate for agent
# Flags:
#   --ca-cert <path>          # CA certificate path
#   --ca-key <path>           # CA key path
#   --agent-id <id>           # Agent identifier
#   --output <dir>            # Output directory
#   --validity <duration>     # Cert validity (default: 1y)
# Output:
#   Agent certificate and key files
# Exit Codes:
#   0 - Success
#   1 - Invalid arguments
#   2 - Generation failed
```

### 9.2 API Endpoint Reference

**Location**: `docs/API_REFERENCE.md`

**NOTE**: Full REST API is future enhancement, but document structure now for planning.

#### API Structure

```text
Base URL: https://boxy-server:8443/api/v1

Authentication:
  Authorization: Bearer <api-token>

Content-Type: application/json
```

#### Pool Endpoints

```text
GET /api/v1/pools
  Description: List all pools
  Query Parameters:
    - filter (string, optional): Filter expression
  Response: 200 OK
    {
      "pools": [
        {
          "name": "win-server-2022",
          "type": "vm",
          "backend": "hyperv",
          "backend_agent": "windows-host-01",
          "stats": {
            "total": 10,
            "ready": 3,
            "allocated": 2,
            "min_ready": 10,
            "max_total": 20
          }
        }
      ]
    }

GET /api/v1/pools/{pool-name}
  Description: Get pool details
  Path Parameters:
    - pool-name (string, required)
  Response: 200 OK
    {
      "name": "win-server-2022",
      "type": "vm",
      "backend": "hyperv",
      "config": { ... },
      "stats": { ... }
    }
  Response: 404 Not Found
    {"error": "pool not found"}

PATCH /api/v1/pools/{pool-name}/scale
  Description: Scale pool
  Request Body:
    {
      "min_ready": 15,
      "preheated": 5
    }
  Response: 200 OK
    {"message": "pool scaled successfully"}
```

#### Sandbox Endpoints

```text
POST /api/v1/sandboxes
  Description: Create sandbox
  Request Body:
    {
      "name": "my-lab",
      "duration": "1h",
      "resources": [
        {"pool": "ubuntu-containers", "count": 2},
        {"pool": "win-vms", "count": 1}
      ]
    }
  Response: 201 Created
    {
      "id": "sb-abc123",
      "name": "my-lab",
      "resource_ids": ["res-1", "res-2", "res-3"],
      "state": "creating",
      "created_at": "2024-11-22T14:30:00Z",
      "expires_at": "2024-11-22T15:30:00Z"
    }
  Response: 400 Bad Request
    {"error": "invalid duration format"}
  Response: 403 Forbidden
    {"error": "quota exceeded"}
  Response: 503 Service Unavailable
    {"error": "no resources available in pool ubuntu-containers"}

GET /api/v1/sandboxes
  Description: List sandboxes
  Query Parameters:
    - state (string, optional): Filter by state
    - user_id (string, optional, admin only): Filter by user
  Response: 200 OK
    {
      "sandboxes": [
        {
          "id": "sb-abc123",
          "name": "my-lab",
          "state": "ready",
          "resources": 3,
          "created_at": "2024-11-22T14:30:00Z",
          "expires_at": "2024-11-22T15:30:00Z"
        }
      ]
    }

GET /api/v1/sandboxes/{sandbox-id}
  Description: Get sandbox details
  Response: 200 OK
    {
      "id": "sb-abc123",
      "name": "my-lab",
      "state": "ready",
      "resource_ids": ["res-1", "res-2"],
      "resources": [
        {
          "id": "res-1",
          "pool": "ubuntu-containers",
          "state": "allocated",
          "connection": {
            "type": "ssh",
            "host": "10.0.1.5",
            "port": 22,
            "username": "user-abc",
            "password": "generated-password"
          }
        }
      ],
      "created_at": "2024-11-22T14:30:00Z",
      "expires_at": "2024-11-22T15:30:00Z"
    }

DELETE /api/v1/sandboxes/{sandbox-id}
  Description: Destroy sandbox
  Response: 204 No Content
  Response: 404 Not Found
    {"error": "sandbox not found"}

PATCH /api/v1/sandboxes/{sandbox-id}/extend
  Description: Extend sandbox duration
  Request Body:
    {"additional_duration": "2h"}
  Response: 200 OK
    {
      "id": "sb-abc123",
      "expires_at": "2024-11-22T17:30:00Z"
    }
```

#### Error Response Format

```json
{
  "error": "human-readable error message",
  "code": "ERROR_CODE",
  "details": {
    "field": "additional context"
  },
  "request_id": "req-xyz789"
}
```

#### Common Error Codes

```text
400 Bad Request - Invalid input
401 Unauthorized - Missing or invalid API token
403 Forbidden - Quota exceeded, permission denied
404 Not Found - Resource doesn't exist
409 Conflict - Resource already exists
503 Service Unavailable - No resources available
500 Internal Server Error - Server error
```

### 9.3 OpenAPI Specification

**Future**: Generate OpenAPI 3.0 spec for API documentation and client code generation.

**Location**: `docs/openapi.yaml`

### 9.4 Usage in Testing

```go
// tests/e2e/cli_regression_test.go
func TestCLI_AllCommandsWork(t *testing.T) {
    // Test every command in CLI_REFERENCE.md
    // Ensure exit codes match documented behavior
}

// tests/e2e/api_regression_test.go
func TestAPI_AllEndpointsWork(t *testing.T) {
    // Test every endpoint in API_REFERENCE.md
    // Ensure responses match documented schemas
}
```

---

## 10. Debugging Documentation

**Purpose**: Comprehensive troubleshooting guide for common issues and debugging techniques.

**Location**: `docs/DEBUGGING_GUIDE.md`

### 10.1 Debugging Sections

#### Log Levels and Interpretation

```yaml
# Enable debug logging
logging:
  level: debug      # Most verbose
  format: json      # Structured for log aggregation
```

**Log Levels**:

- `debug`: All operations, including internal state transitions
- `info`: Normal operations (pool replenishment, sandbox creation)
- `warn`: Unexpected but handled situations (retry attempts, degraded state)
- `error`: Failed operations requiring attention

**Key Log Fields**:

```json
{
  "level": "error",
  "time": "2024-11-22T14:30:00Z",
  "msg": "failed to provision resource",
  "component": "pool-manager",
  "pool": "win-server-2022",
  "error": "provider timeout after 5m",
  "request_id": "req-xyz789",
  "resource_id": "res-abc123"
}
```

#### Common Issues and Solutions

**1. Pool Not Replenishing**

*Symptoms*:

```bash
$ boxy pool stats win-server-2022
Ready: 0
Min Ready: 10
Status: DEGRADED
```

*Debugging Steps*:

```bash
# 1. Check pool worker status
boxy pool inspect win-server-2022 | grep worker

# 2. Check provider health
boxy pool validate win-server-2022

# 3. Enable debug logging
export BOXY_LOG_LEVEL=debug
boxy serve

# 4. Check logs for provision failures
grep "provision" ~/.config/boxy/boxy.log | grep error
```

*Common Causes*:

- Provider backend unavailable (Hyper-V service stopped, Docker daemon not running)
- Insufficient host resources (out of memory, disk space)
- Network connectivity issues (agent unreachable)
- Hook execution failures

*Solutions*:

- Verify backend service running: `Get-Service vmms` (Hyper-V) / `systemctl status docker`
- Check host resources: `free -h`, `df -h`
- Test agent connectivity: `telnet windows-host-01 8444`
- Review hook logs: Check `on_provision` hook output

**2. Sandbox Stuck in "Creating" State**

*Symptoms*:

```bash
$ boxy sandbox ls
ID          STATE       RESOURCES
sb-abc123   creating    0/3
```

*Debugging Steps*:

```bash
# 1. Inspect sandbox details
boxy sandbox inspect sb-abc123

# 2. Check allocation worker logs
grep "sb-abc123" ~/.config/boxy/boxy.log

# 3. Check pool availability
boxy pool stats <pool-name>

# 4. Verify allocator can access pools
boxy pool ls
```

*Common Causes*:

- No resources available in requested pool
- Allocation timeout (on_allocate hooks taking too long)
- Database connection issues

*Solutions*:

- Increase pool size or wait for replenishment
- Optimize on_allocate hooks (should be < 2 minutes)
- Check database connectivity

**3. Agent Connection Failures**

*Symptoms*:

```text
ERROR: failed to connect to agent windows-host-01: connection refused
```

*Debugging Steps*:

```bash
# 1. Test network connectivity
ping windows-host-01.internal
telnet windows-host-01.internal 8444

# 2. Verify agent is running on Windows host
# On Windows host:
Get-Process boxy

# 3. Check agent logs
# On Windows host:
Get-Content C:\ProgramData\Boxy\agent.log -Tail 100

# 4. Verify mTLS certificates
boxy admin verify-cert \
  --cert /etc/boxy/agents/windows-01-cert.pem \
  --ca /etc/boxy/ca-cert.pem
```

*Common Causes*:

- Agent not running on Windows host
- Firewall blocking port 8444
- Certificate expired or invalid
- Clock skew between server and agent

*Solutions*:

- Start agent: `boxy agent serve ...`
- Allow port 8444 in Windows Firewall
- Re-issue certificate: `boxy admin issue-cert ...`
- Synchronize clocks (NTP)

**4. Hook Execution Failures**

*Symptoms*:

```text
WARN: hook execution failed: timeout after 5m
ERROR: on_allocate hook failed: exit code 1
```

*Debugging Steps*:

```bash
# 1. Check hook configuration
cat ~/.config/boxy/boxy.yaml | grep -A 10 "hooks:"

# 2. Test hook manually
# Extract hook script and run locally
powershell -Command "Test-Connection localhost"

# 3. Check hook logs (if logging added to hooks)
# Hooks should log to resource metadata
boxy pool resources <pool> --format json | jq '.[] | select(.metadata.hook_error)'

# 4. Increase hook timeout
# Edit config, increase timeout to 10m
```

*Common Causes*:

- Network issues in hook (downloading packages)
- Insufficient permissions
- Syntax errors in script
- Missing dependencies

*Solutions*:

- Add retry logic to hooks
- Test hooks in isolation before adding to config
- Increase timeout for slow operations
- Use on_provision for heavy setup (not on_allocate)

#### Diagnostic Commands

```bash
# Full system health check
boxy admin health-check

# Output:
# ✓ Database: Connected (SQLite)
# ✓ Pools: 3 running, 0 degraded
# ✓ Agents: 1 connected
# ✗ Disk: Low space (15% free)
# ⚠ Warning: Pool 'win-server-2022' behind min_ready by 5 resources

# Export logs for support
boxy admin export-logs \
  --since 24h \
  --output /tmp/boxy-logs.tar.gz

# Database integrity check
boxy admin db-check

# Cleanup orphaned resources (manual)
boxy admin cleanup-orphans --dry-run
```

#### Debugging with Delve (Go)

```bash
# Build with debug symbols
go build -gcflags="all=-N -l" -o boxy-debug cmd/boxy/main.go

# Run with Delve
dlv exec ./boxy-debug -- serve --config boxy.yaml

# Set breakpoints
(dlv) break internal/core/pool/manager.go:123
(dlv) continue

# Inspect variables
(dlv) print pool.config
(dlv) print resource.State
```

#### Performance Profiling

```bash
# Enable pprof HTTP server
export BOXY_PPROF=:6060
boxy serve

# Capture CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Capture heap profile
go tool pprof http://localhost:6060/debug/pprof/heap

# View goroutines
curl http://localhost:6060/debug/pprof/goroutine?debug=2
```

#### Network Debugging (Agent Communication)

```bash
# Capture gRPC traffic (server side)
export GRPC_GO_LOG_VERBOSITY_LEVEL=99
export GRPC_GO_LOG_SEVERITY_LEVEL=info
boxy serve

# Tcpdump on agent port
tcpdump -i eth0 -n port 8444 -w /tmp/agent-traffic.pcap

# Analyze with Wireshark (filter: tcp.port == 8444)
```

### 10.2 Debugging Checklist

**Before Opening Issue**:

- [ ] Check logs at debug level
- [ ] Run `boxy admin health-check`
- [ ] Verify backend services running (Docker, Hyper-V)
- [ ] Test network connectivity (if using agents)
- [ ] Review recent config changes
- [ ] Try with minimal config (single pool, no hooks)
- [ ] Check GitHub issues for similar problems

**Information to Include in Issue**:

- Boxy version: `boxy version`
- OS and version
- Backend provider (Docker, Hyper-V, etc.)
- Full config file (redact secrets)
- Logs (with debug level)
- Steps to reproduce

### 10.3 FAQ

**Q: Why is my pool not warming resources?**

A: Check `preheating.enabled: true` and `preheating.count > 0` in config.

**Q: Can I see hook output?**

A: Hook output is captured in resource metadata. Use `boxy pool resources <pool> --format json` and check `.metadata.hook_output`.

**Q: How do I reset a stuck pool?**

A: Stop Boxy, manually clean up resources in provider, restart Boxy. Pool will reprovision.

**Q: Agent certificate expired, how to renew?**

A: Re-issue cert with `boxy admin issue-cert`, restart agent with new cert.

**Q: How to completely reset Boxy?**

A: Stop service, delete database (`rm ~/.config/boxy/boxy.db`), restart. WARNING: Destroys all state.

---

## 11. Config File Location Fix

**CRITICAL CHANGE**: Move config from home directory to current working directory.

### 11.1 Problem with Current Approach

**Current (WRONG)**:

```bash
~/.config/boxy/boxy.yaml    # System-wide config in home directory
~/.config/boxy/boxy.db      # Database in home directory
```

**Issues:**

- ❌ Not standard for service-oriented tools
- ❌ Difficult to manage multiple Boxy instances
- ❌ Not clear which config is being used when running from different directories
- ❌ Doesn't follow Docker/Compose patterns (mount volumes from current dir)
- ❌ Awkward for CI/CD (need to setup home directory structure)

### 11.2 New Approach (CORRECT)

**v1 Standard**:

```bash
./boxy.yaml                 # Config in current directory
./boxy.db                   # Database in current directory (or configured path)
```

**Benefits:**

- ✅ Standard pattern for modern tools (docker-compose.yml, k8s manifests, etc.)
- ✅ Clear which config is active (the one in current directory)
- ✅ Easy to manage multiple Boxy deployments (different directories)
- ✅ Works naturally with Docker volume mounts
- ✅ Simple for CI/CD (just place config in workspace)

### 11.3 Config File Name Options

**Recommended**: `./boxy.yaml`

**Alternatives considered:**

- `./boxy.yml` - Shorter but less common than `.yaml`
- `./.boxy.yaml` - Hidden file, but adds confusion
- `./boxy.conf` - Not standard for YAML configs
- `./config.yaml` - Too generic

**Decision**: Use `./boxy.yaml` (most conventional)

### 11.4 Config Discovery

```go
// internal/config/loader.go

func LoadConfig() (*Config, error) {
    // 1. Check --config flag
    if configFlag != "" {
        return loadFromPath(configFlag)
    }

    // 2. Check BOXY_CONFIG environment variable
    if envConfig := os.Getenv("BOXY_CONFIG"); envConfig != "" {
        return loadFromPath(envConfig)
    }

    // 3. Check current directory
    if fileExists("./boxy.yaml") {
        return loadFromPath("./boxy.yaml")
    }

    // 4. Check current directory (.yml variant)
    if fileExists("./boxy.yml") {
        return loadFromPath("./boxy.yml")
    }

    // 5. Error - no config found
    return nil, fmt.Errorf("no config file found. Run 'boxy init' to create one")
}
```

**Priority order:**

1. `--config` flag (highest priority)
2. `BOXY_CONFIG` environment variable
3. `./boxy.yaml` (current directory)
4. `./boxy.yml` (fallback)
5. Error if none found

### 11.5 Database Location

**Default**: Specified in config file

```yaml
# boxy.yaml
storage:
  type: sqlite
  path: ./boxy.db              # Current directory by default
  # path: /var/lib/boxy/boxy.db  # Or absolute path for production
```

**Production recommendations:**

- Development: `./boxy.db` (current directory)
- Production: `/var/lib/boxy/boxy.db` (system location)
- Docker: `/data/boxy.db` (mounted volume)

### 11.6 Migration from Old Location

**Backward compatibility** (v1.0 only, remove in v1.1):

```go
func LoadConfig() (*Config, error) {
    // ... try new locations first ...

    // DEPRECATED: Check old home directory location
    oldPath := filepath.Join(os.UserHomeDir(), ".config", "boxy", "boxy.yaml")
    if fileExists(oldPath) {
        log.Warn("DEPRECATED: Config found in ~/.config/boxy/boxy.yaml")
        log.Warn("Please move to ./boxy.yaml in current directory")
        log.Warn("Support for old location will be removed in v1.1")
        return loadFromPath(oldPath)
    }

    return nil, fmt.Errorf("no config file found")
}
```

### 11.7 Updated CLI Commands

```bash
# Initialize config in current directory (NEW)
boxy init
# Creates: ./boxy.yaml

# Specify custom location
boxy serve --config /etc/boxy/boxy.yaml

# Environment variable
export BOXY_CONFIG=/etc/boxy/boxy.yaml
boxy serve
```

### 11.8 Documentation Updates Required

**Files to update:**

- README.md - All config path examples
- docs/V1_IMPLEMENTATION_PLAN.md - All config examples
- docs/CONFIG_REFERENCE.md - Config location documentation
- docs/DEBUGGING_GUIDE.md - Update log/config paths
- boxy.example.yaml - Header comments
- All code examples in docs/

**Search and replace:**

```bash
# Find all instances
grep -r "~/.config/boxy" docs/
grep -r ".config/boxy" internal/

# Replace with
./boxy.yaml
```

---

## 12. Docker and Docker Compose Support

**CRITICAL FOR v1**: Ensure Boxy server runs well in Docker with proper documentation and examples.

### 12.1 Why Docker Support is Essential

**User needs:**

- Run Boxy server in containerized environment
- Easy deployment without Go toolchain
- Consistent environment across dev/staging/prod
- Integration with existing Docker-based infrastructure

**Use cases:**

- Development: Run Boxy server in Docker, manage local Docker containers
- Production: Deploy Boxy server as container, manage remote agents
- CI/CD: Spin up Boxy in pipeline, create ephemeral test environments

### 12.2 Dockerfile for Boxy Server

**Location**: `Dockerfile`

```dockerfile
# Multi-stage build for minimal image size
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o boxy cmd/boxy/main.go

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/boxy /usr/local/bin/boxy

# Create data directory
RUN mkdir -p /data

# Config and database will be in /data
VOLUME ["/data"]

# Expose server port
EXPOSE 8443

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD boxy admin health-check || exit 1

# Default command
ENTRYPOINT ["boxy"]
CMD ["serve", "--config", "/data/boxy.yaml"]
```

**Build and run:**

```bash
# Build image
docker build -t boxy:v1 .

# Run server
docker run -d \
  --name boxy-server \
  -v $(pwd)/config:/data \
  -p 8443:8443 \
  boxy:v1
```

### 12.3 Docker Compose Examples

#### Example 1: Boxy Server + Docker Provider (Development)

**Location**: `examples/docker-compose/dev/docker-compose.yml`

```yaml
version: '3.8'

services:
  boxy-server:
    image: boxy:v1
    container_name: boxy-server
    volumes:
      - ./boxy.yaml:/data/boxy.yaml
      - ./boxy.db:/data/boxy.db
      - /var/run/docker.sock:/var/run/docker.sock  # Access host Docker
    ports:
      - "8443:8443"
    environment:
      - BOXY_LOG_LEVEL=debug
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "boxy", "admin", "health-check"]
      interval: 30s
      timeout: 3s
      retries: 3

  # Example: Database for multi-tenancy
  postgres:
    image: postgres:15
    container_name: boxy-postgres
    environment:
      POSTGRES_DB: boxy
      POSTGRES_USER: boxy
      POSTGRES_PASSWORD: boxy_password
    volumes:
      - postgres-data:/var/lib/postgresql/data
    ports:
      - "5432:5432"

volumes:
  postgres-data:
```

**Config**: `examples/docker-compose/dev/boxy.yaml`

```yaml
storage:
  type: sqlite
  path: /data/boxy.db

server:
  listen_address: 0.0.0.0:8443

pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    # Docker socket mounted from host
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10
```

#### Example 2: Boxy Server + Remote Agent (Production)

**Location**: `examples/docker-compose/production/docker-compose.yml`

```yaml
version: '3.8'

services:
  boxy-server:
    image: boxy:v1
    container_name: boxy-server
    volumes:
      - ./config:/data
      - ./certs:/certs:ro
    ports:
      - "8443:8443"
    environment:
      - BOXY_LOG_LEVEL=info
    restart: unless-stopped
    networks:
      - boxy-network

  # Postgres for production
  postgres:
    image: postgres:15
    container_name: boxy-db
    environment:
      POSTGRES_DB: boxy
      POSTGRES_USER: boxy
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - postgres-data:/var/lib/postgresql/data
    networks:
      - boxy-network
    restart: unless-stopped

  # Prometheus for metrics
  prometheus:
    image: prom/prometheus:latest
    container_name: boxy-prometheus
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus
    ports:
      - "9090:9090"
    networks:
      - boxy-network

  # Grafana for dashboards
  grafana:
    image: grafana/grafana:latest
    container_name: boxy-grafana
    volumes:
      - grafana-data:/var/lib/grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_PASSWORD}
    networks:
      - boxy-network

networks:
  boxy-network:
    driver: bridge

volumes:
  postgres-data:
  prometheus-data:
  grafana-data:
```

### 12.4 Docker-Specific Configuration

**Environment variable overrides:**

```bash
# Override config values with environment variables
docker run -d \
  -e BOXY_LOG_LEVEL=debug \
  -e BOXY_SERVER_LISTEN_ADDRESS=0.0.0.0:8443 \
  -e BOXY_STORAGE_PATH=/data/boxy.db \
  boxy:v1
```

**Implementation:**

```go
// internal/config/loader.go
func LoadConfig() (*Config, error) {
    cfg := loadFromFile()

    // Override with environment variables
    if logLevel := os.Getenv("BOXY_LOG_LEVEL"); logLevel != "" {
        cfg.Logging.Level = logLevel
    }

    if listenAddr := os.Getenv("BOXY_SERVER_LISTEN_ADDRESS"); listenAddr != "" {
        cfg.Server.ListenAddress = listenAddr
    }

    return cfg, nil
}
```

### 12.5 Volume Mounts and Persistence

**Critical volumes:**

```bash
docker run -d \
  -v $(pwd)/boxy.yaml:/data/boxy.yaml:ro \      # Config (read-only)
  -v $(pwd)/boxy.db:/data/boxy.db \              # Database (read-write)
  -v $(pwd)/certs:/certs:ro \                    # TLS certs (read-only)
  -v /var/run/docker.sock:/var/run/docker.sock \ # Docker access (if using Docker provider)
  boxy:v1
```

### 12.6 Docker Image Distribution

**GitHub Container Registry:**

```bash
# Build and tag
docker build -t ghcr.io/geogboe/boxy:v1.0.0 .
docker build -t ghcr.io/geogboe/boxy:latest .

# Push
docker push ghcr.io/geogboe/boxy:v1.0.0
docker push ghcr.io/geogboe/boxy:latest

# Pull
docker pull ghcr.io/geogboe/boxy:latest
```

**Docker Hub (optional):**

```bash
docker build -t geogboe/boxy:v1.0.0 .
docker push geogboe/boxy:v1.0.0
```

### 12.7 Documentation Required

**Create**: `docs/DOCKER_DEPLOYMENT.md`

Contents:

- Building Docker images
- Running Boxy in Docker
- Docker Compose examples
- Volume mount guide
- Environment variable reference
- Networking considerations
- Security best practices
- Troubleshooting Docker deployments

**Create**: `examples/docker-compose/README.md`

Contents:

- Overview of examples
- Quick start for each example
- Customization guide
- Production deployment checklist

### 12.8 CI/CD Integration

**GitHub Actions** - Build and publish Docker images:

```yaml
# .github/workflows/docker.yml
name: Docker Build and Push

on:
  push:
    tags:
      - 'v*'

jobs:
  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v2

      - name: Login to GitHub Container Registry
        uses: docker/login-action@v2
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Extract version
        id: meta
        run: echo "version=${GITHUB_REF#refs/tags/v}" >> $GITHUB_OUTPUT

      - name: Build and push
        uses: docker/build-push-action@v4
        with:
          context: .
          push: true
          tags: |
            ghcr.io/geogboe/boxy:${{ steps.meta.outputs.version }}
            ghcr.io/geogboe/boxy:latest
          platforms: linux/amd64,linux/arm64
```

### 12.9 Testing Docker Deployment

**Add to test suite:**

```go
// tests/e2e/docker_test.go
func TestE2E_DockerDeployment(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping docker deployment test")
    }

    // 1. Build Docker image
    // 2. Start container with docker-compose
    // 3. Wait for health check
    // 4. Create sandbox via CLI
    // 5. Verify sandbox created
    // 6. Cleanup
}
```

---

## 13. Documentation Updates

### 7.2 CLAUDE.md Updates

**Section to update: "Core Concepts & Terminology"**

```markdown
### Resources
Individual compute units that can be provisioned:
- **VMs** (Virtual Machines) - Full OS instances
- **Containers** - Lightweight isolated environments

**Resource States:**
- **Provisioned** - Created but cold (stopped)
- **Ready** - Running and warm (preheated, available)
- **Allocated** - In use by a sandbox
- **Destroyed** - Cleaned up

### Sandbox
A **time-bound collection of allocated resources**. A sandbox might contain:
- 3 server VMs (Windows Server)
- 1 client VM (Windows 10)
- 2 containers (Linux)

Sandboxes are:
- Time-bound (auto-expire after duration)
- Isolated (each sandbox is independent)
- Ephemeral (destroyed when no longer needed)
- Clean (resources never reused)

### Pool
A **self-replenishing collection of resources of the same type**. Pools ensure resources are always available:

- When a resource is allocated from the pool → automatically provision a replacement
- Maintain a minimum count of ready resources
- Support preheating (keep some resources warm/running)
- Automatic recycling (refresh resources regularly)

**Example Pool Configurations:**
```yaml
pools:
  - name: win-server-2022
    type: vm
    backend: hyperv
    min_ready: 10        # Total resources
    max_total: 20

    preheating:
      enabled: true
      count: 3           # Keep 3 warm
      recycle_interval: 1h
```

### Allocator

An **internal orchestration component** that manages resource movement between pools and sandboxes.

**Not user-facing** - users interact with Pools and Sandboxes, Allocator works behind the scenes.

```text

**Section to add: "Hook System"**

```markdown
### Hooks

Lifecycle hooks allow customization at specific points:

**on_provision** - Runs after provider creates resource (cold state)
- Use for: validation, snapshots, software installation
- Timing: During pool replenishment (can be slow)

**on_allocate** - Runs when user requests resource
- Use for: user account creation, granting access, personalization
- Timing: User is waiting (must be fast)

**Example:**
```yaml
hooks:
  on_provision:
    - type: script
      shell: powershell
      inline: |
        # Validate VM

        Test-Connection localhost

  on_allocate:
    - type: script
      shell: powershell
      inline: |
        # Create user

        New-LocalUser -Name "${username}" -Password "${password}"
```

```text

### 7.3 ADR-005: Pool/Sandbox Peer Architecture

**Create `docs/decisions/adr-005-pool-sandbox-peer-architecture.md`:**

```markdown
# ADR-005: Pool and Sandbox as Peer Components with Internal Allocator

**Date**: 2024-11-22
**Status**: Accepted

## Context

Original architecture had tight coupling:
- Sandbox called `pool.Allocate()` directly
- Pool tracked resources even after allocation
- Dual ownership of resources
- Pool couldn't be managed independently

## Decision

Refactor to peer architecture with internal Allocator:

```

┌──────────────┐          ┌──────────────┐
│    Pool      │          │   Sandbox    │
│              │          │              │
│ (manages     │          │ (manages     │
│  unallocated)│          │  allocated)  │
└──────┬───────┘          └──────┬───────┘
       │                         │
       └────────┬─────────┬──────┘
                ↓         ↓
         ┌──────────────────────┐
         │     Allocator        │ (internal)
         │                      │
         │ Orchestrates resource│
         │ movement             │
         └──────────────────────┘

```text

## Consequences

**Positive:**
- ✅ Clear separation of concerns
- ✅ Pool is first-class component (can be managed via CLI)
- ✅ Single source of truth for resources (Allocator)
- ✅ No tight coupling between Pool and Sandbox
- ✅ Easier to test each component independently

**Negative:**
- ❌ Additional abstraction layer (Allocator)
- ❌ Need to refactor existing code
- ❌ Migration required (minimal - internal only)

## Implementation

See docs/V1_IMPLEMENTATION_PLAN.md for detailed implementation guide.
```

### 7.4 Update MVP_DESIGN.md

**Update component roles section:**

```markdown
## Component Roles

### Provider
**Dumb, stateless CRUD interface**:
- No decisions, no state management
- Just translates Boxy commands to backend APIs
- Methods: Provision, Destroy, GetStatus, GetConnectionInfo, Exec, Update, HealthCheck

### Pool
**Manages unallocated resources**:
- Provisions resources (via provider)
- Runs on_provision hooks
- Maintains min_ready count
- Health checking
- Preheating (keeps some warm)
- Recycling (refreshes regularly)

### Sandbox
**Manages allocated resources**:
- Creates sandbox records
- Coordinates multi-resource allocation (via Allocator)
- Tracks lifecycle (Creating, Ready, Expiring, Destroyed)
- Auto-cleanup of expired sandboxes

### Allocator (Internal)
**Orchestrates resource movement**:
- Manages Pool → Sandbox transitions
- Runs on_allocate hooks
- Tracks resource ownership
- Single source of truth

**Not user-facing** - Pool and Sandbox are user-facing, Allocator is internal.
```

---

## 14. Testing Strategy

### 8.1 Unit Tests

**New components to test:**

```go
// internal/core/allocator/allocator_test.go
func TestAllocator_AllocateFromPool(t *testing.T)
func TestAllocator_ReleaseResources(t *testing.T)
func TestAllocator_ConcurrentAllocations(t *testing.T)

// internal/core/pool/preheating_test.go
func TestPool_EnsurePreheated(t *testing.T)
func TestPool_WarmResource(t *testing.T)
func TestPool_RecycleResources_Rolling(t *testing.T)
func TestPool_RecycleResources_AllAtOnce(t *testing.T)

// internal/core/user/user_test.go
func TestUser_GenerateAPIToken(t *testing.T)
func TestUser_Authentication(t *testing.T)

// internal/core/pool/manager_test.go
func TestPool_ProvisionCold(t *testing.T)  // Resources start as Provisioned
```

### 8.2 Integration Tests

**Test with Docker (real provider):**

```go
// tests/integration/preheating_test.go
func TestPreheating_DockerContainers(t *testing.T) {
    // 1. Create pool with preheating enabled
    // 2. Verify cold resources created
    // 3. Verify preheating worker warms resources
    // 4. Allocate warm resource (fast)
    // 5. Allocate cold resource (slower but works)
}

// tests/integration/recycling_test.go
func TestRecycling_RollingStrategy(t *testing.T) {
    // 1. Create pool with recycling enabled
    // 2. Wait for recycle interval
    // 3. Verify resources recycled one at a time
    // 4. Verify pool maintains availability
}

// tests/integration/multitenancy_test.go
func TestMultiTenancy_QuotaEnforcement(t *testing.T) {
    // 1. Create user with quota
    // 2. Create sandboxes up to quota
    // 3. Verify quota exceeded error
    // 4. Destroy sandbox
    // 5. Verify can create again
}
```

### 8.3 E2E Tests

**Real Docker workflows:**

```go
// tests/e2e/quick_testing_usecase_test.go
func TestE2E_QuickTestingUseCase(t *testing.T) {
    // Simulates primary use case
    // 1. Start Boxy with Docker pool (preheating enabled)
    // 2. Request sandbox
    // 3. Verify instant allocation (preheated)
    // 4. Test resource
    // 5. Destroy sandbox
    // 6. Verify cleanup
}

// tests/e2e/ci_runner_usecase_test.go
func TestE2E_CIRunnerUseCase(t *testing.T) {
    // Simulates CI/CD runner use case
    // Multiple rapid sandbox creations
}
```

### 8.4 Test Coverage Goals

- Unit tests: > 80%
- Integration tests: All major workflows
- E2E tests: All documented use cases
- Security tests: Password generation, token generation

---

## 15. Migration Guide

### 9.1 Configuration Migration

**No breaking changes to YAML!**

**Old config still works:**

```yaml
pools:
  - name: ubuntu-containers
    min_ready: 5
    max_total: 10

    hooks:
      after_provision:  # OLD name
        - ...
```

**New config recommended:**

```yaml
pools:
  - name: ubuntu-containers
    min_ready: 5
    max_total: 10

    preheating:       # NEW
      enabled: true
      count: 3
      recycle_interval: 1h

    hooks:
      on_provision:   # NEW name (after_provision still works)
        - ...
      on_allocate:    # NEW name (before_allocate still works)
        - ...
```

**Backwards compatibility:**

- `after_provision` → logs warning, maps to `on_provision`
- `before_allocate` → logs warning, maps to `on_allocate`
- Missing `preheating` config → defaults to preheating disabled

### 9.2 Database Migration

**Add columns to existing tables:**

```sql
-- Add user tracking to sandboxes
ALTER TABLE sandboxes ADD COLUMN user_id TEXT;
ALTER TABLE sandboxes ADD COLUMN team_id TEXT;

CREATE INDEX idx_sandboxes_user_id ON sandboxes(user_id);
CREATE INDEX idx_sandboxes_team_id ON sandboxes(team_id);

-- Create users table
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT,
    api_token TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_users_token ON users(api_token);

-- Create default admin user
INSERT INTO users (id, username, email, api_token, role, created_at, updated_at)
VALUES (
    'user-admin',
    'admin',
    'admin@localhost',
    'bxy_admin_default_change_this',
    'admin',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);
```

**Migration command:**

```bash
boxy admin migrate-v1
# Runs database migrations
# Creates default admin user
# Logs API token for admin
```

### 9.3 Code Migration

**Internal refactor only - no API changes!**

Existing CLI commands work unchanged:

```bash
boxy sandbox create -p pool:1 -d 1h  # Works exactly the same
```

---

## 10. Future Considerations

### 10.1 Retry Strategy (Noted for Future)

User mentioned: "lots of components might need automatic retry"

**Components that could benefit:**

- Provider.Provision() - retry on transient failures
- Provider.Update() (warmup) - retry if resource slow to start
- Hook execution - retry on network errors
- Recycling - retry if destroy fails

**Proposed for v2:**

```yaml
pools:
  - name: win-test-vms

    retry:
      enabled: true
      max_attempts: 3
      backoff: exponential  # 1s, 2s, 4s

      # What to retry
      provision: true
      warmup: true
      hooks: true
```

**Alternative: on_error hook (user suggestion):**

```yaml
hooks:
  on_error:
    - type: script
      inline: |
        # Log error details
        # Send alert
        # Optionally: drop into debug mode?
```

### 10.2 Pool Layering (v3)

**User mentioned:** "building layers from pools"

```yaml
# Future concept
pools:
  - name: base-windows
    image: windows-11-clean.vhdx

  - name: dev-windows
    base_pool: base-windows    # Inherit!
    hooks:
      on_provision:
        - Install Visual Studio
```

### 10.3 Distributed Agents (v2)

**ADR-004 stays**, but timeline shifts to v2:

- v1: Local providers only (Docker, Hyper-V on same host)
- v2: Remote agents via gRPC/mTLS

**Rationale:** Get core architecture solid before adding distributed complexity.

### 10.4 Network Isolation (v2)

**User mentioned:** "network isolation coming in v2"

- Overlay networks (WireGuard/Headscale)
- Sandbox-level network isolation
- Multi-resource networking

---

## 16. Implementation Checklist

### Phase 1: Architecture Refactor

- [ ] Create Allocator component (`internal/core/allocator/`)
- [ ] Refactor Pool to remove Allocate/Release methods
- [ ] Refactor Sandbox to use Allocator
- [ ] Update ResourceRepository queries
- [ ] Add unit tests for Allocator
- [ ] Add integration tests for refactored flow

### Phase 2: Preheating & Recycling

- [ ] Add new resource states (Provisioned, Warming, Recycling)
- [ ] Update PoolConfig with PreheatingConfig
- [ ] Implement preheating worker
- [ ] Implement recycling worker
- [ ] Update provisionOne to create cold resources
- [ ] Add warmResource method
- [ ] Add recycling tests

### Phase 3: Terminology Updates

- [ ] Rename hooks: after_provision → on_provision, before_allocate → on_allocate
- [ ] Add backwards compatibility for old names
- [ ] Update all example configs
- [ ] Update documentation

### Phase 4: Multi-Tenancy

- [ ] Create User model and repository
- [ ] Create Team model and repository (optional)
- [ ] Add database migrations
- [ ] Implement API token generation
- [ ] Add authentication middleware
- [ ] Update Sandbox model with user_id/team_id
- [ ] Implement quota checking
- [ ] Add user management CLI commands

### Phase 5: Base Image Validation

- [ ] Add validation config to PoolConfig
- [ ] Implement validation logic
- [ ] Add `boxy admin validate-image` command
- [ ] Create validation tests

### Phase 6: Pool CLI Commands

- [ ] Add pool lifecycle commands (start, stop, etc.)
- [ ] Add pool management commands (scale, drain, etc.)
- [ ] Add pool inspection commands (inspect, resources)
- [ ] Add pool maintenance commands (recycle, validate)
- [ ] Update serve command to support new pool model

### Phase 7: Documentation

- [ ] Update CLAUDE.md
- [ ] Update MVP_DESIGN.md
- [ ] Create ADR-005
- [ ] Update ADR-004 (clarify v2 timeline)
- [ ] Update ROADMAP.md
- [ ] Review all docs for consistency

### Phase 8: Testing

- [ ] Write unit tests for all new components
- [ ] Write integration tests (Docker)
- [ ] Write E2E tests for use cases
- [ ] Run full test suite with race detector
- [ ] Verify no regressions

### Phase 9: Migration

- [ ] Create database migration tool
- [ ] Test migration on sample database
- [ ] Create migration documentation

### Phase 10: Final Review

- [ ] Code review (if team)
- [ ] Security audit
- [ ] Performance testing
- [ ] Documentation review
- [ ] Create release notes

---

## 17. Success Criteria

v1 is complete when:

1. ✅ All tests pass (unit, integration, E2E)
2. ✅ Security fix verified (password generation)
3. ✅ Preheating works with Docker
4. ✅ Recycling works (rolling strategy)
5. ✅ Multi-tenancy implemented (users, tokens, quotas)
6. ✅ Pool CLI commands functional
7. ✅ All documentation updated and consistent
8. ✅ Migration tested
9. ✅ Primary use case (quick testing) works end-to-end
10. ✅ No regressions from current functionality

---

## Timeline Estimate

**Note:** This is a significant refactor. Estimate 2-3 weeks for full implementation and testing.

**Week 1:**

- Architecture refactor
- Preheating & recycling
- Core functionality

**Week 2:**

- Multi-tenancy
- Pool CLI commands
- Documentation updates

**Week 3:**

- Testing
- Migration
- Final review & polish

---

## Notes for Implementation Session

1. **Start with architecture refactor** - Foundation for everything else
2. **Test incrementally** - Don't wait until end to test
3. **Keep backwards compatibility** - Migration should be smooth
4. **Document as you go** - Don't leave docs for last
5. **Security first** - Already fixed passwords, maintain this standard
6. **User experience** - No breaking changes to CLI/API

---

**This document is ready for implementation in a new session.**

All architectural decisions are finalized. All requirements are specified. Proceed with confidence! 🚀

---

**Version:** 1.0
**Last Updated:** 2024-11-22
**Author:** Claude (AI) + User collaboration
**Ready for:** Implementation in new session

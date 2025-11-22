# Boxy v1 Implementation Plan

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
7. 📖 **Documentation** - Complete consistency review and updates

---

## Table of Contents

1. [Architectural Refactor](#1-architectural-refactor)
2. [Preheating & Recycling System](#2-preheating--recycling-system)
3. [Terminology Updates](#3-terminology-updates)
4. [Multi-Tenancy](#4-multi-tenancy)
5. [Base Image Validation](#5-base-image-validation)
6. [Pool as First-Class Component](#6-pool-as-first-class-component)
7. [Documentation Updates](#7-documentation-updates)
8. [Testing Strategy](#8-testing-strategy)
9. [Migration Guide](#9-migration-guide)
10. [Future Considerations](#10-future-considerations)

---

## 1. Architectural Refactor

### Current Architecture (Problems)

```
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

```
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
|----------------|----------|------------|-----------|
| Provisioned    | Pool     | Pool       | nil       |
| Ready          | Pool     | Pool       | nil       |
| Allocated      | Sandbox  | Allocator  | set       |
| Destroyed      | None     | Repository | nil       |

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

```
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
```
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

## 7. Documentation Updates

### 7.1 Files to Update

1. ✅ **docs/USE_CASES.md** - Created
2. ✅ **README.md** - Updated with use cases
3. ⏳ **CLAUDE.md** - Update architecture section
4. ⏳ **docs/architecture/MVP_DESIGN.md** - Update for v1 changes
5. ⏳ **ADR-002** - Provider architecture (no changes needed)
6. ⏳ **ADR-003** - Configuration/state storage (minor updates)
7. ⏳ **ADR-004** - Distributed agent architecture (clarify v2 timeline)
8. ⏳ **NEW: ADR-005** - Pool/Sandbox peer architecture
9. ⏳ **docs/ROADMAP.md** - Update v1 scope

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
```

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
```

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
```

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

## 8. Testing Strategy

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

## 9. Migration Guide

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

## Implementation Checklist

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

## Success Criteria

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

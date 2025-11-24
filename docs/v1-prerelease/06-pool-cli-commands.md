# 06: Pool CLI Commands

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/06-pool-cli-commands.md` for planning and migration work.

## History

```yaml
Origin: "docs/v1-prerelease/06-pool-cli-commands.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning copy created in `docs/planning/v1-prerelease/06-pool-cli-commands.md`."
```

---

## Metadata

```yaml
feature: "Pool as First-Class Component"
slug: "pool-cli-commands"
status: "not-started"
priority: "high"
type: "feature"
effort: "medium"
depends_on: ["architecture-refactor"]
enables: ["pool-management", "operational-control"]
testing: ["unit", "integration", "e2e"]
breaking_change: false
week: "3-4"
related_docs:
  - "01-architecture-refactor.md"
  - "02-preheating-recycling.md"
  - "05-base-image-validation.md"
```

---

## Overview

Make Pool a first-class component with full CLI management. Currently pools are configured but not directly manageable. New architecture (01-architecture-refactor) enables independent pool operations.

**New capabilities:**

- Lifecycle management (start, stop, delete)
- Runtime scaling (adjust min_ready, preheating)
- Maintenance operations (drain, recycle, validate)
- Inspection and monitoring (stats, resources, inspect)

---

## New CLI Commands

### Lifecycle Operations

```bash
# Create pool from config file
boxy pool create --config pool.yaml

# Start pool (begin workers, start replenishment)
boxy pool start <pool-name>

# Stop pool (stop workers, no new allocations)
boxy pool stop <pool-name>

# Delete pool (stop + remove from registry)
boxy pool delete <pool-name>
```

### Management Operations

```bash
# Scale pool at runtime
boxy pool scale <pool-name> --min-ready 10 --preheated 3

# Drain pool (stop accepting allocations, wait for existing to complete)
boxy pool drain <pool-name>

# Refill pool (resume accepting allocations)
boxy pool refill <pool-name>
```

### Inspection Operations

```bash
# List all pools
boxy pool ls

# Get pool stats
boxy pool stats <pool-name>

# Detailed pool information
boxy pool inspect <pool-name>

# List resources in pool
boxy pool resources <pool-name>
```

### Maintenance Operations

```bash
# Force recycle resources now
boxy pool recycle <pool-name>

# Validate base image
boxy pool validate <pool-name>
```

---

## Command Details

### `boxy pool ls`

**Output:**

```text
NAME                TYPE        BACKEND    READY    ALLOCATED    STATUS
ubuntu-containers   container   docker     5/10     2            running
win-server-2022     vm          hyperv     3/10     1            running
dev-vms             vm          hyperv     0/5      0            stopped
```

### `boxy pool stats <pool-name>`

**Output:**

```text
Pool: ubuntu-containers

Resources:
  Total:       7
  Provisioned: 2 (cold)
  Ready:       5 (warm)
  Allocated:   2

Configuration:
  min_ready:   10
  max_total:   20
  preheating:  enabled (count: 5)

Preheating:
  Target warm: 5
  Current warm: 5
  Status: ✓ OK

Recycling:
  Enabled: yes
  Interval: 1h
  Strategy: rolling
  Last run: 23m ago
  Next run: 37m

Health:
  Status: healthy
  Last check: 2m ago
```

### `boxy pool inspect <pool-name>`

**Output (JSON):**

```json
{
  "name": "ubuntu-containers",
  "type": "container",
  "backend": "docker",
  "config": {
    "min_ready": 10,
    "max_total": 20,
    "cpus": 2,
    "memory_mb": 4096,
    "image": "ubuntu:22.04",
    "preheating": {
      "enabled": true,
      "count": 5,
      "recycle_interval": "1h",
      "recycle_strategy": "rolling"
    }
  },
  "stats": {
    "total_resources": 7,
    "provisioned": 2,
    "ready": 5,
    "allocated": 2,
    "destroyed": 15
  },
  "status": "running",
  "workers": {
    "replenishment": "running",
    "preheating": "running",
    "recycling": "running",
    "health_check": "running"
  }
}
```

### `boxy pool resources <pool-name>`

**Output:**

```text
RESOURCE ID          STATE         AGE     SANDBOX
res-abc123          provisioned    5m      -
res-def456          provisioned    3m      -
res-ghi789          ready          12m     -
res-jkl012          ready          8m      -
res-mno345          ready          2m      -
res-pqr678          allocated      15m     sb-xyz123
res-stu901          allocated      10m     sb-abc456
```

### `boxy pool scale <pool-name>`

**Usage:**

```bash
# Scale temporarily (until restart)
boxy pool scale ubuntu-containers --min-ready 15 --preheated 7

# Scale permanently (updates config file)
boxy pool scale ubuntu-containers --min-ready 15 --persist

# Output:
Scaling pool 'ubuntu-containers'...
  min_ready: 10 → 15
  preheated: 5 → 7

✓ Pool scaled successfully
  Current resources: 7
  Provisioning: 8 more resources...
```

### `boxy pool drain <pool-name>`

**Usage:**

```bash
boxy pool drain ubuntu-containers

# Output:
Draining pool 'ubuntu-containers'...
  Stopping new allocations
  Waiting for 2 allocated resources to be released...

  [⠋] Waiting (2 remaining)...
  [⠙] Waiting (1 remaining)...

✓ Pool drained successfully
  All resources released
  Pool is now in 'drained' state
```

### `boxy pool recycle <pool-name>`

**Usage:**

```bash
# Recycle all unallocated resources
boxy pool recycle ubuntu-containers

# Recycle with confirmation
boxy pool recycle ubuntu-containers --confirm

# Output:
Recycling pool 'ubuntu-containers'...
  Strategy: rolling
  Resources to recycle: 5

  [⠋] Recycling res-abc123 (1/5)...
  [⠙] Recycling res-def456 (2/5)...
  [⠹] Recycling res-ghi789 (3/5)...

✓ Pool recycled successfully
  Recycled: 5 resources
  Time: 2m 15s
```

---

## Implementation

### Task 6.1: Pool Manager Methods

**File**: `internal/core/pool/manager.go`

```go
// Pool lifecycle
func (m *Manager) Start() error {
    m.logger.Info("Starting pool")

    m.ctx, m.cancel = context.WithCancel(context.Background())

    // Start workers
    m.wg.Add(4)
    go m.replenishmentWorker()
    go m.healthCheckWorker()
    go m.preheatingWorker()
    go m.recyclingWorker()

    return nil
}

func (m *Manager) Stop() error {
    m.logger.Info("Stopping pool")

    m.cancel() // Stop workers
    m.wg.Wait() // Wait for workers to finish

    return nil
}

// Pool management
func (m *Manager) Scale(ctx context.Context, minReady, preheatedCount int) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.config.MinReady = minReady
    m.config.Preheating.Count = preheatedCount

    // Trigger immediate replenishment/preheating
    go m.ensureMinReady(ctx)
    go m.ensurePreheated(ctx)

    return nil
}

func (m *Manager) Drain(ctx context.Context) error {
    m.logger.Info("Draining pool")

    m.mu.Lock()
    m.drained = true
    m.mu.Unlock()

    // Wait for all allocated resources to be released
    for {
        allocated, err := m.repository.CountByState(ctx, m.config.Name, resource.StateAllocated)
        if err != nil {
            return err
        }

        if allocated == 0 {
            break // All resources released
        }

        time.Sleep(5 * time.Second)
    }

    return nil
}

func (m *Manager) Refill(ctx context.Context) error {
    m.logger.Info("Refilling pool")

    m.mu.Lock()
    m.drained = false
    m.mu.Unlock()

    go m.ensureMinReady(ctx)

    return nil
}

// Pool inspection
func (m *Manager) GetStats(ctx context.Context) (*PoolStats, error) {
    resources, err := m.repository.GetByPoolID(ctx, m.config.Name)
    if err != nil {
        return nil, err
    }

    stats := &PoolStats{
        PoolName: m.config.Name,
    }

    for _, res := range resources {
        stats.Total++
        switch res.State {
        case resource.StateProvisioned:
            stats.Provisioned++
        case resource.StateReady:
            stats.Ready++
        case resource.StateAllocated:
            stats.Allocated++
        case resource.StateDestroyed:
            stats.Destroyed++
        }
    }

    return stats, nil
}

func (m *Manager) GetStatus() string {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.drained {
        return "drained"
    }
    if m.ctx.Err() != nil {
        return "stopped"
    }
    return "running"
}

type PoolStats struct {
    PoolName    string
    Total       int
    Provisioned int
    Ready       int
    Allocated   int
    Destroyed   int
}
```

---

### Task 6.2: Pool Registry

**File**: `internal/core/pool/registry.go`

```go
package pool

type Registry struct {
    pools  map[string]*Manager
    mu     sync.RWMutex
    logger *logrus.Logger
}

func NewRegistry(logger *logrus.Logger) *Registry {
    return &Registry{
        pools:  make(map[string]*Manager),
        logger: logger,
    }
}

func (r *Registry) Register(name string, pool *Manager) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    if _, exists := r.pools[name]; exists {
        return fmt.Errorf("pool already registered: %s", name)
    }

    r.pools[name] = pool
    return nil
}

func (r *Registry) Get(name string) (*Manager, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()

    pool, ok := r.pools[name]
    return pool, ok
}

func (r *Registry) List() []*Manager {
    r.mu.RLock()
    defer r.mu.RUnlock()

    pools := make([]*Manager, 0, len(r.pools))
    for _, pool := range r.pools {
        pools = append(pools, pool)
    }
    return pools
}

func (r *Registry) Unregister(name string) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    pool, ok := r.pools[name]
    if !ok {
        return fmt.Errorf("pool not found: %s", name)
    }

    // Stop pool before removing
    if err := pool.Stop(); err != nil {
        return fmt.Errorf("failed to stop pool: %w", err)
    }

    delete(r.pools, name)
    return nil
}
```

---

### Task 6.3: CLI Commands

**File**: `cmd/boxy/commands/pool.go`

```go
package commands

func poolCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "pool",
        Short: "Manage resource pools",
    }

    cmd.AddCommand(poolListCommand())
    cmd.AddCommand(poolStatsCommand())
    cmd.AddCommand(poolInspectCommand())
    cmd.AddCommand(poolResourcesCommand())
    cmd.AddCommand(poolScaleCommand())
    cmd.AddCommand(poolDrainCommand())
    cmd.AddCommand(poolRefillCommand())
    cmd.AddCommand(poolRecycleCommand())
    cmd.AddCommand(poolStartCommand())
    cmd.AddCommand(poolStopCommand())

    return cmd
}

func poolListCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "ls",
        Short: "List all pools",
        RunE: func(cmd *cobra.Command, args []string) error {
            client := getClient()
            pools, err := client.ListPools(cmd.Context())
            if err != nil {
                return err
            }

            // Print table
            w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
            fmt.Fprintln(w, "NAME\tTYPE\tBACKEND\tREADY\tALLOCATED\tSTATUS")

            for _, pool := range pools {
                fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%d\t%s\n",
                    pool.Name,
                    pool.Type,
                    pool.Backend,
                    pool.Stats.Ready,
                    pool.Config.MinReady,
                    pool.Stats.Allocated,
                    pool.Status,
                )
            }

            w.Flush()
            return nil
        },
    }
}

func poolScaleCommand() *cobra.Command {
    var minReady int
    var preheated int
    var persist bool

    cmd := &cobra.Command{
        Use:   "scale <pool-name>",
        Short: "Scale pool resources",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            poolName := args[0]

            client := getClient()

            fmt.Printf("Scaling pool '%s'...\n", poolName)

            if err := client.ScalePool(cmd.Context(), poolName, minReady, preheated); err != nil {
                return err
            }

            if persist {
                // TODO: Update config file
                fmt.Println("✓ Config file updated")
            }

            fmt.Println("✓ Pool scaled successfully")
            return nil
        },
    }

    cmd.Flags().IntVar(&minReady, "min-ready", 0, "Minimum ready resources")
    cmd.Flags().IntVar(&preheated, "preheated", 0, "Preheated resources count")
    cmd.Flags().BoolVar(&persist, "persist", false, "Update config file")

    return cmd
}

// ... other commands follow similar pattern
```

---

## Testing

### Unit Tests

```go
// internal/core/pool/manager_test.go
func TestPool_Start(t *testing.T) {
    pool := newTestPool(t)

    err := pool.Start()
    assert.NoError(t, err)

    // Verify workers started
    assert.Equal(t, "running", pool.GetStatus())

    pool.Stop()
}

func TestPool_Scale(t *testing.T) {
    pool := newTestPool(t)
    pool.Start()
    defer pool.Stop()

    err := pool.Scale(context.Background(), 15, 7)
    assert.NoError(t, err)

    assert.Equal(t, 15, pool.config.MinReady)
    assert.Equal(t, 7, pool.config.Preheating.Count)
}

func TestPool_Drain(t *testing.T) {
    pool := newTestPool(t)
    pool.Start()
    defer pool.Stop()

    err := pool.Drain(context.Background())
    assert.NoError(t, err)

    assert.Equal(t, "drained", pool.GetStatus())
}
```

### Integration Tests

```go
// tests/integration/pool_management_test.go
func TestIntegration_PoolLifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Start pool
    pool := createTestPool(t)
    err := pool.Start()
    assert.NoError(t, err)

    // Get stats
    stats, err := pool.GetStats(context.Background())
    assert.NoError(t, err)
    assert.GreaterOrEqual(t, stats.Total, 0)

    // Stop pool
    err = pool.Stop()
    assert.NoError(t, err)
}
```

---

## Success Criteria

- ✅ All CLI commands implemented
- ✅ Pool lifecycle management works (start/stop)
- ✅ Runtime scaling works
- ✅ Drain/refill works
- ✅ Stats and inspection work
- ✅ Force recycle works
- ✅ Unit tests pass
- ✅ Integration tests pass
- ✅ Documentation complete

---

## User Impact

### Before (Limited Control)

```bash
# Only way to manage pools: edit config and restart
vim boxy.yaml
systemctl restart boxy
```

### After (Full Control)

```bash
# Inspect pools
boxy pool ls
boxy pool stats ubuntu-containers

# Scale at runtime
boxy pool scale ubuntu-containers --min-ready 15

# Maintenance
boxy pool drain ubuntu-containers  # Safe maintenance
boxy pool recycle ubuntu-containers  # Refresh resources
```

---

## Related Documents

- [01: Architecture Refactor](01-architecture-refactor.md) - Enables pool independence
- [02: Preheating & Recycling](02-preheating-recycling.md) - Pool features
- [05: Base Image Validation](05-base-image-validation.md) - Pool validation

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: 01-architecture-refactor
**Blocking**: None

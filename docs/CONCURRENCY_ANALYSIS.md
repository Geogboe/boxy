# Boxy Concurrency Analysis

**Date**: 2025-11-21
**Analyzed by**: Claude (Sonnet 4.5)
**Purpose**: Verify concurrent performance patterns and identify optimization opportunities

## Executive Summary

The Boxy codebase has **good foundational concurrency** with goroutines, mutexes, and WaitGroups properly used. However, there are **critical performance bottlenecks** where operations that should be parallel are currently sequential. These bottlenecks occur in hot paths that directly impact user-perceived performance.

### Key Findings

✅ **Strengths**:
- Proper use of sync.RWMutex for read-heavy operations
- Background workers properly tracked with WaitGroups
- Panic recovery in all goroutines
- Context-based cancellation working correctly

⚠️ **Critical Issues** (Performance Bottlenecks):
1. Pool replenishment provisions resources **sequentially** (should be parallel)
2. Health checks run **sequentially** across all resources (should be parallel)
3. Sandbox resource allocation happens **sequentially** (should be parallel)
4. Sandbox cleanup destroys resources **sequentially** (should be parallel)
5. Pool allocation holds exclusive lock during hook execution (blocks other allocations)

**Performance Impact**: With current sequential provisioning, spinning up 10 VMs that each take 30 seconds would take **5 minutes**. With parallel provisioning, this could be done in **30 seconds** (10x improvement).

---

## Detailed Analysis

### 1. Pool Manager (`internal/core/pool/manager.go`)

#### 1.1 Background Workers ✅

**Lines 92-98, 360-416**: Two background workers properly implemented:

```go
// Start begins the warm pool maintenance goroutines
func (m *Manager) Start() error {
    // ...
    m.wg.Add(1)
    go m.replenishmentWorker()  // ✅ Background worker

    m.wg.Add(1)
    go m.healthCheckWorker()    // ✅ Background worker

    m.wg.Add(1)
    go func() {                 // ✅ Initial replenishment
        defer m.wg.Done()
        if err := m.ensureMinReady(m.ctx); err != nil {
            m.logger.WithError(err).Error("Initial replenishment failed")
        }
    }()
}
```

**Assessment**: ✅ Excellent
- Properly tracked in WaitGroup
- Panic recovery present
- Context-based cancellation
- Clean shutdown in Stop()

#### 1.2 Pool Replenishment ⚠️ CRITICAL BOTTLENECK

**Lines 418-480**: `ensureMinReady()` provisions resources **sequentially**:

```go
// ensureMinReady provisions resources until min_ready count is reached
func (m *Manager) ensureMinReady(ctx context.Context) error {
    m.mu.Lock()              // ⚠️ Holds exclusive lock during entire operation
    defer m.mu.Unlock()

    // ... calculation logic ...

    // Provision needed resources
    for i := 0; i < needed; i++ {
        if err := m.provisionOne(ctx); err != nil {  // ⚠️ SEQUENTIAL!
            m.logger.WithError(err).Error("Failed to provision resource")
        }
    }
    return nil
}
```

**Problem**:
- If `needed = 10` and each `provisionOne()` takes 30 seconds, this takes **5 minutes**
- Holds exclusive lock the entire time, blocking allocations
- No parallelism for expensive I/O operations

**Recommendation**: ⚠️ **HIGH PRIORITY**
```go
// Parallel provisioning approach
var wg sync.WaitGroup
semaphore := make(chan struct{}, 5) // Limit concurrency to 5 at a time

for i := 0; i < needed; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        semaphore <- struct{}{}
        defer func() { <-semaphore }()

        if err := m.provisionOne(ctx); err != nil {
            m.logger.WithError(err).Error("Failed to provision resource")
        }
    }()
}

wg.Wait()
```

**Performance Gain**: ~10x for provisioning 10 resources (5 minutes → 30 seconds)

#### 1.3 Async Replenishment Triggers ✅

**Lines 239-252, 302-315**: Async triggers after allocation/release:

```go
// Trigger replenishment asynchronously
go func() {
    defer func() {
        if r := recover(); r != nil {
            m.logger.WithFields(logrus.Fields{
                "pool":  m.config.Name,
                "panic": r,
            }).Error("Panic in async replenishment after allocation")
        }
    }()
    if err := m.ensureMinReady(m.ctx); err != nil {
        m.logger.WithError(err).Error("Failed to replenish pool after allocation")
    }
}()
```

**Assessment**: ✅ Good
- Non-blocking replenishment
- Proper panic recovery
- However, still suffers from sequential provisioning inside `ensureMinReady()`

#### 1.4 Health Checks ⚠️ BOTTLENECK

**Lines 595-638**: `performHealthChecks()` iterates **sequentially**:

```go
func (m *Manager) performHealthChecks(ctx context.Context) error {
    ready, err := m.repository.GetByState(ctx, m.config.Name, resource.StateReady)
    if err != nil {
        return err
    }

    for _, res := range ready {              // ⚠️ SEQUENTIAL!
        status, err := m.provider.GetStatus(ctx, res)  // ⚠️ Can be slow (network call)
        if err != nil {
            m.logger.WithFields(logrus.Fields{
                "resource_id": res.ID,
                "error":       err,
            }).Warn("Health check failed")
            continue
        }

        if !status.Healthy {
            // ... destroy unhealthy resource (async) ...
        }
    }
    return nil
}
```

**Problem**:
- If pool has 50 resources and each health check takes 1 second, this takes **50 seconds**
- Blocks the health check worker from processing other pools
- Network calls are prime candidates for parallelization

**Recommendation**: ⚠️ **MEDIUM PRIORITY**
```go
var wg sync.WaitGroup
for _, res := range ready {
    wg.Add(1)
    res := res  // Capture loop variable
    go func() {
        defer wg.Done()
        status, err := m.provider.GetStatus(ctx, res)
        // ... rest of logic ...
    }()
}
wg.Wait()
```

**Performance Gain**: ~50x for 50 resources (50 seconds → 1 second)

#### 1.5 Lock Contention ⚠️ ISSUE

**Lines 150-151, 420-421**: Exclusive lock held during expensive operations:

```go
// Allocate holds exclusive lock
func (m *Manager) Allocate(ctx context.Context, sandboxID string) (*resource.Resource, error) {
    m.mu.Lock()           // ⚠️ Exclusive lock
    defer m.mu.Unlock()

    // ... find available resource ...

    // Run before_allocate hooks (personalization) if any
    if len(m.config.Hooks.BeforeAllocate) > 0 {
        // ... hook execution can take 30+ seconds ...  ⚠️ Blocks other allocations!
    }

    // ... update resource ...
}
```

**Problem**:
- Personalization hooks can take 30+ seconds (installing software, configuring system)
- During this time, **all other allocations are blocked**
- With sequential hooks for one allocation, other users wait unnecessarily

**Recommendation**: ⚠️ **HIGH PRIORITY**
```go
// 1. Find and mark resource under lock (fast)
m.mu.Lock()
res := available[0]
res.State = resource.StateAllocating  // New intermediate state
m.mu.Unlock()

// 2. Run hooks WITHOUT holding lock (slow)
if len(m.config.Hooks.BeforeAllocate) > 0 {
    results, err := m.hookExecutor.ExecuteHooks(...)
    if err != nil {
        // Rollback: mark resource as ready again
        m.mu.Lock()
        res.State = resource.StateReady
        m.mu.Unlock()
        return nil, err
    }
}

// 3. Finalize allocation under lock (fast)
m.mu.Lock()
res.State = resource.StateAllocated
res.SandboxID = &sandboxID
m.repository.Update(ctx, res)
m.mu.Unlock()
```

**Performance Gain**: Allows concurrent allocations, ~10x throughput improvement

---

### 2. Sandbox Manager (`internal/core/sandbox/manager.go`)

#### 2.1 Background Cleanup Worker ✅

**Lines 74-81, 400-421**: Cleanup worker properly implemented:

```go
func (m *Manager) Start() {
    m.cleanupTicker = time.NewTicker(30 * time.Second)
    m.wg.Add(1)
    go m.cleanupWorker()  // ✅ Background worker
}

func (m *Manager) cleanupWorker() {
    defer m.wg.Done()
    defer func() {
        if r := recover(); r != nil {
            m.logger.WithFields(logrus.Fields{
                "panic": r,
            }).Error("Panic in sandbox cleanup worker, worker terminated")
        }
    }()

    for {
        select {
        case <-m.ctx.Done():
            return
        case <-m.cleanupTicker.C:
            if err := m.cleanupExpired(m.ctx); err != nil {
                m.logger.WithError(err).Warn("Cleanup of expired sandboxes failed")
            }
        }
    }
}
```

**Assessment**: ✅ Excellent
- Proper WaitGroup tracking
- Panic recovery
- Context cancellation

#### 2.2 Async Resource Allocation ⚠️ BOTTLENECK

**Lines 127, 182-250**: Sandbox creation spawns async goroutine, but allocates **sequentially**:

```go
// Create creates a new sandbox
func (m *Manager) Create(ctx context.Context, req *CreateRequest) (*Sandbox, error) {
    // ... validation ...

    // Allocate resources asynchronously
    go m.allocateResourcesAsync(sb.ID, req.Resources)  // ✅ Async

    return sb, nil
}

// allocateResourcesAsync allocates resources for a sandbox in background
func (m *Manager) allocateResourcesAsync(sandboxID string, resourceReqs []ResourceRequest) {
    defer func() { /* panic recovery */ }()

    ctx := context.Background()
    var allocatedIDs []string

    // Allocate resources from pools
    for _, resReq := range resourceReqs {              // ⚠️ Outer loop sequential
        pool, ok := m.pools[resReq.PoolName]
        if !ok {
            m.markSandboxError(sandboxID, fmt.Sprintf("pool not found: %s", resReq.PoolName), allocatedIDs)
            return
        }

        for i := 0; i < resReq.Count; i++ {           // ⚠️ Inner loop sequential
            res, err := pool.Allocate(ctx, sandboxID)  // ⚠️ SEQUENTIAL!
            if err != nil {
                m.logger.WithError(err).Errorf("Failed to allocate resource %d from pool %s", i+1, resReq.PoolName)
                m.markSandboxError(sandboxID, fmt.Sprintf("failed to allocate from pool %s: %v", resReq.PoolName, err), allocatedIDs)
                return
            }
            allocatedIDs = append(allocatedIDs, res.ID)
        }
    }

    // ... update sandbox ...
}
```

**Problem**:
- User requests: 3 Windows VMs + 2 Linux containers
- Current: Allocates sequentially (5 allocations one after another)
- If each allocation takes 30 seconds (including hooks), total time = **2.5 minutes**
- User sees "Creating..." state for 2.5 minutes

**Recommendation**: ⚠️ **HIGH PRIORITY**
```go
var wg sync.WaitGroup
var mu sync.Mutex
var allocatedIDs []string
var firstError error

for _, resReq := range resourceReqs {
    for i := 0; i < resReq.Count; i++ {
        wg.Add(1)
        go func(poolName string) {
            defer wg.Done()

            pool, ok := m.pools[poolName]
            if !ok {
                mu.Lock()
                if firstError == nil {
                    firstError = fmt.Errorf("pool not found: %s", poolName)
                }
                mu.Unlock()
                return
            }

            res, err := pool.Allocate(ctx, sandboxID)
            if err != nil {
                mu.Lock()
                if firstError == nil {
                    firstError = err
                }
                mu.Unlock()
                return
            }

            mu.Lock()
            allocatedIDs = append(allocatedIDs, res.ID)
            mu.Unlock()
        }(resReq.PoolName)
    }
}

wg.Wait()

if firstError != nil {
    m.markSandboxError(sandboxID, firstError.Error(), allocatedIDs)
    return
}
```

**Performance Gain**: ~5x for 5 resources (2.5 minutes → 30 seconds)

#### 2.3 Expired Sandbox Cleanup ⚠️ BOTTLENECK

**Lines 423-443**: `cleanupExpired()` destroys sandboxes **sequentially**:

```go
func (m *Manager) cleanupExpired(ctx context.Context) error {
    expired, err := m.sandboxRepo.GetExpiredSandboxes(ctx)
    if err != nil {
        return err
    }

    if len(expired) == 0 {
        return nil
    }

    m.logger.WithField("count", len(expired)).Info("Cleaning up expired sandboxes")

    for _, sb := range expired {                      // ⚠️ SEQUENTIAL!
        if err := m.Destroy(ctx, sb.ID); err != nil {  // ⚠️ Can take 30+ seconds per sandbox
            m.logger.WithError(err).WithField("sandbox_id", sb.ID).Error("Failed to destroy expired sandbox")
        }
    }

    return nil
}
```

**Problem**:
- If 10 sandboxes expire at once and each takes 10 seconds to destroy, cleanup takes **100 seconds**
- Next cleanup cycle delayed
- Resources not freed quickly

**Recommendation**: ⚠️ **MEDIUM PRIORITY**
```go
var wg sync.WaitGroup
for _, sb := range expired {
    wg.Add(1)
    sb := sb  // Capture loop variable
    go func() {
        defer wg.Done()
        if err := m.Destroy(ctx, sb.ID); err != nil {
            m.logger.WithError(err).WithField("sandbox_id", sb.ID).Error("Failed to destroy expired sandbox")
        }
    }()
}
wg.Wait()
```

**Performance Gain**: ~10x for 10 sandboxes (100 seconds → 10 seconds)

#### 2.4 Sandbox Destroy ⚠️ BOTTLENECK

**Lines 281-335**: `Destroy()` releases resources **sequentially**:

```go
func (m *Manager) Destroy(ctx context.Context, id string) error {
    // ... get resources ...

    // Release all resources
    var releaseErrors []error
    for _, res := range resources {              // ⚠️ SEQUENTIAL!
        pool, ok := m.pools[res.PoolID]
        if !ok {
            m.logger.WithField("pool_id", res.PoolID).Warn("Pool not found for resource release")
            continue
        }

        if err := pool.Release(ctx, res.ID); err != nil {  // ⚠️ Can take 10+ seconds per resource
            m.logger.WithError(err).WithField("resource_id", res.ID).Error("Failed to release resource")
            releaseErrors = append(releaseErrors, err)
        }
    }

    // ... mark destroyed ...
}
```

**Problem**:
- Sandbox with 5 resources, each takes 10 seconds to destroy = **50 seconds**
- User waiting for "destroy" command to complete

**Recommendation**: ⚠️ **MEDIUM PRIORITY**
```go
var wg sync.WaitGroup
var mu sync.Mutex
var releaseErrors []error

for _, res := range resources {
    wg.Add(1)
    res := res  // Capture loop variable
    go func() {
        defer wg.Done()

        pool, ok := m.pools[res.PoolID]
        if !ok {
            return
        }

        if err := pool.Release(ctx, res.ID); err != nil {
            mu.Lock()
            releaseErrors = append(releaseErrors, err)
            mu.Unlock()
        }
    }()
}

wg.Wait()
```

**Performance Gain**: ~5x for 5 resources (50 seconds → 10 seconds)

---

### 3. Hook Executor (`internal/hooks/executor.go`)

#### 3.1 Sequential Hook Execution ✅ (Intentional)

**Lines 27-98**: Hooks execute **sequentially** in order:

```go
func (e *Executor) ExecuteHooks(
    ctx context.Context,
    hooks []Hook,
    hookPoint HookPoint,
    prov provider.Provider,
    res *resource.Resource,
    hookCtx HookContext,
    phaseTimeout time.Duration,
) ([]HookResult, error) {
    // ...
    results := make([]HookResult, 0, len(hooks))

    for i, hook := range hooks {              // ✅ Sequential is correct
        e.logger.WithFields(logrus.Fields{
            "resource_id": res.ID,
            "hook_name":   hook.Name,
            "hook_index":  i + 1,
            "hook_total":  len(hooks),
        }).Info("Executing hook")

        result, err := e.executeHookWithRetry(phaseCtx, hook, prov, res, hookCtx)
        results = append(results, result)

        if err != nil && !hook.ContinueOnFailure {
            return results, fmt.Errorf("hook %s failed: %w", hook.Name, err)
        }

        // ...
    }

    return results, nil
}
```

**Assessment**: ✅ **Correct Design**
- Hooks often have dependencies (e.g., "install software" → "configure software")
- Sequential execution ensures ordering
- `ContinueOnFailure` flag provides flexibility
- Phase timeout prevents runaway hooks

**Recommendation**: ✅ No change needed
- Consider future enhancement: allow parallel execution for hooks marked as "independent"
- Not a bottleneck compared to other issues

---

### 4. Provider Implementations

#### 4.1 Docker Provider (`internal/provider/docker/docker.go`)

**All operations are synchronous**: ✅ Correct
- `Provision()`: Creates and starts container (lines 45-152)
- `Destroy()`: Removes container (lines 155-170)
- `GetStatus()`: Inspects container (lines 172-203)
- `Exec()`: Executes command (lines 316-367)

**Assessment**: ✅ **Correct Design**
- Provider interface is designed to be synchronous
- Concurrency handled at pool/sandbox manager level
- Clean, simple implementation

---

### 5. Channel Usage

#### 5.1 Current Usage 📊

**Minimal channel usage found**:

1. **Pool Manager** (line 39, 71):
   ```go
   stopChan chan struct{}  // Unbuffered, used for shutdown signaling
   ```

2. **Tests** (stress_test.go):
   ```go
   errors := make(chan error, numWorkers)  // Buffered
   done := make(chan struct{})             // Unbuffered
   results := make(chan result, numWorkers) // Buffered
   ```

3. **Serve Command** (cmd/boxy/commands/serve.go:132):
   ```go
   sigChan := make(chan os.Signal, 1)  // Buffered for signal handling
   ```

**Assessment**: ✅ Adequate
- Channels used appropriately for signaling
- No need for worker pools with channels (goroutines + WaitGroups work fine)
- Test channels properly buffered

**Recommendation**: ✅ No change needed
- Current pattern of "spawn goroutine + WaitGroup" is appropriate for the workload
- If adding work distribution in the future, consider channels

---

### 6. Mutex and Lock Contention

#### 6.1 Lock Analysis 🔒

**Pool Manager Locks**:
- Uses `sync.RWMutex` ✅ (good for read-heavy operations)
- `Allocate()`: Holds exclusive lock (lines 150-151)
- `ensureMinReady()`: Holds exclusive lock (lines 420-421)
- Lock held during expensive operations (hooks) ⚠️

**Provider Registry Locks** (pkg/provider/provider.go):
- Uses `sync.RWMutex` ✅
- `Register()`: Exclusive lock (lines 92-93)
- `Get()`, `List()`: Read lock (lines 99-100, 107-108)
- Properly designed for concurrent read access ✅

**Mock Provider Locks** (internal/provider/mock/mock.go):
- Uses `sync.Mutex` ✅
- Fine-grained locking per operation ✅
- No long-held locks ✅

**Assessment**: ✅ Good mutex usage overall, but:
- ⚠️ Pool manager holds exclusive lock too long during allocation hooks
- ⚠️ Pool manager holds exclusive lock during entire provisioning loop

---

### 7. Race Condition Analysis

#### 7.1 Potential Race Conditions 🔍

**Examined areas**:
1. ✅ Pool resource allocation - protected by mutex
2. ✅ Provider registry access - protected by RWMutex
3. ✅ Sandbox state updates - each operation atomic
4. ✅ Background worker shutdown - properly coordinated with WaitGroup + context

**Testing Evidence**:
- CI runs tests with `-race` flag (.github/workflows/ci.yml:43, 46)
- Stress tests verify concurrent access (tests/integration/stress_test.go)
- No race conditions detected in current implementation

**Assessment**: ✅ No race conditions detected

---

## Performance Impact Summary

### Current Sequential Operations

| Operation | Resources | Time per Resource | Total Time (Sequential) | Total Time (Parallel) | Speedup |
|-----------|-----------|-------------------|------------------------|---------------------|---------|
| Pool replenishment | 10 VMs | 30s | **5 minutes** | 30s | **10x** |
| Health checks | 50 resources | 1s | **50 seconds** | 1s | **50x** |
| Sandbox allocation | 5 resources | 30s | **2.5 minutes** | 30s | **5x** |
| Sandbox destroy | 5 resources | 10s | **50 seconds** | 10s | **5x** |
| Expired cleanup | 10 sandboxes | 10s | **100 seconds** | 10s | **10x** |

### User-Facing Impact

**Scenario**: User requests sandbox with 3 Windows VMs, each with personalization hooks

**Current Experience**:
1. Request submitted (instant)
2. Waiting for resources... (90+ seconds) ⏱️
3. Sandbox ready

**With Parallel Allocation**:
1. Request submitted (instant)
2. Waiting for resources... (30 seconds) ⚡
3. Sandbox ready

**Result**: 3x faster perceived performance

---

## Recommendations Priority Matrix

### 🔴 High Priority (Critical Path Performance)

1. **Parallel Pool Replenishment** (ensureMinReady)
   - File: `internal/core/pool/manager.go:418-480`
   - Impact: 10x faster pool warm-up
   - Complexity: Medium
   - Risk: Low (can limit concurrency with semaphore)

2. **Parallel Sandbox Resource Allocation** (allocateResourcesAsync)
   - File: `internal/core/sandbox/manager.go:182-250`
   - Impact: 5x faster sandbox creation
   - Complexity: Medium
   - Risk: Low (independent operations)

3. **Reduce Lock Hold Time During Allocation** (Allocate)
   - File: `internal/core/pool/manager.go:142-255`
   - Impact: 10x allocation throughput
   - Complexity: Medium
   - Risk: Medium (need new state: StateAllocating)

### 🟡 Medium Priority (Background Operations)

4. **Parallel Health Checks** (performHealthChecks)
   - File: `internal/core/pool/manager.go:595-638`
   - Impact: 50x faster health check cycles
   - Complexity: Low
   - Risk: Low (independent operations)

5. **Parallel Sandbox Destroy** (Destroy)
   - File: `internal/core/sandbox/manager.go:281-335`
   - Impact: 5x faster cleanup
   - Complexity: Low
   - Risk: Low (independent operations)

6. **Parallel Expired Cleanup** (cleanupExpired)
   - File: `internal/core/sandbox/manager.go:423-443`
   - Impact: 10x faster batch cleanup
   - Complexity: Low
   - Risk: Low (independent operations)

### 🟢 Low Priority (Future Enhancements)

7. **Buffered Work Channels** (optional)
   - Impact: More predictable resource usage
   - Complexity: High
   - Risk: Medium (architectural change)
   - Recommendation: Only if needed for rate limiting

8. **Parallel Independent Hooks** (optional)
   - Impact: Faster hook execution for independent hooks
   - Complexity: High
   - Risk: Medium (need dependency analysis)
   - Recommendation: Future enhancement

---

## Implementation Guidelines

### Pattern: Parallel with Semaphore (Controlled Concurrency)

Use when you want parallelism but need to limit concurrent operations:

```go
// Limit to 5 concurrent provisions
semaphore := make(chan struct{}, 5)
var wg sync.WaitGroup

for i := 0; i < needed; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()

        // Acquire semaphore
        semaphore <- struct{}{}
        defer func() { <-semaphore }()

        // Do expensive work
        if err := m.provisionOne(ctx); err != nil {
            m.logger.WithError(err).Error("Failed")
        }
    }()
}

wg.Wait()
```

**When to use**:
- I/O-bound operations (network, disk)
- Resource provisioning
- External API calls
- Health checks

**Benefits**:
- Prevents overwhelming external systems
- Predictable resource usage
- Graceful degradation

### Pattern: Full Parallelism (No Limits)

Use when operations are fast and independent:

```go
var wg sync.WaitGroup

for _, item := range items {
    wg.Add(1)
    item := item  // Capture loop variable!
    go func() {
        defer wg.Done()

        // Do work
        process(item)
    }()
}

wg.Wait()
```

**When to use**:
- CPU-bound operations
- In-memory processing
- Independent database queries (with connection pool)
- Cleanup operations

### Pattern: Parallel with Shared State

Use when goroutines need to collect results:

```go
var wg sync.WaitGroup
var mu sync.Mutex
var results []Result
var firstError error

for _, item := range items {
    wg.Add(1)
    item := item
    go func() {
        defer wg.Done()

        result, err := process(item)

        mu.Lock()
        if err != nil && firstError == nil {
            firstError = err
        }
        if err == nil {
            results = append(results, result)
        }
        mu.Unlock()
    }()
}

wg.Wait()

if firstError != nil {
    return nil, firstError
}
return results, nil
```

**When to use**:
- Collecting results from parallel operations
- Error handling with fail-fast
- Aggregating data

---

## Testing Recommendations

### 1. Add Concurrency Benchmarks

Create `internal/core/pool/manager_bench_test.go`:

```go
func BenchmarkEnsureMinReady_Sequential(b *testing.B) {
    // Current implementation
    for i := 0; i < b.N; i++ {
        manager.ensureMinReady(ctx)
    }
}

func BenchmarkEnsureMinReady_Parallel(b *testing.B) {
    // New parallel implementation
    for i := 0; i < b.N; i++ {
        manager.ensureMinReadyParallel(ctx)
    }
}
```

Run with:
```bash
go test -bench=BenchmarkEnsureMinReady -benchtime=10s -benchmem
```

### 2. Stress Test Parallel Operations

Already have good stress tests in `tests/integration/stress_test.go` ✅

Add specific tests for:
- Parallel provisioning under load
- Concurrent allocations during replenishment
- Health checks during allocation storms

### 3. Race Detection

CI already runs with `-race` ✅

Add to development workflow:
```bash
make test-race    # Already in Makefile line 93
```

---

## Conclusion

The Boxy codebase has **solid concurrency fundamentals** but suffers from **sequential execution in critical hot paths**. Implementing parallel provisioning, allocation, and cleanup will provide:

- **5-10x performance improvement** for user-facing operations
- **50x improvement** for background maintenance tasks
- **Better resource utilization** of CPU and network
- **Improved user experience** with faster sandbox creation

### Recommended Implementation Order

1. ✅ **Week 1**: Parallel pool replenishment (biggest impact)
2. ✅ **Week 1**: Parallel sandbox allocation (user-facing)
3. ✅ **Week 2**: Reduce lock contention during allocation
4. ✅ **Week 2**: Parallel health checks
5. ✅ **Week 3**: Parallel sandbox destroy and cleanup
6. ✅ **Week 3**: Performance benchmarks and tuning

### Key Metrics to Track

- Pool warm-up time (target: <30s for 10 resources)
- Sandbox creation time (target: <30s for 5 resources)
- Allocation throughput (target: 10+ concurrent allocations)
- Health check cycle time (target: <5s for 50 resources)

---

**Analysis complete.** All major concurrency patterns examined and optimization opportunities identified.

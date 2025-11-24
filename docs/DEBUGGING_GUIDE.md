# Boxy Debugging and Troubleshooting Guide

## Table of Contents

1. [Logging System](#logging-system)
2. [Current Logging Coverage](#current-logging-coverage)
3. [Debug Mode Usage](#debug-mode-usage)
4. [Troubleshooting Common Issues](#troubleshooting-common-issues)
5. [Inspecting System State](#inspecting-system-state)
6. [Hook Debugging](#hook-debugging)
7. [Provider-Specific Debugging](#provider-specific-debugging)
8. [Performance Debugging](#performance-debugging)
9. [Identified Gaps](#identified-gaps)
10. [Recommended Improvements](#recommended-improvements)

---

## Logging System

### Log Levels

Boxy uses **logrus** for structured logging with the following levels:

- `debug` - Detailed execution flow, command parameters, internal state changes
- `info` - Normal operational messages (default)
- `warn` - Warning conditions that don't prevent operation
- `error` - Error conditions that prevent specific operations

### Enabling Debug Logging

Set the log level via command-line flag:

```bash
# Debug mode - shows detailed execution flow
boxy --log-level debug serve

# Show debug output for pool operations
boxy --log-level debug pool list
boxy --log-level debug pool stats ubuntu-containers

# Debug sandbox creation
boxy --log-level debug sandbox create --pool ubuntu-containers:2 --duration 1h
```

### Environment Variables

Boxy supports environment variable configuration:

```bash
# Override storage location
export BOXY_STORAGE_PATH=/custom/path/boxy.db

# Override encryption key (base64-encoded 32-byte key)
export BOXY_ENCRYPTION_KEY="base64encodedkey..."

# Viper supports auto-env with BOXY_ prefix
export BOXY_LOGGING_LEVEL=debug
export BOXY_STORAGE_TYPE=sqlite
```

### Log Format

All logs include:

- **Timestamp** (full timestamp enabled)
- **Log Level**
- **Message**
- **Structured Fields** (context-specific data)

Example log output:

```text
INFO[2025-01-21T10:15:30-05:00] Starting pool manager  pool=ubuntu-containers min_ready=3 max_total=10
DEBUG[2025-01-21T10:15:31-05:00] Provisioning resource  pool=ubuntu-containers image=ubuntu:22.04
INFO[2025-01-21T10:15:35-05:00] Resource provisioned and ready  pool=ubuntu-containers resource_id=abc123
```

---

## Current Logging Coverage

### ✅ Well-Logged Components

#### **Commands Layer** (`cmd/boxy/commands/`)

- **File**: `root.go`
  - Log level initialization
  - Config loading errors

- **File**: `serve.go`
  - Service startup/shutdown (lines 38, 47, 58, 69, 75, 107, 124, 129, 136, 144, 150)
  - Pool manager initialization
  - Provider health checks
  - Docker daemon connectivity

- **File**: `pool.go`
  - Pool statistics errors (lines 70, 76)

- **File**: `sandbox.go`
  - Resource allocation errors (line 176, 220)

#### **Pool Manager** (`internal/core/pool/manager.go`)

- **Provisioning Flow** (30+ log points):
  - Pool start/stop (lines 84-88, 129, 138)
  - Resource allocation (lines 144-147, 233-237, 281-284)
  - Replenishment triggers (lines 464-469, 486-489)
  - Hook execution (lines 176-180, 526-530)
  - Resource state transitions (lines 579-582)

- **Health Checks**:
  - Failed health checks (lines 605-608)
  - Unhealthy resource detection (line 614)

- **Error Conditions**:
  - Pool at capacity warnings (line 459)
  - Provisioning failures (lines 506-511, 558-566)
  - Release failures (line 288)

- **Panic Recovery**:
  - All goroutines have panic recovery with logging (lines 105-110, 241-247, 304-310, 364-369, 393-398, 621-628)

#### **Sandbox Manager** (`internal/core/sandbox/manager.go`)

- **Lifecycle Operations** (20+ log points):
  - Sandbox creation (lines 75, 94, 105-109, 129, 246-249)
  - Sandbox destruction (lines 283, 310, 315, 329, 333)
  - Sandbox extension (lines 360-363)
  - Expired sandbox cleanup (line 434)

- **Resource Allocation**:
  - Async allocation errors (lines 220, 231)
  - Pool validation (line 213)

- **Error Handling**:
  - Connection info retrieval failures (lines 380, 387)

- **Panic Recovery**:
  - Async allocation panic recovery (lines 185-189)
  - Cleanup worker panic recovery (lines 404-408)

#### **Hook Executor** (`internal/hooks/executor.go`)

- **Hook Execution Flow** (10+ log points):
  - Hook batch execution (lines 39-42, 46-50, 92-95)
  - Individual hook execution (lines 59-64, 138-143, 204-209)
  - Hook failures (lines 70-74, 78-83, 147-152)

- **Retry Logic**:
  - Retry attempts (lines 118-123)

#### **Docker Provider** (`internal/provider/docker/docker.go`)

- **Resource Lifecycle** (8+ log points):
  - Provisioning (lines 46-49, 147-150)
  - Image pulling (lines 272, 287)
  - Container destruction (lines 157, 168)

- **Command Execution**:
  - Exec operations (lines 318-321, 361-364)

#### **Mock Provider** (`internal/provider/mock/mock.go`)

- Debug-level logging for:
  - Mock provisioning (line 102)
  - Mock destruction (line 124)

### ⚠️ Limited Logging

#### **Storage Layer** (`internal/storage/sqlite.go`)

- **ISSUE**: GORM logging set to **Silent** mode (line 23)
- No visibility into:
  - SQL queries
  - Database errors
  - Transaction issues
  - Connection pool problems

#### **Hyper-V Provider** (`internal/provider/hyperv/hyperv.go`)

- Stub implementation with minimal logging

---

## Debug Mode Usage

### Starting Service in Debug Mode

```bash
# Start service with debug logging
boxy --log-level debug serve

# Example output:
# DEBUG[...] Allocating resource from pool  pool=ubuntu-containers sandbox_id=sb-abc123
# DEBUG[...] Provisioning resource  pool=ubuntu-containers image=ubuntu:22.04
# DEBUG[...] Executing command in container  container_id=abc123 command=[/bin/bash -c echo 'test']
# INFO[...] Resource provisioned and ready  pool=ubuntu-containers resource_id=res-123
```

### Debugging Specific Operations

#### Pool Operations

```bash
# Debug pool provisioning
boxy --log-level debug pool list

# Debug pool statistics
boxy --log-level debug pool stats ubuntu-containers
```

#### Sandbox Operations

```bash
# Debug sandbox creation (shows allocation flow)
boxy --log-level debug sandbox create \
  --pool ubuntu-containers:2 \
  --duration 2h \
  --name debug-test

# Debug sandbox listing
boxy --log-level debug sandbox list

# Debug sandbox destruction
boxy --log-level debug sandbox destroy sb-abc123
```

### Interpreting Debug Logs

Key debug messages to look for:

1. **Resource Allocation**:

   ```text
   DEBUG[...] Allocating resource from pool  pool=X sandbox_id=Y
   ```

2. **Hook Execution**:

   ```text
   INFO[...] Executing hooks  resource_id=X hook_point=after_provision hook_count=2
   DEBUG[...] Executing command via provider.Exec()  resource_id=X hook_name=Y command=[...]
   ```

3. **Provider Operations**:

   ```text
   DEBUG[...] Executing command in container  container_id=X command=[/bin/bash -c ...]
   DEBUG[...] Command execution completed  container_id=X exit_code=0
   ```

---

## Troubleshooting Common Issues

### Issue 1: Pool Not Replenishing

**Symptoms**: `pool stats` shows `Ready: 0`, pool not replenishing

**Debug Steps**:

1. Enable debug logging:

   ```bash
   boxy --log-level debug serve
   ```

2. Look for errors in logs:
   - `"Failed to provision resource"` - Provider error
   - `"Pool at max capacity"` - Hit max_total limit
   - `"Docker health check failed"` - Docker daemon issue

3. Check pool configuration:

   ```bash
   boxy pool stats <pool-name>
   ```

4. Verify provider health:
   - Docker: `docker ps` (check Docker daemon)
   - Check provider-specific logs

**Common Causes**:

- Docker daemon not running
- Image pull failures (network issues, invalid image name)
- Resource limits exceeded (CPU, memory, disk)
- Hook failures blocking provisioning

### Issue 2: Sandbox Creation Stuck in "Creating"

**Symptoms**: Sandbox stays in `StateCreating`, never becomes ready

**Debug Steps**:

1. Check sandbox state:

   ```bash
   boxy sandbox list
   ```

2. Enable debug logging and watch allocation:

   ```bash
   boxy --log-level debug sandbox create --pool ubuntu-containers:1
   ```

3. Look for errors:
   - `"Failed to allocate resource"` - Pool exhausted
   - `"Pool not found"` - Invalid pool name
   - `"Personalization hooks failed"` - Hook execution error

4. Check database state:

   ```bash
   # Connect to SQLite database
   sqlite3 ~/.config/boxy/boxy.db

   # Check sandbox state
   SELECT id, name, state, created_at, expires_at FROM sandboxes;

   # Check resources
   SELECT id, pool_id, state, sandbox_id, created_at FROM resources;
   ```

**Common Causes**:

- No resources available in pool
- Hook execution failures
- Timeout during allocation
- Provider errors

### Issue 3: Hooks Failing

**Symptoms**: Resources stuck in provisioning, allocation fails

**Debug Steps**:

1. Enable debug logging:

   ```bash
   boxy --log-level debug serve
   ```

2. Watch for hook execution:

   ```text
   INFO[...] Executing hooks  hook_count=2 hook_point=after_provision
   INFO[...] Executing hook  hook_index=1 hook_name=validate-network
   ERROR[...] Hook failed (critical)  hook_name=validate-network error="execution failed..."
   ```

3. Check hook results in database (see [Hook Debugging](#hook-debugging))

4. Test hooks manually:

   ```bash
   # For Docker containers
   docker exec -it <container_id> /bin/bash
   # Run hook script manually
   ```

**Common Causes**:

- Script syntax errors
- Missing dependencies in container
- Network issues (for network validation hooks)
- Timeout (hook takes too long)
- Wrong shell type (bash vs powershell)

### Issue 4: Resource Leaks

**Symptoms**: Resources not being destroyed, disk space growing

**Debug Steps**:

1. Check active resources:

   ```bash
   # Count Docker containers
   docker ps -a --filter "label=boxy.managed=true" | wc -l
   ```

2. Check database vs actual resources:

   ```bash
   sqlite3 ~/.config/boxy/boxy.db
   SELECT state, COUNT(*) FROM resources GROUP BY state;
   ```

3. Enable debug logging for cleanup:

   ```bash
   boxy --log-level debug serve
   # Watch logs for:
   # - "Cleaning up expired sandboxes"
   # - "Failed to destroy expired sandbox"
   ```

4. Check provider-specific resources:

   ```bash
   # Docker
   docker ps -a --filter "label=boxy.managed=true"

   # Check for containers that should be destroyed
   ```

**Common Causes**:

- Provider.Destroy() failures
- Panic during cleanup
- Database inconsistency (state mismatch)
- Provider daemon issues

### Issue 5: Database Locked Errors

**Symptoms**: `"database is locked"` errors in logs

**Context**: SQLite uses file-based locking, single writer at a time

**Debug Steps**:

1. Check for concurrent operations:

   ```bash
   # Look for multiple boxy processes
   ps aux | grep boxy
   ```

2. Verify connection pool settings (internal/storage/sqlite.go:37-40):

   ```go
   sqlDB.SetMaxOpenConns(1)  // Single writer
   ```

3. Check database file permissions:

   ```bash
   ls -la ~/.config/boxy/boxy.db
   ```

**Solutions**:

- Ensure only one `boxy serve` instance running
- Check file permissions (should be 0644)
- Consider PostgreSQL for multi-writer scenarios

### Issue 6: Encrypted Passwords Not Decrypting

**Symptoms**: `"failed to decrypt password"` errors

**Debug Steps**:

1. Check encryption key:

   ```bash
   ls -la ~/.config/boxy/encryption.key
   ```

2. Verify key length:

   ```bash
   cat ~/.config/boxy/encryption.key | base64 -d | wc -c
   # Should output: 32
   ```

3. Check for key rotation issues:
   - If key file was regenerated, old resources won't decrypt

4. Enable debug logging:

   ```bash
   boxy --log-level debug sandbox create ...
   # Look for encryption/decryption errors
   ```

**Solutions**:

- Use `BOXY_ENCRYPTION_KEY` env var for consistent key
- Don't delete/regenerate encryption.key
- For key rotation, destroy all resources first

---

## Inspecting System State

### Database Inspection

Connect to SQLite database:

```bash
sqlite3 ~/.config/boxy/boxy.db
```

Useful queries:

```sql
-- View all sandboxes
SELECT id, name, state, created_at, expires_at
FROM sandboxes
ORDER BY created_at DESC;

-- View active resources by pool
SELECT pool_id, state, COUNT(*) as count
FROM resources
GROUP BY pool_id, state;

-- Find orphaned resources (no sandbox)
SELECT id, pool_id, state, provider_id
FROM resources
WHERE sandbox_id IS NULL AND state != 'ready';

-- Check resource metadata (includes hook results)
SELECT id, metadata
FROM resources
WHERE id = 'res-123';

-- Find expired sandboxes
SELECT id, name, expires_at
FROM sandboxes
WHERE expires_at < datetime('now') AND state != 'destroyed';

-- Resource state breakdown
SELECT state, COUNT(*) as count
FROM resources
GROUP BY state;
```

### Provider-Specific Inspection

#### Docker

```bash
# List all Boxy-managed containers
docker ps -a --filter "label=boxy.managed=true"

# Inspect a specific container
docker inspect <container_id>

# View container logs
docker logs <container_id>

# Check resource usage
docker stats --no-stream <container_id>

# Test connectivity
docker exec -it <container_id> /bin/bash
```

#### System Resources

```bash
# Check disk space (important for Docker)
df -h

# Check Docker disk usage
docker system df

# Check memory
free -h

# Check running processes
ps aux | grep boxy
```

### Config Inspection

View active configuration:

```bash
cat ~/.config/boxy/boxy.yaml
```

Check config validity:

```bash
# Try loading config (will show validation errors)
boxy --config ~/.config/boxy/boxy.yaml pool list
```

---

## Hook Debugging

### Hook Execution Flow

Hooks are executed at two lifecycle points:

1. **after_provision** - After `provider.Provision()`, during pool warming
2. **before_allocate** - Before allocating resource to user (personalization)

### Hook Results Storage

Hook execution results are stored in resource metadata:

```sql
-- View hook results for a resource
SELECT id, metadata FROM resources WHERE id = 'res-123';

-- Metadata contains:
-- {
--   "finalization_hooks": [
--     {
--       "hook_name": "validate-network",
--       "success": true,
--       "exit_code": 0,
--       "stdout": "...",
--       "stderr": "...",
--       "duration": "2.5s",
--       "attempt": 1
--     }
--   ],
--   "personalization_hooks": [...]
-- }
```

### Debugging Hook Execution

#### Enable Hook Debug Logging

```bash
boxy --log-level debug serve
```

Key log messages:

```text
INFO[...] Running finalization hooks  pool=X resource_id=Y
INFO[...] Executing hooks  resource_id=X hook_point=after_provision hook_count=2
INFO[...] Executing hook  resource_id=X hook_name=validate-network hook_index=1 hook_total=2
DEBUG[...] Executing command via provider.Exec()  resource_id=X hook_name=Y command=[/bin/bash -c ...]
DEBUG[...] Command execution completed  container_id=X exit_code=0
INFO[...] Hook executed successfully  resource_id=X hook_name=Y duration=2.1s exit_code=0
INFO[...] All hooks executed successfully  resource_id=X hook_point=after_provision
```

#### Test Hooks Manually

```bash
# Get container ID from resource
sqlite3 ~/.config/boxy/boxy.db "SELECT provider_id FROM resources WHERE id='res-123';"

# Connect to container
docker exec -it <container_id> /bin/bash

# Manually run hook script
echo 'your hook script here' | /bin/bash
```

#### Hook Retry Debugging

Hooks support retry with delay:

```text
INFO[...] Retrying hook  resource_id=X hook_name=Y attempt=2 max_attempts=4
WARN[...] Hook attempt failed  resource_id=X hook_name=Y attempt=2 error="..."
```

#### Hook Failure Modes

1. **Critical failure** (`continue_on_failure: false`):

   ```text
   ERROR[...] Hook failed (critical)  resource_id=X hook_name=Y error="..."
   ```

   - Resource marked as `StateError`
   - Provisioning fails
   - Resource destroyed

2. **Non-critical failure** (`continue_on_failure: true`):

   ```text
   WARN[...] Hook failed (non-critical, continuing)  resource_id=X hook_name=Y error="..."
   ```

   - Execution continues to next hook
   - Resource still becomes ready

#### Hook Timeout Debugging

Phase-level timeouts:

- **Provision**: `timeouts.provision` (default: 5 minutes)
- **Finalization**: `timeouts.finalization` (default: 10 minutes)
- **Personalization**: `timeouts.personalization` (default: 30 seconds)

Individual hook timeout:

```yaml
hooks:
  after_provision:
    - name: slow-operation
      timeout: 15m  # Override default
```

Timeout error:

```text
ERROR[...] phase timeout exceeded after executing 2/3 hooks
```

### Common Hook Issues

| Issue | Symptoms | Solution |
| ------- | ---------- | ---------- |
| Script syntax error | `exit_code: 127` or non-zero | Test script manually in container |
| Missing dependencies | `command not found` | Install deps in base image or hook |
| Network unreachable | Timeout or connection errors | Check container networking |
| Timeout | `context deadline exceeded` | Increase hook/phase timeout |
| Wrong shell | Script doesn't execute | Verify `shell:` matches script type |

---

## Provider-Specific Debugging

### Docker Provider

#### Provisioning Issues

Enable debug logging:

```bash
boxy --log-level debug serve
```

Look for:

```text
INFO[...] Provisioning Docker container  image=ubuntu:22.04 type=container
INFO[...] Pulling Docker image  image=ubuntu:22.04
INFO[...] Image pulled successfully  image=ubuntu:22.04
INFO[...] Container provisioned successfully  container_id=abc123 image=ubuntu:22.04
```

Common issues:

1. **Image pull failures**:

   ```text
   ERROR[...] failed to pull image: ...
   ```

   - Check image name/tag
   - Check network connectivity
   - Check Docker Hub rate limits

2. **Container start failures**:

   ```text
   ERROR[...] failed to start container: ...
   ```

   - Check resource limits (CPU, memory)
   - Check port conflicts
   - Inspect container: `docker inspect <container_id>`

3. **Exec failures**:

   ```text
   ERROR[...] failed to create exec: ...
   ```

   - Container may be stopped
   - Command may not exist in container

#### Docker Daemon Issues

Check Docker daemon:

```bash
# Test Docker connectivity
docker ps

# Check daemon logs
journalctl -u docker

# Restart daemon
sudo systemctl restart docker
```

Health check failures:

```text
WARN[...] Docker health check failed - Docker functionality may be limited
```

### Mock Provider (Testing)

The mock provider includes debug logging:

```text
DEBUG[...] Mock resource provisioned  resource_id=X
DEBUG[...] Mock resource destroyed  resource_id=X
```

Mock provider supports failure simulation:

```go
// Configure failure rate for testing
mockProvider.SetFailureRate(0.2) // 20% failure rate
mockProvider.SetShouldFailHealth(true) // Simulate unhealthy resources
```

### Hyper-V Provider (Stub)

Currently a stub implementation. No real provisioning occurs.

---

## Performance Debugging

### Slow Provisioning

**Diagnosis**:

1. Enable debug logging:

   ```bash
   boxy --log-level debug serve
   ```

2. Look for timing in logs:

   ```text
   INFO[...] Resource provisioned and ready  pool=X resource_id=Y
   ```

   - Check time between "Provisioning resource" and "ready"

3. Profile hook execution:
   - Hook results include `duration` field
   - Check metadata for slow hooks

**Common causes**:

- Slow image pulls (large images, slow network)
- Slow hooks (network validation, package installation)
- Provider overhead (VM boot time)

### High Memory Usage

**Diagnosis**:

```bash
# Check Go heap
ps aux | grep boxy

# Monitor Docker memory
docker stats --no-stream

# Check container memory limits
docker inspect <container_id> | grep Memory
```

### Database Performance

**Issue**: Database locked errors, slow queries

**Diagnosis**:

1. Check connection pool (should be 1 for SQLite):

   ```go
   // internal/storage/sqlite.go:38
   sqlDB.SetMaxOpenConns(1)
   ```

2. Enable GORM debug logging (currently disabled):

   ```go
   // internal/storage/sqlite.go:23
   Logger: logger.Default.LogMode(logger.Silent), // Change to logger.Info
   ```

3. Check database size:

   ```bash
   ls -lh ~/.config/boxy/boxy.db
   ```

---

## Identified Gaps

### Critical Gaps

1. **❌ No visibility into SQL queries**
   - **File**: `internal/storage/sqlite.go:23`
   - GORM logging set to Silent mode
   - Can't debug database errors or performance issues
   - **Impact**: High - blind to storage layer issues

2. **❌ No CLI command to view hook results**
   - Hook results stored in metadata but not exposed
   - Must query database directly to see stdout/stderr
   - **Impact**: High - can't debug hook failures easily

3. **❌ No resource inspection command**
   - Can't view detailed resource state from CLI
   - Must use database queries
   - **Impact**: Medium - limits troubleshooting

4. **❌ No sandbox inspection command**
   - `sandbox list` shows basic info only
   - Can't see resource details, hook results, metadata
   - **Impact**: Medium - limits debugging

### Moderate Gaps

1. **⚠️ No verbose/trace logging level**
   - Only debug/info/warn/error
   - Can't enable extra-verbose output
   - **Impact**: Medium - some operations lack detail

2. **⚠️ No metrics/observability**
   - No Prometheus metrics
   - No health check endpoint
   - No performance counters
   - **Impact**: Medium - hard to monitor in production

3. **⚠️ Limited error context in some paths**
   - Some errors don't include full context
   - Example: storage errors don't always include query details
   - **Impact**: Low-Medium

4. **⚠️ No profiling support**
   - No pprof HTTP endpoint
   - Can't profile CPU/memory in production
   - **Impact**: Low - mainly for performance tuning

### Minor Gaps

1. **⚠️ No command to validate config**
   - Must run command to check config validity
   - **Impact**: Low

2. **⚠️ No audit log**
    - Who requested what resource?
    - No record of user actions
    - **Impact**: Low-Medium (important for multi-user)

---

## Recommended Improvements

### Priority 1: Critical for Maintenance

#### 1.1 Enable Database Debug Logging (Conditional)

**File**: `internal/storage/sqlite.go`

Add environment variable to enable GORM logging:

```go
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
    dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, err
    }
    // ...
}
```

**Usage**:

```bash
BOXY_DEBUG_SQL=1 boxy serve
```

#### 1.2 Add Resource Inspect Command

**File**: New `cmd/boxy/commands/resource.go`

```bash
boxy resource inspect <resource-id>

# Output:
# Resource: res-abc123
# ├─ Pool: ubuntu-containers
# ├─ State: allocated
# ├─ Sandbox: sb-xyz789
# ├─ Provider: docker (container-abc123)
# ├─ Created: 2025-01-21 10:15:30
# └─ Metadata:
#    ├─ Hook Results (finalization):
#    │  └─ validate-network: ✓ success (2.1s)
#    └─ Hook Results (personalization):
#       └─ create-user: ✓ success (0.5s)
#          stdout: Creating user boxy-user...
```

#### 1.3 Add Sandbox Inspect Command

**File**: `cmd/boxy/commands/sandbox.go`

```bash
boxy sandbox inspect <sandbox-id>

# Output:
# Sandbox: sb-xyz789
# ├─ Name: my-lab
# ├─ State: ready
# ├─ Created: 2025-01-21 10:15:00
# ├─ Expires: 2025-01-21 12:15:00 (in 1h 30m)
# └─ Resources (2):
#    ├─ [1] res-abc123 (ubuntu-containers)
#    │   └─ Connection: docker exec -it abc123 /bin/bash
#    └─ [2] res-def456 (ubuntu-containers)
#        └─ Connection: docker exec -it def456 /bin/bash
```

### Priority 2: Improve Debuggability

#### 2.1 Add Hook Output Viewing

**File**: `cmd/boxy/commands/hooks.go`

```bash
boxy hooks inspect <resource-id>

# Output:
# Hook Results for Resource: res-abc123
#
# Finalization Hooks (after_provision):
# ├─ [1] validate-network: ✓ success (2.1s, exit: 0)
# │   stdout: Validating network: 172.17.0.2
# │          Network is reachable
# └─ [2] setup-monitoring: ✓ success (1.5s, exit: 0)
#     stdout: Setting up monitoring for res-abc123
#
# Personalization Hooks (before_allocate):
# └─ [1] create-user: ✓ success (0.5s, exit: 0)
#     stdout: Creating user boxy-user with password...
#            User created successfully
```

#### 2.2 Add Trace Logging Level

**File**: `cmd/boxy/commands/root.go`

```go
rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info",
    "log level (trace, debug, info, warn, error)")
```

Trace level would include:

- All function entry/exit points
- Variable values
- Detailed state transitions

#### 2.3 Add Config Validation Command

**File**: `cmd/boxy/commands/config.go`

```bash
boxy config validate [--config path]

# Output:
# ✓ Configuration valid
#
# Pools (2):
# ├─ ubuntu-containers
# │  ├─ Type: container
# │  ├─ Backend: docker
# │  ├─ Image: ubuntu:22.04
# │  ├─ Min Ready: 3
# │  ├─ Max Total: 10
# │  └─ Hooks: 2 after_provision, 1 before_allocate
# └─ nginx-containers
#    └─ ...
```

### Priority 3: Observability & Monitoring

#### 3.1 Add Metrics Endpoint

**File**: New `internal/metrics/prometheus.go`

Expose Prometheus metrics:

```text
# Pool metrics
boxy_pool_resources_total{pool="ubuntu-containers",state="ready"} 5
boxy_pool_resources_total{pool="ubuntu-containers",state="allocated"} 2

# Sandbox metrics
boxy_sandboxes_total{state="ready"} 10
boxy_sandboxes_total{state="creating"} 2

# Hook metrics
boxy_hook_executions_total{hook="validate-network",result="success"} 50
boxy_hook_execution_duration_seconds{hook="validate-network"} 2.1
```

#### 3.2 Add Health Check Endpoint

**File**: New `cmd/boxy/commands/health.go`

```bash
boxy health

# Output:
# Boxy Health Check
# ├─ Service: ✓ running
# ├─ Database: ✓ connected (boxy.db, 2.5MB)
# ├─ Providers:
# │  └─ docker: ✓ healthy
# └─ Pools (2):
#    ├─ ubuntu-containers: ✓ healthy (5 ready / 3 min)
#    └─ nginx-containers: ⚠ low (1 ready / 3 min)
```

#### 3.3 Add Performance Profiling

**File**: `cmd/boxy/commands/serve.go`

Add optional pprof HTTP server:

```bash
boxy serve --pprof-addr :6060
```

Access at:

```text
http://localhost:6060/debug/pprof/
```

### Priority 4: Production Operations

#### 4.1 Add Audit Logging

**File**: New `internal/audit/logger.go`

Log all user actions:

```text
[AUDIT] user=admin action=sandbox.create sandbox_id=sb-123 pools=[ubuntu:2]
[AUDIT] user=admin action=sandbox.destroy sandbox_id=sb-123
[AUDIT] user=operator action=pool.scale pool=ubuntu min_ready=5
```

Store in separate audit.log file or database table.

#### 4.2 Add Resource Leak Detection

**File**: `cmd/boxy/commands/doctor.go`

```bash
boxy doctor

# Output:
# Boxy System Check
#
# ✓ No orphaned resources
# ⚠ Found 2 expired sandboxes (will be cleaned up)
# ✗ Found 3 Docker containers without database records
#   - container-abc123 (created 2 days ago)
#   - container-def456 (created 1 day ago)
#   - container-ghi789 (created 3 hours ago)
#
#   Run: boxy doctor --fix
```

#### 4.3 Add Backup/Restore Commands

**File**: `cmd/boxy/commands/backup.go`

```bash
# Backup database and config
boxy backup --output boxy-backup-2025-01-21.tar.gz

# Restore from backup
boxy restore boxy-backup-2025-01-21.tar.gz
```

---

## Quick Reference: Debugging Checklist

When troubleshooting an issue:

- [ ] Enable debug logging: `--log-level debug`
- [ ] Check service logs for errors/warnings
- [ ] Verify provider health (Docker, etc.)
- [ ] Inspect database state (`sqlite3 boxy.db`)
- [ ] Check resource states (`SELECT * FROM resources`)
- [ ] View hook results in metadata
- [ ] Test provider directly (e.g., `docker ps`)
- [ ] Check disk space and memory
- [ ] Review configuration for errors
- [ ] Check for orphaned resources

Common commands:

```bash
# Enable debug
boxy --log-level debug serve

# Check pool status
boxy pool stats <pool-name>

# Check sandboxes
boxy sandbox list

# Database inspection
sqlite3 ~/.config/boxy/boxy.db

# Provider inspection
docker ps -a --filter "label=boxy.managed=true"
```

---

## Conclusion

Boxy has a **solid foundation** for debugging with:

- ✅ Comprehensive structured logging via logrus
- ✅ Configurable log levels
- ✅ Good error wrapping and context
- ✅ Panic recovery in all goroutines
- ✅ Hook result storage in metadata

However, there are **key gaps** that should be addressed:

1. **Database visibility** (GORM silent logging)
2. **CLI inspection tools** (resource/sandbox inspect)
3. **Hook debugging** (no way to view hook output)
4. **Observability** (no metrics, health checks)

Implementing the Priority 1 recommendations would **significantly improve** maintainability and troubleshooting capabilities.

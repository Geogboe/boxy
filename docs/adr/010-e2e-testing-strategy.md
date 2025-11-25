# ADR-010: End-to-End Testing Strategy

**Status:** Proposed
**Date:** 2025-11-25
**Author:** Claude Code

## Context

Boxy currently has unit tests for individual components, but lacks end-to-end (E2E) tests that validate complete workflows. We need to establish a testing strategy that:

1. Tests real user workflows (CLI commands, API calls)
2. Validates full system integration (pool → sandbox → resource lifecycle)
3. Runs fast enough for CI/CD
4. Provides high confidence in system behavior
5. Is maintainable and easy to extend

The scratch/shell provider is now stable, making it ideal for E2E testing since it requires no external dependencies (no Docker, VMs, etc.).

## Decision

We will implement E2E tests with the following approach:

### 1. Test Framework Structure

```
tests/
  e2e/
    framework/        # Test utilities and harness
      harness.go      # Server lifecycle management
      helpers.go      # Common test operations
      assertions.go   # Custom assertions

    sandbox_lifecycle_test.go
    pool_management_test.go
    concurrent_test.go
    expiration_test.go
    hooks_test.go

    testdata/
      configs/        # Test configuration files
      scripts/        # Test hook scripts
```

### 2. Test Execution Mode: In-Process

**Choice:** Run boxy server in-process (not as external binary)

**Rationale:**
- Faster test execution (no process spawning)
- Better error messages and stack traces
- Easier debugging (can set breakpoints)
- Direct access to internal state for assertions
- Can still test CLI by calling command functions directly

**Trade-off:** Doesn't test actual binary distribution
- **Mitigation:** Add separate smoke tests that spawn binary

### 3. Test Isolation: Per-Test Database

**Choice:** Each test gets its own SQLite database

**Approach:**
```go
func setupTest(t *testing.T) *TestHarness {
    tempDir := t.TempDir() // Auto-cleanup
    dbPath := filepath.Join(tempDir, "test.db")

    cfg := &config.Config{
        Storage: config.StorageConfig{
            Type: "sqlite",
            Path: dbPath,
        },
        // ... test pools
    }

    return NewHarness(t, cfg)
}
```

**Rationale:**
- Complete isolation between tests
- Tests can run in parallel
- No state leakage
- Automatic cleanup via `t.TempDir()`

### 4. Test Data Management

**Workspace Cleanup:**
- Use `t.TempDir()` for scratch provider base directories
- Each test gets isolated workspace location
- Automatic cleanup after test completes

**Sample Configs:**
- Store in `tests/e2e/testdata/configs/`
- Minimal configs focused on test scenario
- Use scratch provider exclusively for speed

### 5. Test Scenarios

**Priority 1 (MVP):**
1. **Sandbox Lifecycle** - Create → Use → Destroy
2. **Pool Replenishment** - Verify min_ready maintained
3. **Concurrent Operations** - Multiple sandboxes simultaneously
4. **Expiration** - TTL-based cleanup
5. **Hook Execution** - on_provision and on_allocate hooks

**Priority 2 (Post-MVP):**
6. Error handling and recovery
7. API endpoint testing
8. Resource health checks
9. Pool scaling
10. Database persistence

### 6. CI/CD Integration

**GitHub Actions Workflow:**
```yaml
name: E2E Tests
on: [push, pull_request]

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Run E2E Tests
        run: go test -v ./tests/e2e/...
        timeout-minutes: 10
```

**Test Requirements:**
- Must complete in < 5 minutes
- Must be deterministic (no flaky tests)
- Must clean up resources on failure

### 7. Test Assertions

**Custom Assertions:**
```go
// framework/assertions.go
func AssertSandboxReady(t *testing.T, h *Harness, sandboxID string) {
    sb, err := h.GetSandbox(sandboxID)
    require.NoError(t, err)
    assert.Equal(t, sandbox.StateReady, sb.State)
}

func AssertConnectScriptExists(t *testing.T, h *Harness, sandboxID string) {
    resources := h.GetSandboxResources(sandboxID)
    for _, res := range resources {
        connInfo := h.GetConnectionInfo(res.ID)
        scriptPath := connInfo.ExtraFields["connect_script"].(string)
        assert.FileExists(t, scriptPath)
    }
}
```

## Consequences

### Positive
- **Fast Tests:** In-process execution, no network overhead
- **Reliable:** Scratch provider has no external dependencies
- **Maintainable:** Clear structure, reusable helpers
- **Parallel:** Per-test isolation enables concurrent execution
- **Debuggable:** Can step through code in debugger

### Negative
- **Limited Coverage:** Doesn't test binary distribution
  - *Mitigation:* Add separate smoke test suite
- **In-Process Only:** Might miss process-level issues
  - *Mitigation:* Production testing will catch these
- **SQLite Only:** Doesn't test PostgreSQL backend
  - *Mitigation:* Add database-specific tests later if needed

### Neutral
- **Additional Code:** Framework code adds maintenance burden
- **Test Data:** Need to maintain test configs and scripts

## Implementation Plan

### Phase 1: Framework (Week 1)
1. Create test harness for server lifecycle
2. Add helper functions for common operations
3. Create sample test configs

### Phase 2: Core Tests (Week 1-2)
4. Sandbox lifecycle test
5. Pool replenishment test
6. Basic concurrency test

### Phase 3: Advanced Tests (Week 2)
7. Expiration test
8. Hook execution tests
9. Error handling tests

### Phase 4: CI Integration (Week 2)
10. GitHub Actions workflow
11. Test coverage reporting
12. Badge in README

## Alternatives Considered

### Alternative 1: CLI Binary Testing
**Approach:** Spawn `boxy` binary, interact via CLI

**Rejected Because:**
- Slower (process spawning overhead)
- Harder to debug (no stack traces)
- More complex test harness
- Still need in-process tests for unit-level validation

**Keep Option Open For:** Smoke tests of actual distribution

### Alternative 2: Docker Compose Environment
**Approach:** Full environment with Docker, PostgreSQL, etc.

**Rejected Because:**
- Requires Docker (breaks simplicity)
- Much slower
- More complex setup
- Overkill for current needs

**Revisit When:** Adding Docker provider or PostgreSQL backend

### Alternative 3: Shared Test Database
**Approach:** All tests use same database, clean between tests

**Rejected Because:**
- Risk of state leakage
- Can't run tests in parallel
- Cleanup failures affect other tests
- More complex test orchestration

## References

- [Go Testing Best Practices](https://go.dev/doc/tutorial/add-a-test)
- [Testify Documentation](https://github.com/stretchr/testify)
- ADR-007: Scratch Provider Design (stable provider for testing)

## Open Questions

1. **Should we test API endpoints separately or through CLI?**
   - Recommendation: CLI tests cover API implicitly, add dedicated API tests later

2. **How to handle long-running operations (expiration tests)?**
   - Recommendation: Use short TTLs (1-5 seconds) in tests, not realistic durations

3. **Should tests exercise error paths extensively?**
   - Recommendation: Yes, but in separate test files (`error_handling_test.go`)

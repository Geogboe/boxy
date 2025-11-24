# Testing Strategy for Boxy

## Overview

Boxy employs a comprehensive, multi-layered testing strategy covering unit, integration, end-to-end (E2E), stress, and edge-case testing to ensure its reliability, performance, and correctness in production. Our philosophy emphasizes Test-Driven Development (TDD) and continuous testing throughout the development lifecycle.

## Philosophy

**Unit tests should be pure logic tests with no external dependencies. Integration tests should test real interactions with external systems.** We test incrementally as features are built, not just at the end, using mocks and stubs to isolate components effectively.

## Testing Pyramid

To guide our testing efforts, we adhere to the Testing Pyramid model:

```text
        ┌─────────────┐
        │  Manual     │  Real Hyper-V on Windows (Final Validation)
        │  Testing    │
        └─────────────┘
       ┌───────────────┐
       │  E2E Tests    │  Full workflows, often with stubs/mocks for complex dependencies
       │               │
       └───────────────┘
      ┌─────────────────┐
      │ Integration     │  Component interactions with real external systems (e.g., Docker, DB)
      │ Tests           │
      └─────────────────┘
     ┌───────────────────┐
     │  Unit Tests       │  Isolated components, pure logic
     │                   │
     └───────────────────┘
```

## Testing Levels

### 1. Unit Tests

**Goal**: Test individual components in isolation.
**Target**: > 80% Coverage for business logic.

**Scope**:

- Domain models (Resource, Pool, Sandbox)
- Business logic validation
- State transitions
- Helper functions

**Tools**:

- `testing` (Go stdlib)
- `testify/assert` for assertions
- `testify/require` for critical checks
- `testify/mock` for mocking (used sparingly for simple cases)

**Location**: `*_test.go` files alongside source.

**Mocking Strategy**:

- Use interfaces for dependencies to enable mocking.
- Create simple, custom mock implementations rather than relying heavily on complex mocking frameworks.

**Example**:

```go
func TestResource_IsAvailable(t *testing.T) {
    res := resource.NewResource("pool1", resource.ResourceTypeContainer, "docker")
    assert.True(t, res.State == resource.StateProvisioning)

    res.State = resource.StateReady
    assert.True(t, res.IsAvailable())

    sandboxID := "sb-123"
    res.SandboxID = &sandboxID
    assert.False(t, res.IsAvailable())
}
```

### 2. Integration Tests

**Goal**: Test component interactions with real dependencies.
**Target**: All major workflows.

**Scope**:

- Pool manager + Storage
- Sandbox manager + Pool allocators
- Provider + Docker SDK (requires Docker)
- Configuration loading
- Database operations

**Primary Provider**: Docker (used for Linux CI).
**Stubbed Provider**: Hyper-V (simulated for testing on Linux CI).

**Tools**:

- Docker-in-Docker for isolated testing.
- Testcontainers for dependencies.
- In-memory SQLite for fast database tests.

**Location**: `tests/integration/` or `*_integration_test.go` files (if specific to a package).

**Running Integration Tests**:
Integration tests are typically slower and platform-specific. They are often skipped during `go test -short`.

```bash
# Run all integration tests (Go's default will pick them up)
go test ./... -v

# Run specific integration tests (e.g., those tagged with 'integration')
# This project uses testing.Short() for skipping.
# Example: go test ./tests/integration/... -v
```

**Example**:

```go
func TestPoolManager_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    // Setup
    store := setupTestStore(t) // Helper to get a test DB
    provider := docker.NewProvider(logger)
    manager := pool.NewManager(config, provider, store, logger)

    // Test
    manager.Start()
    defer manager.Stop()

    stats, err := manager.GetStats(ctx)
    require.NoError(t, err)
    assert.GreaterOrEqual(t, stats.TotalReady, config.MinReady)
}
```

### 3. End-to-End (E2E) Tests

**Goal**: Test full user workflows.
**Target**: All documented use cases.

**Scope**:

- `boxy init` → `boxy serve` → `boxy sandbox create` → cleanup.
- Multi-pool sandboxes.
- Expiration and auto-cleanup.
- Error recovery (e.g., Docker daemon down).

**Uses**: Real Boxy service (often run in background), real providers (Docker), stubbed providers (Hyper-V).

**Location**: `tests/e2e/`.

**Running E2E Tests**:
E2E tests are the slowest and typically run after unit and integration tests have passed.

```bash
# Run all e2e tests
go test ./tests/e2e/... -v
```

**Example**:

```go
func TestE2E_CreateSandbox(t *testing.T) {
    // Start service in background
    cmd := exec.Command("./boxy", "serve")
    err := cmd.Start()
    require.NoError(t, err)
    defer cmd.Process.Kill()

    // ... (wait for service) ...

    // Create sandbox via CLI
    out, err := exec.Command("./boxy", "sandbox", "create",
        "-p", "test-pool:1", "-d", "5m").CombinedOutput()
    require.NoError(t, err)
    assert.Contains(t, string(out), "Sandbox created successfully")
}
```

### 4. Stress Tests

**Goal**: Verify behavior under load and identify performance bottlenecks.

**Scope**:

- Concurrent sandbox creation (100+ sandboxes).
- Pool exhaustion scenarios.
- Rapid allocation/deallocation.
- Memory and goroutine leaks (using `go test -race`).

**Tools**:

- `go test -race` for race detection.
- `pprof` for profiling.
- Custom load generators.

**Location**: `tests/stress/`.

### 5. Edge Case & Error Tests

**Goal**: Ensure graceful handling of unexpected situations and failures.

**Scope**:

- Docker daemon down, database corruption.
- Configuration errors, resource limits exceeded.
- Network failures, partial failures during sandbox creation/cleanup.

**Location**: Throughout unit, integration, and E2E test files.

## Stub/Mock Strategy for External Dependencies

### Hyper-V Stub for Linux Testing

**Problem**: Hyper-V only runs on Windows, but CI often runs on Linux.
**Solution**: Use a `StubHyperVProvider` that simulates Hyper-V behavior. This stub provides configurable latency and failure rates to mimic real-world scenarios.

```go
// pkg/provider/stub/hyperv_stub.go (Illustrative example)
package stub

type StubHyperVProvider struct {
    // ... fields to control behavior like latency, failureRate
}

func NewStubHyperVProvider(latency time.Duration) *StubHyperVProvider { /* ... */ }

func (s *StubHyperVProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Simulate realistic provision time and potential failures
    time.Sleep(s.latency)
    // ... logic to create a stubbed resource
}
// ... other Provider methods implemented realistically for testing
```

**Usage in Tests**:

```go
// Use stub in integration tests or E2E tests for Linux CI
stubProvider := stub.NewStubHyperVProvider(10 * time.Second)
pool := pool.NewManager(poolConfig, stubProvider, repo, logger)
```

## Smoke Tests

**Definition**: Minimal sanity checks to confirm core functionality after a change.
**Run**: After every commit, before pushing.

```bash
# Example smoke test script
#!/bin/bash
set -e

echo "Running smoke tests..."

# 1. Build
go build ./cmd/boxy

# 2. Unit tests (fast)
go test ./internal/... -short

# 3. Basic E2E (quick run of a few essential E2E tests)
go test ./tests/e2e/... -run TestE2E_Basic -short

echo "✅ Smoke tests passed"
```

## Regression Tests

**Goal**: Ensure no functionality is lost or broken by new changes, especially for existing features.
**Strategy**:

- Keep all existing E2E tests from previous milestones (e.g., MVP).
- Run these tests with new architecture and features.
- All regression tests must pass without modification.

```go
// tests/e2e/mvp_regression_test.go (Example)
func TestRegression_SandboxCreate(t *testing.T) {
    // Original MVP test - must still pass
}
```

## Test Organization

```text
boxy/
├── internal/              # Internal business logic and components
│   ├── core/
│   │   ├── pool/
│   │   │   ├── manager.go
│   │   │   ├── manager_test.go        # Unit tests
│   │   └── sandbox/
│   │       └── manager_test.go
│   ├── provider/
│   │   └── mock/                      # Generic mock provider for unit testing
│   │   └── stub/hyperv_stub.go        # Hyper-V specific stub for Linux CI
│   └── storage/
│       └── sqlite_test.go
├── tests/
│   ├── integration/         # Tests for component interactions with real dependencies
│   │   ├── allocator_test.go
│   │   ├── preheating_test.go
│   │   ├── multitenancy_test.go
│   │   └── distributed_agent_test.go
│   ├── e2e/                 # Full user workflow tests
│   │   ├── quick_testing_usecase_test.go
│   │   ├── ci_runner_usecase_test.go
│   │   └── mvp_regression_test.go
│   ├── stress/              # Performance and concurrency tests
│   │   └── concurrent_test.go
│   └── fixtures/            # Test data (configs, certs, images)
│       └── configs/
│       └── certs/
│       └── images/
└── Makefile                 # Test runners
```

## Test Commands (via Makefile)

```makefile
# Makefile
.PHONY: test test-unit test-integration test-e2e test-stress test-all

test-unit:
 go test -v -short ./...

test-integration:
 go test -v ./tests/integration/...

test-e2e:
 go test -v -timeout 10m ./tests/e2e/...

test-stress:
 go test -v -timeout 30m ./tests/stress/...

test-race:
 go test -race -short ./...

test-coverage:
 go test -coverprofile=coverage.out ./...
 go tool cover -html=coverage.out -o coverage.html

test-all: test-unit test-integration test-e2e
 @echo "All tests passed!"

bench:
 go test -bench=. -benchmem ./...
```

## Continuous Integration (GitHub Actions)

```yaml
# .github/workflows/test.yml
name: Tests

on: [push, pull_request]

jobs:
  unit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - name: Unit tests
        run: make test-unit
      - name: Race detection
        run: make test-race
      - name: Upload coverage
        uses: codecov/codecov-action@v3

  integration:
    runs-on: ubuntu-latest
    services:
      docker:
        image: docker:dind # Docker-in-Docker for isolated integration testing
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Integration tests
        run: make test-integration

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Build Boxy CLI
        run: go build -o boxy ./cmd/boxy
      - name: E2E tests
        run: make test-e2e
```

## Test Data Management

- Use `t.TempDir()` for temporary directories.
- Clean up Docker containers and other resources with `t.Cleanup()`.
- Use in-memory SQLite (`:memory:`) for fast database tests.
- Deterministic UUIDs in tests for reproducibility where possible.

### Fixtures

Test data, configuration, and certificates are organized in `tests/fixtures/`:

```text
tests/
├── fixtures/
│   ├── configs/             # Various Boxy configurations (Docker-only, distributed, full)
│   │   ├── boxy-docker.yaml
│   │   ├── boxy-distributed.yaml
│   │   └── boxy-full.yaml
│   ├── certs/               # Test mTLS certificates
│   │   ├── test-ca.pem
│   │   ├── test-agent-cert.pem
│   │   └── test-agent-key.pem
│   └── images/              # Dockerfiles or build contexts for test images
│       └── test-container/Dockerfile
```

## Performance Benchmarks

Track key metrics using Go's built-in benchmarking tools:

- Sandbox creation time.
- Pool replenishment speed.
- Memory usage under load.
- Goroutine count stability.

## Testing Best Practices

1. **Isolation**: Each test is independent and does not affect others.
2. **Fast**: Unit tests should run quickly (< 1s).
3. **Deterministic**: Tests should produce the same results every time (no flaky tests).
4. **Clear**: Use descriptive test names and provide clear error messages.
5. **Cleanup**: Always ensure resources created during tests are properly cleaned up.
6. **Parallel**: Utilize `t.Parallel()` where tests are safe to run concurrently.
7. **Table-Driven**: Use table-driven tests for variations of inputs or scenarios.

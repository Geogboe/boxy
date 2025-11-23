# Testing Strategy for Boxy

## Overview

Comprehensive testing strategy covering unit, integration, E2E, stress, and edge case testing to ensure Boxy is production-ready.

## Testing Levels

### 1. Unit Tests

**Goal**: Test individual components in isolation

**Coverage**:

- Domain models (Resource, Pool, Sandbox)
- Business logic validation
- State transitions
- Helper functions

**Tools**:

- `testing` (Go stdlib)
- `testify/assert` for assertions
- `testify/require` for critical checks
- `testify/mock` for mocking

**Location**: `*_test.go` files alongside source

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

**Goal**: Test component interactions with real dependencies

**Coverage**:

- Pool manager + Storage
- Sandbox manager + Pool allocators
- Provider + Docker SDK (requires Docker)
- Configuration loading
- Database operations

**Tools**:

- Docker-in-Docker for isolated testing
- Testcontainers for dependencies
- In-memory SQLite for fast tests

**Location**: `tests/integration/`

**Example**:

```go
func TestPoolManager_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    // Setup
    store := setupTestStore(t)
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

### 3. End-to-End Tests

**Goal**: Test full user workflows

**Coverage**:

- `boxy init` → `boxy serve` → `boxy sandbox create` → cleanup
- Multi-pool sandboxes
- Expiration and auto-cleanup
- Error recovery

**Tools**:

- Actual CLI execution
- Docker daemon (real)
- Temporary config files

**Location**: `tests/e2e/`

**Example**:

```go
func TestE2E_CreateSandbox(t *testing.T) {
    // Start service in background
    cmd := exec.Command("./boxy", "serve")
    err := cmd.Start()
    require.NoError(t, err)
    defer cmd.Process.Kill()

    // Wait for service to be ready
    time.Sleep(5 * time.Second)

    // Create sandbox
    out, err := exec.Command("./boxy", "sandbox", "create",
        "-p", "test-pool:1", "-d", "5m").CombinedOutput()
    require.NoError(t, err)
    assert.Contains(t, string(out), "Sandbox created successfully")
}
```

### 4. Mock Provider Tests

**Goal**: Test without Docker dependency

**Coverage**:

- Pool management logic without real containers
- Sandbox orchestration with fake resources
- Error scenarios (Docker down, provision failures)

**Tools**:

- Custom mock provider implementation
- Controlled delays and failures

**Location**: `internal/provider/mock/`

**Example**:

```go
type MockProvider struct {
    provisionDelay time.Duration
    failureRate    float64
}

func (m *MockProvider) Provision(ctx context.Context, spec ResourceSpec) (*Resource, error) {
    if rand.Float64() < m.failureRate {
        return nil, errors.New("simulated provision failure")
    }
    time.Sleep(m.provisionDelay)
    return &Resource{ID: uuid.New().String()}, nil
}
```

### 5. Stress Tests

**Goal**: Verify behavior under load

**Coverage**:

- Concurrent sandbox creation (100+ sandboxes)
- Pool exhaustion scenarios
- Rapid allocation/deallocation
- Memory and goroutine leaks
- Race condition detection

**Tools**:

- `go test -race` for race detection
- `pprof` for profiling
- Custom load generators

**Location**: `tests/stress/`

**Example**:

```go
func TestStress_ConcurrentAllocation(t *testing.T) {
    const numWorkers = 50
    const allocationsPerWorker = 20

    var wg sync.WaitGroup
    errors := make(chan error, numWorkers)

    for i := 0; i < numWorkers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < allocationsPerWorker; j++ {
                _, err := manager.Allocate(ctx, fmt.Sprintf("sb-%d-%d", i, j))
                if err != nil {
                    errors <- err
                }
            }
        }()
    }

    wg.Wait()
    close(errors)

    errorCount := 0
    for err := range errors {
        t.Logf("Allocation error: %v", err)
        errorCount++
    }

    assert.Less(t, errorCount, 10, "Too many allocation failures")
}
```

### 6. Edge Case & Error Tests

**Goal**: Handle failures gracefully

**Coverage**:

- Docker daemon down
- Database corruption
- Config file errors
- Resource limits exceeded
- Network failures
- Partial failures during sandbox creation
- Cleanup failures

**Location**: Throughout test files

**Example**:

```go
func TestError_DockerDown(t *testing.T) {
    // Stop Docker daemon
    exec.Command("systemctl", "stop", "docker").Run()
    defer exec.Command("systemctl", "start", "docker").Run()

    provider := docker.NewProvider(logger)
    err := provider.HealthCheck(context.Background())
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "daemon not reachable")
}

func TestError_PartialSandboxCreation(t *testing.T) {
    // Configure pool to fail after 2 allocations
    mockProvider := &MockProvider{failAfter: 2}

    // Request 5 resources
    _, err := sandboxManager.Create(ctx, &CreateRequest{
        Resources: []ResourceRequest{{PoolName: "test", Count: 5}},
    })

    assert.Error(t, err)

    // Verify cleanup happened
    resources := store.GetResourcesBySandboxID(ctx, "failed-sb")
    assert.Empty(t, resources, "Partial resources should be cleaned up")
}
```

## Test Organization

```text
boxy/
├── internal/
│   ├── core/
│   │   ├── pool/
│   │   │   ├── manager.go
│   │   │   ├── manager_test.go        # Unit tests
│   │   │   └── types_test.go
│   │   ├── sandbox/
│   │   │   ├── manager.go
│   │   │   └── manager_test.go
│   │   └── resource/
│   │       └── types_test.go
│   ├── provider/
│   │   ├── mock/
│   │   │   └── mock.go                # Mock provider
│   │   └── docker/
│   │       └── docker_test.go
│   └── storage/
│       └── sqlite_test.go
├── tests/
│   ├── integration/
│   │   ├── pool_integration_test.go
│   │   ├── sandbox_integration_test.go
│   │   └── helpers.go
│   ├── e2e/
│   │   ├── cli_test.go
│   │   ├── full_workflow_test.go
│   │   └── docker-compose.yml         # Test environment
│   └── stress/
│       ├── concurrent_test.go
│       ├── pool_exhaustion_test.go
│       └── memory_leak_test.go
└── Makefile                           # Test runners
```

## Test Commands

```makefile
# Makefile
.PHONY: test test-unit test-integration test-e2e test-stress test-all

test-unit:
 go test -v -short ./...

test-integration:
 go test -v -run Integration ./tests/integration/

test-e2e:
 go test -v -timeout 10m ./tests/e2e/

test-stress:
 go test -v -timeout 30m ./tests/stress/

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

## Coverage Goals

- **Unit Tests**: >80% coverage for business logic
- **Integration Tests**: All critical paths tested
- **E2E Tests**: Main user workflows covered
- **Edge Cases**: All error paths tested

## Continuous Integration

GitHub Actions workflow:

```yaml
name: Tests

on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - run: make test-unit
      - run: make test-race

  integration-tests:
    runs-on: ubuntu-latest
    services:
      docker:
        image: docker:dind
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: make test-integration

  e2e-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: go build -o boxy ./cmd/boxy
      - run: make test-e2e
```

## Test Data Management

- Use `t.TempDir()` for temporary directories
- Clean up Docker containers with `t.Cleanup()`
- Use in-memory SQLite (`:memory:`) for fast tests
- Deterministic UUIDs in tests for reproducibility

## Performance Benchmarks

Track key metrics:

- Sandbox creation time
- Pool replenishment speed
- Memory usage under load
- Goroutine count stability

## Testing Best Practices

1. **Isolation**: Each test is independent
2. **Fast**: Unit tests run in <1s
3. **Deterministic**: No flaky tests
4. **Clear**: Descriptive test names and error messages
5. **Cleanup**: Always clean up resources
6. **Parallel**: Use `t.Parallel()` where safe
7. **Table-Driven**: Use table-driven tests for variations

## Current Status

- [ ] Unit tests for domain models
- [ ] Mock provider implementation
- [ ] Integration tests for pool manager
- [ ] Integration tests for sandbox manager
- [ ] E2E CLI tests
- [ ] Stress tests for concurrent operations
- [ ] Race detection tests
- [ ] CI/CD pipeline
- [ ] Performance benchmarks

## Next Steps

1. Implement mock provider
2. Add unit tests for core domain
3. Add integration tests
4. Create E2E test suite
5. Run stress tests
6. Set up CI pipeline
7. Achieve >80% coverage

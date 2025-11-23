# Testing Strategy - v1 Prerelease

---

## Metadata

```yaml
feature: "Testing Strategy"
slug: "testing-strategy"
status: "not-started"
priority: "critical"
type: "documentation"
effort: "medium"
depends_on: []
enables: ["all-features"]
testing: ["meta"]
breaking_change: false
week: "ongoing"
```

---

## Overview

v1 Prerelease uses **Test-Driven Development** with emphasis on:

- **Smoke tests** - Quick sanity checks
- **Integration tests** - Component interactions with real providers
- **E2E tests** - Full user workflows
- **Stubs/mocks** - Test without unavailable harness (Hyper-V on Linux)

**Philosophy**: Test incrementally as features are built, not all at end.

---

## Testing Pyramid

```text
        ┌─────────────┐
        │  Manual     │  Real Hyper-V on Windows
        │  Testing    │
        └─────────────┘
       ┌───────────────┐
       │  E2E Tests    │  Full workflows with stubs
       │               │
       └───────────────┘
      ┌─────────────────┐
      │ Integration     │  Real Docker, stubbed Hyper-V
      │ Tests           │
      └─────────────────┘
     ┌───────────────────┐
     │  Unit Tests       │  Isolated components
     │                   │
     └───────────────────┘
```

---

## Unit Tests

### Target: > 80% Coverage

**Scope**: Test individual components in isolation

**Examples:**

```go
// internal/core/allocator/allocator_test.go
func TestAllocator_AllocateFromPool(t *testing.T) {
    mockRepo := &mockResourceRepository{}
    mockPool := &mockPoolManager{}
    allocator := NewAllocator(mockRepo, map[string]*pool.Manager{"test-pool": mockPool}, logger)

    res, err := allocator.AllocateFromPool(ctx, "test-pool", "sb-123")
    assert.NoError(t, err)
    assert.Equal(t, "sb-123", res.SandboxID)
}

func TestAllocator_ConcurrentAllocations(t *testing.T) {
    // Test race conditions with goroutines
}

// internal/core/pool/preheating_test.go
func TestPool_EnsurePreheated(t *testing.T) {
    // Test preheating worker maintains count
}

func TestPool_WarmResource(t *testing.T) {
    // Test cold → warm transition
}

// internal/core/sandbox/manager_test.go
func TestSandbox_CreateWithAllocator(t *testing.T) {
    // Test sandbox creation calls allocator correctly
}
```

**Mocking Strategy:**

- Use interfaces for dependencies
- Create simple mock implementations
- Avoid heavy mocking frameworks (keep it simple)

---

## Integration Tests

### Target: All Major Workflows

**Scope**: Test components working together with **real providers**

**Primary Provider**: Docker (runs on Linux CI)
**Stubbed Provider**: Hyper-V (stub for testing on Linux)

### Integration Examples

```go
// tests/integration/allocator_test.go
func TestIntegration_FullAllocationFlow(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Setup: Real Docker provider, real database
    provider := docker.NewProvider(logger, encryptor)
    repo := sqlite.NewRepository("test.db")
    pool := pool.NewManager(poolConfig, provider, repo, logger)
    pool.Start()
    defer pool.Stop()

    allocator := allocator.NewAllocator(repo, map[string]*pool.Manager{"test-pool": pool}, logger)

    // Test: Allocate resource
    res, err := allocator.AllocateFromPool(ctx, "test-pool", "sb-123")
    assert.NoError(t, err)
    assert.NotNil(t, res.ConnectionInfo)

    // Verify: Resource is running
    status, err := provider.GetStatus(ctx, res)
    assert.NoError(t, err)
    assert.Equal(t, "running", status.State)

    // Cleanup: Release resource
    err = allocator.ReleaseResources(ctx, "sb-123")
    assert.NoError(t, err)
}

// tests/integration/preheating_test.go
func TestPreheating_DockerContainers(t *testing.T) {
    // 1. Create pool with preheating enabled
    // 2. Verify cold resources created
    // 3. Wait for preheating worker cycle
    // 4. Verify warm resources available
    // 5. Allocate warm resource (should be fast < 5s)
    // 6. Allocate cold resource (slower but works)
}

// tests/integration/recycling_test.go
func TestRecycling_RollingStrategy(t *testing.T) {
    // 1. Create pool with recycling enabled (short interval for test)
    // 2. Wait for recycle interval
    // 3. Verify resources recycled one at a time
    // 4. Verify pool maintains availability throughout
}

// tests/integration/multitenancy_test.go
func TestMultiTenancy_QuotaEnforcement(t *testing.T) {
    // 1. Create user with quota=2
    // 2. Create 2 sandboxes (should succeed)
    // 3. Create 3rd sandbox (should fail with quota exceeded)
    // 4. Destroy sandbox
    // 5. Create sandbox again (should succeed)
}

// tests/integration/distributed_agent_test.go
func TestAgent_DockerViaRemote(t *testing.T) {
    // 1. Start agent with Docker provider
    // 2. Start server with remote provider pointing to agent
    // 3. Allocate resource via remote provider
    // 4. Verify resource created on agent
    // 5. Verify connection info returned to server
}
```

**Running Integration Tests:**

```bash
# Run all integration tests
go test ./tests/integration/... -v

# Skip slow tests
go test ./tests/integration/... -short

# Run specific test
go test ./tests/integration/... -run TestPreheating
```

---

## E2E Tests

### Target: All Documented Use Cases

**Scope**: Full user workflows from CLI/API to resource ready

**Uses**: Real Docker provider + Stubbed Hyper-V provider

### E2E Examples

```go
// tests/e2e/quick_testing_usecase_test.go
func TestE2E_QuickTestingUseCase(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping e2e test")
    }

    // Simulates primary use case from USE_CASES.md

    // 1. Start Boxy server
    server := startBoxyServer(t, "testdata/boxy-docker.yaml")
    defer server.Stop()

    // 2. Request sandbox via CLI
    cmd := exec.Command("boxy", "sandbox", "create",
        "-p", "ubuntu-containers:1",
        "-d", "1h",
    )
    output, err := cmd.CombinedOutput()
    assert.NoError(t, err)

    // Parse sandbox ID from output
    sandboxID := parseSandboxID(string(output))

    // 3. Verify sandbox created quickly (preheated resource)
    start := time.Now()
    assert.Eventually(t, func() bool {
        cmd := exec.Command("boxy", "sandbox", "get", sandboxID)
        output, _ := cmd.CombinedOutput()
        return strings.Contains(string(output), "Ready")
    }, 10*time.Second, 1*time.Second)
    duration := time.Since(start)
    assert.Less(t, duration, 10*time.Second, "Should allocate preheated resource quickly")

    // 4. Get connection info
    cmd = exec.Command("boxy", "sandbox", "resources", sandboxID)
    output, err = cmd.CombinedOutput()
    assert.NoError(t, err)
    assert.Contains(t, string(output), "ssh://")

    // 5. Destroy sandbox
    cmd = exec.Command("boxy", "sandbox", "destroy", sandboxID)
    err = cmd.Run()
    assert.NoError(t, err)

    // 6. Verify cleanup
    cmd = exec.Command("boxy", "sandbox", "get", sandboxID)
    output, err = cmd.CombinedOutput()
    assert.Error(t, err) // Should not exist
}

// tests/e2e/ci_runner_usecase_test.go
func TestE2E_CIRunnerUseCase(t *testing.T) {
    // Simulates CI/CD runner use case
    // Multiple rapid sandbox creations in parallel
}

// tests/e2e/distributed_agent_test.go
func TestE2E_DistributedAgent_StubHyperV(t *testing.T) {
    // 1. Start agent with stubbed Hyper-V provider
    // 2. Start server configured to use agent
    // 3. Create sandbox with Hyper-V pool
    // 4. Verify communication over gRPC/mTLS
    // 5. Verify resource created on agent
}

// tests/e2e/architecture_refactor_test.go
func TestE2E_SandboxWithNewArchitecture(t *testing.T) {
    // Regression test: Ensure new architecture doesn't break existing functionality
    // All MVP use cases should still work
}
```

**Running E2E Tests:**

```bash
# Run all e2e tests
go test ./tests/e2e/... -v

# Run specific use case
go test ./tests/e2e/... -run TestE2E_QuickTesting
```

---

## Stub/Mock Strategy

### Hyper-V Stub for Linux Testing

**Problem**: Hyper-V only runs on Windows, but CI runs on Linux

**Solution**: Stub provider that simulates Hyper-V behavior

```go
// pkg/provider/stub/hyperv_stub.go
package stub

type StubHyperVProvider struct {
    vms     map[string]*stubVM
    mu      sync.Mutex
    latency time.Duration // Configurable latency
}

type stubVM struct {
    ID     string
    Name   string
    State  string // "stopped", "running"
    CPUs   int
    Memory int64
}

func NewStubHyperVProvider(latency time.Duration) *StubHyperVProvider {
    return &StubHyperVProvider{
        vms:     make(map[string]*stubVM),
        latency: latency,
    }
}

func (s *StubHyperVProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Simulate realistic provision time
    time.Sleep(s.latency) // e.g., 10 seconds

    s.mu.Lock()
    defer s.mu.Unlock()

    vm := &stubVM{
        ID:     uuid.New().String(),
        Name:   fmt.Sprintf("stub-vm-%s", uuid.New().String()[:8]),
        State:  "stopped",
        CPUs:   spec.CPUs,
        Memory: spec.MemoryMB,
    }
    s.vms[vm.ID] = vm

    return &resource.Resource{
        ID:         uuid.New().String(),
        ProviderID: vm.ID,
        State:      resource.StateProvisioned,
        Type:       resource.ResourceTypeVM,
        ProviderType: "hyperv",
        Metadata: map[string]string{
            "stub":    "true",
            "vm_name": vm.Name,
        },
    }, nil
}

func (s *StubHyperVProvider) Update(ctx context.Context, res *resource.Resource, update provider.ResourceUpdate) error {
    // Simulate starting VM
    if update.PowerState != nil && *update.PowerState == provider.PowerStateRunning {
        time.Sleep(3 * time.Second) // Boot time

        s.mu.Lock()
        defer s.mu.Unlock()

        vm, ok := s.vms[res.ProviderID]
        if !ok {
            return errors.New("vm not found")
        }
        vm.State = "running"
    }
    return nil
}

func (s *StubHyperVProvider) GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error) {
    return &resource.ConnectionInfo{
        Protocol: "rdp",
        Host:     "stub-vm.local",
        Port:     3389,
        Username: "Administrator",
        Password: "stub-password",
    }, nil
}

// Implement all other Provider methods realistically...
```

**Usage in Tests:**

```go
// Use stub in integration tests
stubProvider := stub.NewStubHyperVProvider(10 * time.Second)
pool := pool.NewManager(poolConfig, stubProvider, repo, logger)
```

---

## Smoke Tests

**Definition**: Minimal sanity checks that core functionality works

**Run**: After every commit, before pushing

```bash
# Smoke test script
#!/bin/bash
set -e

echo "Running smoke tests..."

# 1. Build
go build ./cmd/boxy

# 2. Unit tests (fast)
go test ./internal/... -short

# 3. Basic E2E
go test ./tests/e2e/... -run TestE2E_Basic -short

echo "✅ Smoke tests passed"
```

---

## Regression Tests

**Goal**: Ensure no functionality lost from MVP

**Strategy**:

- Keep ALL existing E2E tests from MVP
- Run with new architecture
- Must pass without modification

```go
// tests/e2e/mvp_regression_test.go
func TestRegression_SandboxCreate(t *testing.T) {
    // Original MVP test - must still pass
}

func TestRegression_PoolStats(t *testing.T) {
    // Original MVP test - must still pass
}
```

---

## Manual Testing

### Hyper-V on Windows

**Required**: At least once before v1 release

**Setup**:

1. Windows Server with Hyper-V enabled
2. Install Boxy agent on Windows
3. Linux server with Boxy server
4. Configure mTLS certificates

**Test Cases**:

- [ ] Agent connects to server successfully
- [ ] Hyper-V pool provisions VMs
- [ ] Preheating works with VMs
- [ ] Sandbox creation with VMs
- [ ] RDP connection info correct
- [ ] VM cleanup works

**Documentation**: Record results in `docs/manual-testing-results.md`

---

## CI/CD Integration

### GitHub Actions

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
        run: go test ./internal/... ./pkg/... -race -coverprofile=coverage.out
      - name: Upload coverage
        uses: codecov/codecov-action@v3

  integration:
    runs-on: ubuntu-latest
    services:
      docker:
        image: docker:dind
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Integration tests
        run: go test ./tests/integration/... -v

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Build
        run: go build ./cmd/boxy
      - name: E2E tests
        run: go test ./tests/e2e/... -v
```

---

## Test Data Management

### Fixtures

```text
tests/
├── fixtures/
│   ├── configs/
│   │   ├── boxy-docker.yaml       # Docker-only config
│   │   ├── boxy-distributed.yaml  # With agents
│   │   └── boxy-full.yaml         # All features
│   ├── certs/
│   │   ├── test-ca.pem
│   │   ├── test-agent-cert.pem
│   │   └── test-agent-key.pem
│   └── images/
│       └── test-container/Dockerfile
```

### Test Databases

```go
// tests/testutil/db.go
func NewTestDB(t *testing.T) *sql.DB {
    // Create temporary SQLite database
    db, err := sql.Open("sqlite3", ":memory:")
    require.NoError(t, err)

    // Run migrations
    RunMigrations(db)

    // Cleanup
    t.Cleanup(func() {
        db.Close()
    })

    return db
}
```

---

## Coverage Targets

| Component | Target | Status |
| ----------- | -------- | -------- |
| internal/core/allocator | > 90% | not-started |
| internal/core/pool | > 85% | not-started |
| internal/core/sandbox | > 85% | not-started |
| pkg/provider/remote | > 90% | not-started |
| internal/agent | > 85% | not-started |
| **Overall** | **> 80%** | not-started |

---

## Test Organization

```text
tests/
├── unit/                   # (optional, can live with code)
├── integration/
│   ├── allocator_test.go
│   ├── preheating_test.go
│   ├── recycling_test.go
│   ├── multitenancy_test.go
│   └── agent_test.go
├── e2e/
│   ├── quick_testing_usecase_test.go
│   ├── ci_runner_usecase_test.go
│   ├── distributed_agent_test.go
│   └── mvp_regression_test.go
├── fixtures/
│   ├── configs/
│   ├── certs/
│   └── images/
└── testutil/
    ├── db.go
    ├── docker.go
    ├── server.go
    └── assertions.go
```

---

## Success Criteria

- ✅ All unit tests pass (> 80% coverage)
- ✅ All integration tests pass with Docker
- ✅ All E2E tests pass with stubs
- ✅ No regression from MVP functionality
- ✅ Manual Hyper-V testing successful
- ✅ CI/CD pipeline passing
- ✅ Tests run in < 10 minutes total

---

**Last Updated**: 2025-11-23
**Review**: Continuous throughout v1 implementation

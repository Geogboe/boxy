# Testing Strategy

## Philosophy

**Unit tests should be pure logic tests with no external dependencies. Integration tests should test real interactions with external systems.**

## Test Types

### 1. Unit Tests

**Location**: `*_test.go` files next to source
**Run**: `go test ./...` or `go test -short ./...`

**Characteristics:**

- ✅ No external dependencies (no Docker, no Windows, no network)
- ✅ Fast (< 1ms per test typically)
- ✅ Mock all external interactions
- ✅ Test pure logic, algorithms, data transformations
- ✅ Run on all platforms (Linux, Windows, macOS)
- ✅ Run in CI on every commit

**Example packages with unit tests:**

- `pkg/crypto` - Pure cryptographic logic
- `pkg/powershell` - Uses mock executor
- `internal/core/pool` - Uses mock provider interface

**Naming**: `package_test.go` or `feature_test.go`

### 2. Integration Tests

**Location**: `*_integration_test.go` next to source OR `tests/integration/`
**Run**: `go test ./...` (includes integration) or `go test -tags=integration`

**Characteristics:**

- ✅ Tests real external systems (Docker, Hyper-V, databases)
- ⚠️ Slower (seconds to minutes)
- ⚠️ Platform-specific (may require Windows, Docker, etc.)
- ✅ Skip automatically when dependencies unavailable
- ✅ Run in CI on platform-specific runners

**Skipping Strategy:**

```go
func TestDockerIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    if runtime.GOOS != "windows" {
        t.Skip("requires Windows")
    }
    // Or check for Docker availability
}
```

**Example integration tests:**

- `pkg/powershell/executor_integration_test.go` - Requires Windows + PowerShell
- `pkg/hyperv/vm_integration_test.go` - Requires Windows + Hyper-V
- `pkg/provider/docker/provider_integration_test.go` - Requires Docker

**Naming**: `*_integration_test.go`

### 3. End-to-End Tests

**Location**: `tests/e2e/`
**Run**: `go test ./tests/e2e/`

**Characteristics:**

- ✅ Tests complete user workflows
- ✅ Real Boxy service + real providers
- ⚠️ Slowest (minutes)
- ⚠️ Requires full environment setup
- ✅ Run in CI before releases

**Examples:**

- Create sandbox → allocate resources → verify access → destroy
- Pool replenishment with hooks
- Multi-resource sandbox with networking

## Integration Test Approaches in Go

### Approach 1: Build Tags (Recommended for large suites)

```go
//go:build integration
// +build integration

package hyperv_test

func TestHyperVIntegration(t *testing.T) {
    // Real Hyper-V tests
}
```

Run with: `go test -tags=integration ./...`

**Pros:**

- Clear separation
- Can have different build constraints per file
- CI can run separately

**Cons:**

- Must remember to use tags
- Default `go test` skips them

### Approach 2: testing.Short() Skip (Current approach)

```go
package powershell

func TestRealPowerShell(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    // Real PowerShell test
}
```

Run unit only: `go test -short ./...`
Run all: `go test ./...`

**Pros:**

- Default `go test` runs everything
- Easy to remember
- No build tags needed

**Cons:**

- Integration tests run by default (slower)
- Less explicit separation

### Approach 3: Separate Directory

```text
tests/
  integration/
    hyperv/
      vm_test.go
    docker/
      container_test.go
```

**Pros:**

- Very clear organization
- Can have shared test utilities
- Easy to run separately: `go test ./tests/integration/...`

**Cons:**

- More directory structure
- Import paths more complex

### Recommendation: Hybrid Approach

**For Boxy, use:**

1. **Unit tests**: `*_test.go` next to source (run with `go test -short ./...`)
2. **Integration tests**: `*_integration_test.go` with `testing.Short()` skip
3. **E2E tests**: `tests/e2e/` separate directory

**Why?**

- Unit tests run fast by default (`-short`)
- Integration tests colocated with code (easier to find)
- E2E tests separate (complex setup, orchestration)

## CI Strategy

### GitHub Actions Example

```yaml
name: Tests

on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: go test -short ./...  # Unit tests only

  integration-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: docker pull ubuntu:latest
      - run: go test ./pkg/provider/docker/...  # Docker integration

  integration-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: go test ./pkg/powershell/...  # PowerShell integration
      - run: go test ./pkg/hyperv/...      # Hyper-V integration (if enabled)

  e2e:
    runs-on: ubuntu-latest
    needs: [unit-tests, integration-linux]
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - run: docker-compose up -d  # Start dependencies
      - run: go test ./tests/e2e/...
```

## Mocking Strategy

### Package Level Interfaces

```go
// pkg/powershell/interface.go
type Commander interface {
    Exec(ctx context.Context, script string) (string, error)
    ExecJSON(ctx context.Context, script string, result interface{}) error
}

// pkg/powershell/mock.go
type MockExecutor struct {
    ExecFunc func(ctx context.Context, script string) (string, error)
}
```

**Benefits:**

- Consumers can mock dependencies
- pkg/hyperv can test without real PowerShell
- pkg/provider/hyperv can test without real Hyper-V

### Provider Interface

```go
// pkg/provider/provider.go
type Provider interface {
    Provision(ctx context.Context, spec ResourceSpec) (*Resource, error)
    Destroy(ctx context.Context, id string) error
}

// pkg/provider/mock/mock.go
type MockProvider struct {
    ProvisionFunc func(...) (*Resource, error)
}
```

**Benefits:**

- internal/core/pool can test without real providers
- internal/core/sandbox can test allocation logic
- Fast unit tests for orchestration layers

## Coverage Goals

**Target coverage by layer:**

- `pkg/*` packages: **80-90%** (pure logic, highly testable)
- `internal/core/*`: **70-80%** (business logic with mocks)
- `internal/storage/*`: **60-70%** (database integration)
- `internal/server/*`: **60-70%** (HTTP handlers)

**Coverage commands:**

```bash
# Overall coverage
go test -cover ./...

# Detailed coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Per-package coverage
go test -cover ./pkg/crypto
go test -cover ./pkg/powershell
go test -cover ./internal/core/pool
```

## Testing Checklist

### For new pkg/ packages

- [ ] Unit tests with mocks (if external dependencies)
- [ ] Integration tests (if platform-specific)
- [ ] README with testing section
- [ ] Interface for mocking (if consumed by others)
- [ ] Tests run on CI

### For new internal/ packages

- [ ] Unit tests with mocked dependencies
- [ ] Integration tests (if needed)
- [ ] Tests for error cases
- [ ] Tests for edge cases

### Before merging

- [ ] All unit tests pass (`go test -short ./...`)
- [ ] Integration tests pass on relevant platforms
- [ ] No decrease in coverage
- [ ] CI passes

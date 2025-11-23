# pkg/provider/mock

Mock provider implementation for testing Boxy without real infrastructure.

## Purpose

Provides a fake provider that simulates resource provisioning without requiring Docker, Hyper-V, or any real infrastructure. Perfect for unit tests and development.

## Contract

**Implements**: `provider.Provider`

**Features**:

- Configurable delays (provision, destroy)
- Simulated failure rates (for testing error handling)
- In-memory resource tracking
- Statistics collection (provision count, destroy count, etc.)
- Health check simulation

**Guarantees**:

- Fast (millisecond delays, configurable)
- Deterministic (no actual external state)
- Thread-safe (protected by mutex)
- Resettable (clear all state)

## Usage Example

### Basic Usage

```go
import (
    "github.com/sirupsen/logrus"
    "boxy/pkg/provider"
    "boxy/pkg/provider/mock"
)

// Create mock provider
logger := logrus.New()
provider := mock.NewProvider(logger, nil) // Uses default config

// Provision fake resource
spec := provider.ResourceSpec{
    Type:         provider.ResourceTypeContainer,
    ProviderType: "mock",
    Image:        "test-image",
    CPUs:         2,
    MemoryMB:     1024,
}

resource, err := provider.Provision(context.Background(), spec)
// Returns immediately after 100ms delay

// Get connection info
conn, err := provider.GetConnectionInfo(context.Background(), resource)
// Returns mock credentials

// Destroy
provider.Destroy(context.Background(), resource)
```

### With Custom Configuration

```go
config := &mock.Config{
    ProvisionDelay:   500 * time.Millisecond,  // Simulate slow provisioning
    DestroyDelay:     100 * time.Millisecond,
    FailureRate:      0.1,  // 10% of provisions fail
    ShouldFailHealth: false,
}

provider := mock.NewProvider(logger, config)
```

### For Testing

```go
func TestPoolProvisioning(t *testing.T) {
    logger := logrus.New()
    mockProvider := mock.NewProvider(logger, &mock.Config{
        ProvisionDelay: 10 * time.Millisecond,  // Fast for tests
        FailureRate:    0.0,  // No failures
    })

    // Test pool with mock provider
    pool := pool.NewManager(config, mockProvider, repo, logger)

    resource, err := pool.Allocate(ctx, "sandbox-123")
    require.NoError(t, err)
    assert.NotNil(t, resource)

    // Check stats
    stats := mockProvider.Stats()
    assert.Equal(t, 1, stats.ProvisionCount)
    assert.Equal(t, 1, stats.ActiveResources)
}
```

## Architecture

**Links:**

- [Provider Interface](../provider.go)
- [Testing Strategy](../../../docs/TESTING_STRATEGY.md)

**Dependencies:**

- `pkg/provider` - Provider interface & types (only)

**Used by:**

- `internal/core/pool` tests
- `internal/core/sandbox` tests
- Integration tests
- Development/debugging

## API

### Methods

```go
// Provision - Creates a mock resource
Provision(ctx, spec) (*Resource, error)

// Destroy - Removes a mock resource
Destroy(ctx, res) error

// GetStatus - Returns mock status
GetStatus(ctx, res) (*ResourceStatus, error)

// GetConnectionInfo - Returns mock connection details
GetConnectionInfo(ctx, res) (*ConnectionInfo, error)

// Exec - Simulates command execution
Exec(ctx, res, cmd) (*ExecResult, error)

// Update - Simulates resource updates
Update(ctx, res, updates) error

// HealthCheck - Returns health status
HealthCheck(ctx) error
```

### Additional Methods (Testing)

```go
// Stats - Returns provider statistics
Stats() ProviderStats

// SetFailHealth - Control health check behavior
SetFailHealth(shouldFail bool)

// Reset - Clears all state (use between tests)
Reset()
```

## Configuration Options

```go
type Config struct {
    // ProvisionDelay - How long Provision() takes
    ProvisionDelay time.Duration  // Default: 100ms

    // DestroyDelay - How long Destroy() takes
    DestroyDelay time.Duration    // Default: 50ms

    // FailureRate - Probability of provision failure (0.0 - 1.0)
    FailureRate float64           // Default: 0.0 (no failures)

    // ShouldFailHealth - Whether HealthCheck() should fail
    ShouldFailHealth bool          // Default: false
}
```

## Testing Scenarios

### Normal Operation

```go
provider := mock.NewProvider(logger, nil)
// Fast, reliable provisioning
```

### Slow Provisioning

```go
config := &mock.Config{
    ProvisionDelay: 5 * time.Second,  // Simulate slow hardware
}
provider := mock.NewProvider(logger, config)
// Test timeout handling
```

### Random Failures

```go
config := &mock.Config{
    FailureRate: 0.2,  // 20% failure rate
}
provider := mock.NewProvider(logger, config)
// Test retry logic, error handling
```

### Health Check Failures

```go
provider := mock.NewProvider(logger, nil)
provider.SetFailHealth(true)
// Test pool/sandbox reaction to unhealthy provider
```

### Statistics Tracking

```go
provider := mock.NewProvider(logger, nil)

// Do operations...

stats := provider.Stats()
fmt.Printf("Provisioned: %d\n", stats.ProvisionCount)
fmt.Printf("Destroyed: %d\n", stats.DestroyCount)
fmt.Printf("Active: %d\n", stats.ActiveResources)
```

## Best Practices

### Use in Unit Tests

```go
// Fast, no external dependencies
config := &mock.Config{
    ProvisionDelay: 1 * time.Millisecond,  // As fast as possible
    FailureRate:    0.0,  // Deterministic
}
```

### Use in Integration Tests

```go
// More realistic timings
config := &mock.Config{
    ProvisionDelay: 100 * time.Millisecond,  // Simulate real-world delay
}
```

### Reset Between Tests

```go
func TestMultipleScenarios(t *testing.T) {
    provider := mock.NewProvider(logger, nil)

    t.Run("scenario 1", func(t *testing.T) {
        // Test...
        provider.Reset()  // Clean slate
    })

    t.Run("scenario 2", func(t *testing.T) {
        // Test...
        provider.Reset()
    })
}
```

## Limitations

- No actual resources created (all in-memory)
- Not suitable for E2E tests (use real providers)
- Statistics are process-local (not persisted)
- No actual command execution (just simulated)

## When to Use

✅ **Use Mock Provider:**

- Unit tests for pool/sandbox logic
- Development without infrastructure
- Testing error scenarios (failures, timeouts)
- CI/CD pipelines without Docker/Hyper-V

❌ **Don't Use Mock Provider:**

- E2E tests (use real providers)
- Production deployments
- Testing actual virtualization features
- Benchmarking real performance

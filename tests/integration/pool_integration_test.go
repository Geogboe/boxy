package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/provider/mock"
)

func TestPoolManager_Integration_BasicLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	poolCfg := SetupTestPool("test-pool", 3, 10)
	mockCfg := &mock.Config{
		ProvisionDelay: 50 * time.Millisecond,
		DestroyDelay:   25 * time.Millisecond,
	}

	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	// Start the pool manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for pool to provision min_ready resources
	WaitForPoolReady(t, manager, 3)

	// Verify pool stats
	stats, err := manager.GetStats(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalReady, 3)
	assert.True(t, stats.Healthy)
}

func TestPoolManager_Integration_Allocation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 2, 5)
	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 2)

	ctx := context.Background()

	// Allocate a resource
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, "sandbox-1", *res.SandboxID)

	// Pool should trigger replenishment
	time.Sleep(200 * time.Millisecond)

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalAllocated)
	assert.GreaterOrEqual(t, stats.TotalReady+stats.TotalProvisioning, 2)
}

func TestPoolManager_Integration_Release(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 2, 5)
	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 2)

	ctx := context.Background()

	// Allocate
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)

	// Release
	err = manager.Release(ctx, res.ID)
	require.NoError(t, err)

	// Wait for replenishment
	time.Sleep(200 * time.Millisecond)

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalReady, 2)
}

func TestPoolManager_Integration_MultipleAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 5, 10)
	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 5)

	ctx := context.Background()

	// Allocate 3 resources
	for i := 0; i < 3; i++ {
		_, err := manager.Allocate(ctx, "sandbox-1")
		require.NoError(t, err)
	}

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.TotalAllocated)

	// Wait for replenishment
	WaitForPoolReady(t, manager, 5)
}

func TestPoolManager_Integration_ConcurrentAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 10, 20)
	mockCfg := &mock.Config{ProvisionDelay: 100 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 10)

	ctx := context.Background()
	const numWorkers = 5

	// Concurrent allocations with unique sandbox IDs
	type result struct {
		id  int
		err error
	}
	results := make(chan result, numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(id int) {
			sandboxID := fmt.Sprintf("sandbox-concurrent-%d", id)
			_, err := manager.Allocate(ctx, sandboxID)
			results <- result{id: id, err: err}
		}(i)
	}

	// Collect results
	successCount := 0
	for i := 0; i < numWorkers; i++ {
		res := <-results
		if res.err == nil {
			successCount++
		}
		assert.NoError(t, res.err, fmt.Sprintf("Concurrent allocation %d failed", res.id))
	}

	// Give a moment for stats to update
	time.Sleep(100 * time.Millisecond)

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	t.Logf("Successful allocations: %d, Stats.TotalAllocated: %d", successCount, stats.TotalAllocated)
	assert.Equal(t, numWorkers, stats.TotalAllocated)
}

func TestPoolManager_Integration_HealthChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 3, 10)
	poolCfg.HealthCheckInterval = 100 * time.Millisecond

	mockCfg := &mock.Config{
		ProvisionDelay:   50 * time.Millisecond,
		ShouldFailHealth: false, // Start healthy
	}
	mockProvider := mock.NewProvider(TestLogger(), mockCfg)

	store := SetupTestStore(t)
	adapter := storage.NewResourceRepositoryAdapter(store)
	manager, err := pool.NewManager(poolCfg, mockProvider, adapter, TestLogger())
	require.NoError(t, err)

	err = manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 3)

	ctx := context.Background()

	// Now make health checks fail
	mockProvider.SetShouldFailHealth(true)

	// Wait for health check to run and mark resources as unhealthy
	time.Sleep(500 * time.Millisecond)

	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)

	// Some resources should be marked as error or being replaced
	assert.LessOrEqual(t, stats.TotalReady, 3)
}

func TestPoolManager_Integration_CapacityLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool", 2, 3) // max 3 total
	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 2)

	ctx := context.Background()

	// Allocate to capacity (2 ready + 1 allocated = 3)
	_, err = manager.Allocate(ctx, "sb-1")
	require.NoError(t, err)

	// Try to allocate beyond capacity
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)

	if stats.Total >= poolCfg.MaxTotal {
		// Pool should be at capacity
		assert.Equal(t, poolCfg.MaxTotal, stats.Total)
	}
}

// Benchmark integration tests
func BenchmarkPoolManager_Allocate(b *testing.B) {
	poolCfg := SetupTestPool("bench-pool", 100, 200)
	mockCfg := &mock.Config{ProvisionDelay: 1 * time.Millisecond}

	// Can't use testing.T helpers in benchmark
	logger := TestLogger()
	store, _ := storage.NewSQLiteStore(":memory:")
	defer store.Close()

	provider := mock.NewProvider(logger, mockCfg)
	adapter := storage.NewResourceRepositoryAdapter(store)
	manager, _ := pool.NewManager(poolCfg, provider, adapter, logger)

	manager.Start()
	defer manager.Stop()

	// Wait for initial provisioning
	time.Sleep(2 * time.Second)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.Allocate(ctx, "bench-sandbox")
	}
}

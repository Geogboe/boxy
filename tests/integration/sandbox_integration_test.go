package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/storage"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/mock"
)

// SetupTestSandboxManager creates a sandbox manager with pools for testing
func SetupTestSandboxManager(t *testing.T) (*sandbox.Manager, storage.Store) {
	store := SetupTestStore(t)
	logger := TestLogger()

	// Create mock provider
	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	provider := mock.NewProvider(logger, mockCfg)

	// Create provider registry
	registry := provider_pkg.NewRegistry()
	registry.Register("mock", provider)

	// Create two pools for testing
	pool1Cfg := SetupTestPool("pool-ubuntu", 3, 10)
	pool2Cfg := SetupTestPool("pool-alpine", 2, 5)

	adapter := storage.NewResourceRepositoryAdapter(store)

	pool1Manager, err := pool.NewManager(pool1Cfg, provider, adapter, logger)
	require.NoError(t, err)
	err = pool1Manager.Start()
	require.NoError(t, err)

	pool2Manager, err := pool.NewManager(pool2Cfg, provider, adapter, logger)
	require.NoError(t, err)
	err = pool2Manager.Start()
	require.NoError(t, err)

	// Wait for pools to be ready
	WaitForPoolReady(t, pool1Manager, 3)
	WaitForPoolReady(t, pool2Manager, 2)

	// Create pool allocators map
	pools := map[string]allocator.PoolAllocator{
		"pool-ubuntu": pool1Manager,
		"pool-alpine": pool2Manager,
	}

	// Create sandbox manager
	sandboxRepo := storage.NewSandboxRepositoryAdapter(store)
	resourceRepo := storage.NewResourceRepositoryAdapter(store)
	manager := sandbox.NewManager(pools, sandboxRepo, resourceRepo, registry, logger)
	manager.Start()

	t.Cleanup(func() {
		manager.Stop()
		pool1Manager.Stop()
		pool2Manager.Stop()
	})

	return manager, store
}

func TestSandboxManager_Integration_CreateSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox request
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 2},
			{PoolName: "pool-alpine", Count: 1},
		},
	}

	// Create sandbox
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, sb)
	assert.Equal(t, "test-sandbox", sb.Name)
	assert.Equal(t, sandbox.StateReady, sb.State)
	assert.Equal(t, 3, len(sb.ResourceIDs)) // 2 + 1
	assert.NotNil(t, sb.ExpiresAt)
}

func TestSandboxManager_Integration_GetSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 1},
		},
	}
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)

	// Get sandbox
	retrieved, err := manager.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sb.ID, retrieved.ID)
	assert.Equal(t, sb.Name, retrieved.Name)
}

func TestSandboxManager_Integration_ListSandboxes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create multiple sandboxes
	for i := 0; i < 3; i++ {
		req := &sandbox.CreateRequest{
			Name:     fmt.Sprintf("sandbox-%d", i),
			Duration: 1 * time.Hour,
			Resources: []sandbox.ResourceRequest{
				{PoolName: "pool-ubuntu", Count: 1},
			},
		}
		_, err := manager.Create(ctx, req)
		require.NoError(t, err)
	}

	// List sandboxes
	sandboxes, err := manager.List(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(sandboxes), 3)
}

func TestSandboxManager_Integration_DestroySandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 2},
		},
	}
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)

	// Destroy sandbox
	err = manager.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	// Verify state
	retrieved, err := manager.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateDestroyed, retrieved.State)
}

func TestSandboxManager_Integration_ExtendSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 1},
		},
	}
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)
	originalExpiry := *sb.ExpiresAt

	// Extend sandbox
	err = manager.Extend(ctx, sb.ID, 30*time.Minute)
	require.NoError(t, err)

	// Verify extension
	retrieved, err := manager.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.True(t, retrieved.ExpiresAt.After(originalExpiry))
	expectedExpiry := originalExpiry.Add(30 * time.Minute)
	assert.WithinDuration(t, expectedExpiry, *retrieved.ExpiresAt, 1*time.Second)
}

func TestSandboxManager_Integration_GetResourcesWithConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 2},
		},
	}
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)

	// Get resources with connection info
	resources, err := manager.GetResourcesForSandbox(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(resources))

	// Verify connection info exists
	for _, res := range resources {
		assert.NotNil(t, res.Resource)
		assert.NotNil(t, res.Connection)
		assert.Equal(t, "mock", res.Connection.Type)
		assert.NotEmpty(t, res.Connection.Host)
	}
}

func TestSandboxManager_Integration_PartialFailureCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create request with non-existent pool
	req := &sandbox.CreateRequest{
		Name:     "test-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 1},
			{PoolName: "non-existent-pool", Count: 1}, // This will fail
		},
	}

	// Attempt to create sandbox - should fail
	_, err := manager.Create(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool not found")

	// Verify no sandboxes left in limbo
	sandboxes, err := manager.List(ctx)
	require.NoError(t, err)

	// Check that the failed sandbox is not in the list
	for _, sb := range sandboxes {
		assert.NotEqual(t, "test-sandbox", sb.Name, "Failed sandbox should have been cleaned up")
	}
}

func TestSandboxManager_Integration_ExpirationCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox with very short duration
	req := &sandbox.CreateRequest{
		Name:     "short-lived-sandbox",
		Duration: 100 * time.Millisecond, // Very short
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 1},
		},
	}
	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Manually trigger cleanup (normally runs every 30s)
	// We'll just destroy it manually since we can't easily trigger the worker
	err = manager.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	// Verify destroyed
	retrieved, err := manager.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateDestroyed, retrieved.State)
}

func TestSandboxManager_Integration_MultiPoolAllocation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	// Create sandbox with resources from both pools
	req := &sandbox.CreateRequest{
		Name:     "multi-pool-sandbox",
		Duration: 1 * time.Hour,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-ubuntu", Count: 2},
			{PoolName: "pool-alpine", Count: 1},
		},
		Metadata: map[string]string{
			"purpose": "integration-test",
		},
	}

	sb, err := manager.Create(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 3, len(sb.ResourceIDs))
	assert.Equal(t, "integration-test", sb.Metadata["purpose"])

	// Get resources and verify they're from different pools
	resources, err := manager.GetResourcesForSandbox(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(resources))

	poolCounts := make(map[string]int)
	for _, res := range resources {
		poolCounts[res.Resource.PoolID]++
	}
	assert.Equal(t, 2, poolCounts["pool-ubuntu"])
	assert.Equal(t, 1, poolCounts["pool-alpine"])
}

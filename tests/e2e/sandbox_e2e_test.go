package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/internal/core/lifecycle/hooks"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/mock"
	"github.com/sirupsen/logrus"
)

func TestE2E_CompleteSandboxLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup logger
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	// Setup storage
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Setup encryption
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	_, err = crypto.NewEncryptor(key)
	require.NoError(t, err)

	// Setup mock provider
	mockCfg := &mock.Config{
		ProvisionDelay: 100 * time.Millisecond,
		DestroyDelay:   50 * time.Millisecond,
	}
	mockProvider := mock.NewProvider(logger, mockCfg)

	// Setup provider registry
	providerRegistry := provider.NewRegistry()
	providerRegistry.Register("mock", mockProvider)

	// Setup pool configuration with hooks
	poolCfg := &pool.PoolConfig{
		Name:     "test-pool",
		Type:     provider.ResourceTypeContainer,
		Backend:  "mock",
		Image:    "test:latest",
		MinReady: 2,
		MaxTotal: 5,
		CPUs:     1,
		MemoryMB: 512,
		Hooks: hooks.HookConfig{
			OnProvision: []hooks.Hook{
				{
					Name:   "finalize",
					Type:   hooks.HookTypeScript,
					Shell:  hooks.ShellBash,
					Inline: "echo 'Finalization for ${resource.id}'",
				},
			},
			OnAllocate: []hooks.Hook{
				{
					Name:   "personalize",
					Type:   hooks.HookTypeScript,
					Shell:  hooks.ShellBash,
					Inline: "echo 'User: ${username}, Pass: ${password}'",
				},
			},
		},
	}

	// Create pool manager
	resourceRepo := storage.NewResourceRepositoryAdapter(store)
	poolMgr, err := pool.NewManager(poolCfg, mockProvider, resourceRepo, logger)
	require.NoError(t, err)

	// Start pool manager
	err = poolMgr.Start()
	require.NoError(t, err)
	defer poolMgr.Stop()

	// Wait for pool to warm up
	waitForPoolReady(t, poolMgr, 2)

	// Create sandbox manager
	poolAllocators := map[string]allocator.PoolAllocator{
		"test-pool": poolMgr,
	}
	sandboxMgr := sandbox.NewManager(
		poolAllocators,
		store,
		store,
		providerRegistry,
		logger,
	)
	sandboxMgr.Start()
	defer sandboxMgr.Stop()

	ctx := context.Background()

	// === TEST: Create Sandbox ===
	t.Log("Creating sandbox...")
	createReq := &sandbox.CreateRequest{
		Name: "test-sandbox",
		Resources: []sandbox.ResourceRequest{
			{
				PoolName: "test-pool",
				Count:    2,
			},
		},
		Duration: 1 * time.Hour,
	}

	sb, err := sandboxMgr.Create(ctx, createReq)
	require.NoError(t, err)
	assert.NotNil(t, sb)
	assert.Equal(t, "test-sandbox", sb.Name)
	assert.Equal(t, sandbox.StateCreating, sb.State)

	// === TEST: Wait for Ready ===
	t.Log("Waiting for sandbox to be ready...")
	sb, err = sandboxMgr.WaitForReady(ctx, sb.ID, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateReady, sb.State)
	assert.Len(t, sb.ResourceIDs, 2)

	// === TEST: Get Sandbox ===
	t.Log("Getting sandbox details...")
	retrieved, err := sandboxMgr.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sb.ID, retrieved.ID)
	assert.Equal(t, sandbox.StateReady, retrieved.State)

	// === TEST: List Sandboxes ===
	t.Log("Listing sandboxes...")
	sandboxes, err := sandboxMgr.List(ctx)
	require.NoError(t, err)
	assert.Len(t, sandboxes, 1)
	assert.Equal(t, sb.ID, sandboxes[0].ID)

	// === TEST: Get Resources with Connection Info ===
	t.Log("Getting resource connection info...")
	resourcesWithConn, err := sandboxMgr.GetResourcesForSandbox(ctx, sb.ID)
	require.NoError(t, err)
	assert.Len(t, resourcesWithConn, 2)

	for i, rwc := range resourcesWithConn {
		t.Logf("Resource %d: %s", i+1, rwc.Resource.ID)
		assert.NotNil(t, rwc.Connection)
		assert.Equal(t, provider.StateAllocated, rwc.Resource.State)

		// Check that hooks were executed
		assert.Contains(t, rwc.Resource.Metadata, "finalization_hooks")
		assert.Contains(t, rwc.Resource.Metadata, "personalization_hooks")
	}

	// === TEST: Pool Replenishment ===
	t.Log("Verifying pool replenished...")
	time.Sleep(500 * time.Millisecond) // Give time for replenishment
	stats, err := poolMgr.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.TotalAllocated)
	// Pool should have replenished or be replenishing
	assert.GreaterOrEqual(t, stats.TotalReady+stats.TotalProvisioning, 2)

	// === TEST: Destroy Sandbox ===
	t.Log("Destroying sandbox...")
	err = sandboxMgr.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	// Verify sandbox is destroyed
	destroyed, err := sandboxMgr.Get(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.StateDestroyed, destroyed.State)

	// === TEST: Final Pool State ===
	t.Log("Verifying final pool state...")
	time.Sleep(500 * time.Millisecond) // Give time for cleanup and replenishment
	stats, err = poolMgr.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalAllocated)      // No more allocated
	assert.GreaterOrEqual(t, stats.TotalReady, 2) // Pool replenished

	t.Log("✓ Complete lifecycle test passed")
}

func TestE2E_MultipleResourceTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup (similar to above but simplified)
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	_, err = crypto.NewEncryptor(key)
	require.NoError(t, err)

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	mockProvider := mock.NewProvider(logger, mockCfg)

	providerRegistry := provider.NewRegistry()
	providerRegistry.Register("mock", mockProvider)

	resourceRepo := storage.NewResourceRepositoryAdapter(store)

	// Create two different pools
	pool1Cfg := &pool.PoolConfig{
		Name:     "pool-a",
		Type:     provider.ResourceTypeContainer,
		Backend:  "mock",
		Image:    "test-a:latest",
		MinReady: 1,
		MaxTotal: 3,
		CPUs:     1,
		MemoryMB: 256,
	}

	pool2Cfg := &pool.PoolConfig{
		Name:     "pool-b",
		Type:     provider.ResourceTypeContainer,
		Backend:  "mock",
		Image:    "test-b:latest",
		MinReady: 1,
		MaxTotal: 3,
		CPUs:     2,
		MemoryMB: 512,
	}

	poolMgr1, err := pool.NewManager(pool1Cfg, mockProvider, resourceRepo, logger)
	require.NoError(t, err)
	poolMgr2, err := pool.NewManager(pool2Cfg, mockProvider, resourceRepo, logger)
	require.NoError(t, err)

	err = poolMgr1.Start()
	require.NoError(t, err)
	defer poolMgr1.Stop()

	err = poolMgr2.Start()
	require.NoError(t, err)
	defer poolMgr2.Stop()

	waitForPoolReady(t, poolMgr1, 1)
	waitForPoolReady(t, poolMgr2, 1)

	// Create sandbox with resources from both pools
	poolAllocators := map[string]allocator.PoolAllocator{
		"pool-a": poolMgr1,
		"pool-b": poolMgr2,
	}

	sandboxMgr := sandbox.NewManager(poolAllocators, store, store, providerRegistry, logger)
	sandboxMgr.Start()
	defer sandboxMgr.Stop()

	ctx := context.Background()

	createReq := &sandbox.CreateRequest{
		Resources: []sandbox.ResourceRequest{
			{PoolName: "pool-a", Count: 1},
			{PoolName: "pool-b", Count: 1},
		},
		Duration: 1 * time.Hour,
	}

	sb, err := sandboxMgr.Create(ctx, createReq)
	require.NoError(t, err)

	sb, err = sandboxMgr.WaitForReady(ctx, sb.ID, 30*time.Second)
	require.NoError(t, err)
	assert.Len(t, sb.ResourceIDs, 2)

	// Cleanup
	err = sandboxMgr.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	t.Log("✓ Multiple resource types test passed")
}

func TestE2E_SandboxExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	_, err = crypto.NewEncryptor(key)
	require.NoError(t, err)

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	mockProvider := mock.NewProvider(logger, mockCfg)

	providerRegistry := provider.NewRegistry()
	providerRegistry.Register("mock", mockProvider)

	poolCfg := &pool.PoolConfig{
		Name:     "test-pool",
		Type:     provider.ResourceTypeContainer,
		Backend:  "mock",
		Image:    "test:latest",
		MinReady: 1,
		MaxTotal: 3,
	}

	resourceRepo := storage.NewResourceRepositoryAdapter(store)
	poolMgr, err := pool.NewManager(poolCfg, mockProvider, resourceRepo, logger)
	require.NoError(t, err)

	err = poolMgr.Start()
	require.NoError(t, err)
	defer poolMgr.Stop()

	waitForPoolReady(t, poolMgr, 1)

	poolAllocators := map[string]allocator.PoolAllocator{"test-pool": poolMgr}
	sandboxMgr := sandbox.NewManager(poolAllocators, store, store, providerRegistry, logger)
	sandboxMgr.Start()
	defer sandboxMgr.Stop()

	ctx := context.Background()

	// Create sandbox with very short duration
	createReq := &sandbox.CreateRequest{
		Resources: []sandbox.ResourceRequest{{PoolName: "test-pool", Count: 1}},
		Duration:  2 * time.Second, // Very short
	}

	sb, err := sandboxMgr.Create(ctx, createReq)
	require.NoError(t, err)

	sb, err = sandboxMgr.WaitForReady(ctx, sb.ID, 30*time.Second)
	require.NoError(t, err)

	// Verify it's ready
	assert.Equal(t, sandbox.StateReady, sb.State)

	// Wait for expiration
	time.Sleep(3 * time.Second)

	// Check if expired
	assert.True(t, sb.IsExpired(), "Sandbox should be expired")

	// Cleanup worker should eventually clean it up
	// (We don't wait for it in this test to keep it fast)

	t.Log("✓ Sandbox expiration test passed")
}

// Helper function
func waitForPoolReady(t *testing.T, mgr *pool.Manager, minReady int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			require.Fail(t, "Timeout waiting for pool to be ready")
			return
		case <-ticker.C:
			stats, err := mgr.GetStats(context.Background())
			if err != nil {
				continue
			}
			if stats.TotalReady >= minReady {
				return
			}
		}
	}
}

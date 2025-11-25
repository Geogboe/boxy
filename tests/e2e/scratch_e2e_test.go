package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/runtime"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

// createTestLogger creates a test logger
func createTestLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	if os.Getenv("TEST_VERBOSE") == "1" {
		logger.SetLevel(logrus.DebugLevel)
	}
	return logger
}

// TestScratchE2E_FullWorkflow tests the complete workflow with scratch provider
// This test validates: server startup, pool creation, sandbox lifecycle, and cleanup
func TestScratchE2E_FullWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	ctx := context.Background()

	// Create temporary directory for test
	testDir, err := os.MkdirTemp("", "boxy-e2e-*")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	scratchDir := filepath.Join(testDir, "scratch")
	dbPath := filepath.Join(testDir, "test.db")

	// Create test configuration
	cfg := &config.Config{
		Storage: config.StorageConfig{
			Type: "sqlite",
			Path: dbPath,
		},
		Logging: config.LoggingConfig{
			Level: "info",
		},
		Pools: []pool.PoolConfig{
			{
				Name:                "scratch-pool",
				Type:                "process",
				Backend:             "scratch/shell",
				MinReady:            2,
				MaxTotal:            5,
				CPUs:                1,
				MemoryMB:            128,
				HealthCheckInterval: 5 * time.Second,
				ExtraConfig: map[string]interface{}{
					"base_dir": scratchDir,
					"allowed_shells": []interface{}{
						"bash",
						"sh",
					},
				},
			},
		},
	}

	// Setup storage
	store, err := storage.NewSQLiteStore(dbPath)
	require.NoError(t, err, "Failed to create storage")
	defer store.Close()

	// Setup encryption
	encryptionKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	_, err = crypto.NewEncryptor(encryptionKey)
	require.NoError(t, err)

	// Build provider registry
	logger := createTestLogger()
	registry := provider.NewRegistry()

	shellProvider := shell.New(logger, shell.Config{
		BaseDir:       scratchDir,
		AllowedShells: []string{"bash", "sh"},
	})
	registry.Register("scratch/shell", shellProvider)

	// Start runtime
	t.Log("Starting Boxy runtime...")
	rt, err := runtime.StartSandboxRuntime(ctx, cfg, registry, store, logger)
	require.NoError(t, err, "Failed to start runtime")

	// Start pool managers
	for _, pm := range rt.Pools {
		err = pm.Start()
		require.NoError(t, err, "Failed to start pool manager")
	}

	// Defer cleanup in correct order: pools first, then runtime
	defer func() {
		for _, pm := range rt.Pools {
			pm.Stop()
		}
		rt.Stop(logger)
	}()

	// Wait for pool to warm up
	t.Log("Waiting for pool to reach min_ready=2...")
	waitForScratchPoolReady(t, rt.Pools["scratch-pool"], 2)

	// Test 1: Create a sandbox
	t.Log("Test 1: Creating sandbox...")
	createReq := runtime.CreateSandboxRequest{
		Name:     "e2e-test-sandbox",
		Duration: 10 * time.Minute,
		Resources: []runtime.ResourceRequest{
			{PoolName: "scratch-pool", Count: 1},
		},
	}

	sb, err := runtime.CreateSandbox(ctx, rt, createReq)
	require.NoError(t, err, "Failed to create sandbox")
	require.NotNil(t, sb)
	require.NotEmpty(t, sb.ID)
	t.Logf("Created sandbox: %s", sb.ID)

	// Wait for sandbox to be ready
	t.Log("Waiting for sandbox to be ready...")
	sb, err = runtime.WaitSandboxReady(ctx, rt, sb.ID, 30*time.Second)
	require.NoError(t, err, "Sandbox failed to become ready")
	assert.Equal(t, "ready", string(sb.State))
	assert.Len(t, sb.ResourceIDs, 1)

	// Test 2: Get sandbox details
	t.Log("Test 2: Getting sandbox details...")
	resources, err := runtime.GetSandboxResources(ctx, rt, sb.ID)
	require.NoError(t, err, "Failed to get resources")
	require.Len(t, resources, 1)

	res := resources[0]
	// Resource is allocated to the sandbox
	assert.Equal(t, provider.StateAllocated, res.Resource.State)
	assert.NotEmpty(t, res.Connection)

	// Verify resource root directory exists
	// NOTE: Due to how resources are managed, we need to use ProviderID (the actual path)
	// rather than reconstructing from resource.ID
	resourceDir := res.Resource.ProviderID
	t.Logf("Resource directory (from ProviderID): %s", resourceDir)

	_, err = os.Stat(resourceDir)
	require.NoError(t, err, "Resource directory should exist")
	t.Logf("Resource directory verified: %s", resourceDir)

	// Verify workspace subdirectory exists
	workspaceDir := filepath.Join(resourceDir, "workspace")
	_, err = os.Stat(workspaceDir)
	require.NoError(t, err, "Workspace directory should exist")
	t.Logf("Workspace directory verified: %s", workspaceDir)

	// Test 3: Verify connection info is available
	t.Log("Test 3: Verifying connection info...")
	assert.NotNil(t, res.Connection.ExtraFields)
	t.Logf("Connection type: %s", res.Connection.Type)

	// Test 4: List sandboxes
	t.Log("Test 4: Listing sandboxes...")
	sandboxes, err := rt.SandboxMgr.List(ctx)
	require.NoError(t, err, "Failed to list sandboxes")
	assert.GreaterOrEqual(t, len(sandboxes), 1, "Should have at least one sandbox")

	found := false
	for _, s := range sandboxes {
		if s.ID == sb.ID {
			found = true
			assert.Equal(t, "e2e-test-sandbox", s.Name)
			break
		}
	}
	assert.True(t, found, "Created sandbox not found in list")

	// Test 5: Destroy sandbox
	t.Log("Test 5: Destroying sandbox...")
	err = rt.SandboxMgr.Destroy(ctx, sb.ID)
	require.NoError(t, err, "Failed to destroy sandbox")

	// Note: Resources are returned to pool, not destroyed, so directory should still exist
	time.Sleep(1 * time.Second)
	_, err = os.Stat(resourceDir)
	assert.NoError(t, err, "Resource directory should still exist (returned to pool)")

	// Verify pool has resources available
	t.Log("Test 6: Verifying pool state...")
	time.Sleep(1 * time.Second)
	pm := rt.Pools["scratch-pool"]
	stats, err := pm.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalAllocated, "No resources should be allocated")
	// Pool may be replenishing or have resources ready
	t.Logf("Pool stats: ready=%d, provisioning=%d, total=%d", stats.TotalReady, stats.TotalProvisioning, stats.Total)

	t.Log("E2E full workflow test PASSED")
}

// TestScratchE2E_MultipleResources tests creating sandboxes with multiple resources
func TestScratchE2E_MultipleResources(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	ctx := context.Background()
	testDir := t.TempDir()
	scratchDir := filepath.Join(testDir, "scratch")
	dbPath := filepath.Join(testDir, "test.db")

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Type: "sqlite",
			Path: dbPath,
		},
		Logging: config.LoggingConfig{Level: "info"},
		Pools: []pool.PoolConfig{
			{
				Name:                "scratch-pool",
				Type:                "process",
				Backend:             "scratch/shell",
				MinReady:            3,
				MaxTotal:            10,
				CPUs:                1,
				MemoryMB:            128,
				HealthCheckInterval: 5 * time.Second,
				ExtraConfig: map[string]interface{}{
					"base_dir": scratchDir,
					"allowed_shells": []interface{}{
						"bash",
						"sh",
					},
				},
			},
		},
	}

	store, err := storage.NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	encryptionKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	_, err = crypto.NewEncryptor(encryptionKey)
	require.NoError(t, err)

	logger := createTestLogger()
	registry := provider.NewRegistry()

	shellProvider := shell.New(logger, shell.Config{
		BaseDir:       scratchDir,
		AllowedShells: []string{"bash", "sh"},
	})
	registry.Register("scratch/shell", shellProvider)

	rt, err := runtime.StartSandboxRuntime(ctx, cfg, registry, store, logger)
	require.NoError(t, err)

	// Start pool managers
	for _, pm := range rt.Pools {
		err = pm.Start()
		require.NoError(t, err)
	}

	// Defer cleanup in correct order
	defer func() {
		for _, pm := range rt.Pools {
			pm.Stop()
		}
		rt.Stop(logger)
	}()

	waitForScratchPoolReady(t, rt.Pools["scratch-pool"], 3)

	// Create sandbox with 3 resources
	t.Log("Creating sandbox with 3 resources...")
	createReq := runtime.CreateSandboxRequest{
		Name:     "multi-resource-sandbox",
		Duration: 10 * time.Minute,
		Resources: []runtime.ResourceRequest{
			{PoolName: "scratch-pool", Count: 3},
		},
	}

	sb, err := runtime.CreateSandbox(ctx, rt, createReq)
	require.NoError(t, err)

	sb, err = runtime.WaitSandboxReady(ctx, rt, sb.ID, 30*time.Second)
	require.NoError(t, err)
	assert.Len(t, sb.ResourceIDs, 3)

	// Verify all resources
	resources, err := runtime.GetSandboxResources(ctx, rt, sb.ID)
	require.NoError(t, err)
	assert.Len(t, resources, 3)

	// Store resource directories for cleanup verification
	var resourceDirs []string
	for i, res := range resources {
		resourceDir := res.Resource.ProviderID
		_, err := os.Stat(resourceDir)
		require.NoError(t, err, fmt.Sprintf("Resource %d directory should exist", i))
		t.Logf("Resource %d directory: %s", i, resourceDir)
		resourceDirs = append(resourceDirs, resourceDir)
	}

	// Destroy sandbox
	err = rt.SandboxMgr.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	// Note: Resources are returned to pool, not destroyed
	time.Sleep(2 * time.Second)
	for i, resourceDir := range resourceDirs {
		_, err := os.Stat(resourceDir)
		assert.NoError(t, err, fmt.Sprintf("Resource %d directory should still exist (returned to pool)", i))
	}

	t.Log("Multiple resources test PASSED")
}

// Helper functions

func waitForScratchPoolReady(t *testing.T, pm *pool.Manager, minReady int) {
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("Timeout waiting for pool to reach min_ready=%d", minReady)
		}

		stats, err := pm.GetStats(ctx)
		if err != nil {
			t.Logf("Error getting stats: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if stats.TotalReady >= minReady {
			t.Logf("Pool ready: %d resources", stats.TotalReady)
			return
		}

		t.Logf("Pool: ready=%d/%d", stats.TotalReady, minReady)
		time.Sleep(1 * time.Second)
	}
}


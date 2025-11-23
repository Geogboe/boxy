package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/allocator"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/crypto"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/docker"
)

// createTestEncryptor creates an encryptor for testing
func createTestEncryptor(t *testing.T) *crypto.Encryptor {
	key, err := crypto.GenerateKey()
	require.NoError(t, err, "Failed to generate test encryption key")
	encryptor, err := crypto.NewEncryptor(key)
	require.NoError(t, err, "Failed to create test encryptor")
	return encryptor
}

// TestDockerE2E_FullLifecycle tests the complete lifecycle with real Docker containers
func TestDockerE2E_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	// Check if Docker is available
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skip("Docker not available:", err)
	}
	defer dockerClient.Close()

	// Ping Docker to ensure it's running
	ctx := context.Background()
	_, err = dockerClient.Ping(ctx)
	if err != nil {
		t.Skip("Docker daemon not running:", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

	// Create real Docker provider with encryption
	encryptor := createTestEncryptor(t)
	dockerProvider, err := docker.NewProvider(logger, encryptor)
	require.NoError(t, err, "Failed to create Docker provider")

	// Create in-memory storage
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err, "Failed to create storage")
	defer store.Close()

	adapter := storage.NewResourceRepositoryAdapter(store)

	// Create pool configuration for Alpine containers (small and fast)
	poolCfg := &pool.PoolConfig{
		Name:                "e2e-alpine-pool",
		Type:                resource.ResourceTypeContainer,
		Backend:             "docker",
		Image:               "alpine:latest",
		MinReady:            2,
		MaxTotal:            5,
		CPUs:                1,
		MemoryMB:            128,
		HealthCheckInterval: 10 * time.Second,
	}

	// Create pool manager with real Docker
	poolManager, err := pool.NewManager(poolCfg, dockerProvider, adapter, logger)
	require.NoError(t, err, "Failed to create pool manager")

	// Start pool manager
	err = poolManager.Start()
	require.NoError(t, err, "Failed to start pool manager")
	defer func() {
		t.Log("Stopping pool manager...")
		poolManager.Stop()
	}()

	// Wait for pool to warm up (real Docker takes time)
	t.Log("Waiting for pool to reach min_ready=2...")
	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("Timeout waiting for pool to warm up")
		}

		stats, err := poolManager.GetStats(ctx)
		if err != nil {
			t.Logf("Error getting stats: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		t.Logf("Pool stats: ready=%d, provisioning=%d, total=%d",
			stats.TotalReady, stats.TotalProvisioning, stats.Total)

		if stats.TotalReady >= 2 {
			t.Log("Pool is ready!")
			break
		}

		time.Sleep(2 * time.Second)
	}

	// Test 1: Allocate a real container
	t.Log("Test 1: Allocating container from warm pool...")
	res, err := poolManager.Allocate(ctx, "e2e-sandbox-1")
	require.NoError(t, err, "Failed to allocate container")
	assert.NotNil(t, res)
	assert.NotEmpty(t, res.ID)
	t.Logf("Allocated container: %s (ProviderID: %s)", res.ID, res.ProviderID)

	// Verify container is running
	containerJSON, err := dockerClient.ContainerInspect(ctx, res.ProviderID)
	require.NoError(t, err, "Failed to inspect container")
	assert.True(t, containerJSON.State.Running, "Container should be running")
	t.Logf("Container %s is running", containerJSON.Name)

	// Test 2: Get connection info
	t.Log("Test 2: Getting connection info...")
	connInfo, err := dockerProvider.GetConnectionInfo(ctx, res)
	require.NoError(t, err, "Failed to get connection info")
	assert.NotNil(t, connInfo)
	assert.NotEmpty(t, connInfo.Password)
	t.Logf("Container connection: %s (password length: %d)", res.ProviderID[:12], len(connInfo.Password))

	// Test 3: Check container health
	t.Log("Test 3: Checking container health...")
	status, err := dockerProvider.GetStatus(ctx, res)
	require.NoError(t, err, "Failed to get status")
	assert.True(t, status.Healthy, "Container should be healthy")
	assert.Equal(t, resource.StateReady, status.State)

	// Test 4: Release container back to pool
	t.Log("Test 4: Releasing container back to pool...")
	err = poolManager.Release(ctx, res.ID)
	require.NoError(t, err, "Failed to release container")

	// Container should still be running (returned to pool)
	containerJSON, err = dockerClient.ContainerInspect(ctx, res.ProviderID)
	require.NoError(t, err, "Container should still exist")
	assert.True(t, containerJSON.State.Running, "Container should still be running after release")

	// Test 5: Verify pool replenishment
	t.Log("Test 5: Verifying pool auto-replenishment...")
	time.Sleep(3 * time.Second)
	stats, err := poolManager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalAllocated, "No containers should be allocated")
	assert.GreaterOrEqual(t, stats.TotalReady, 2, "Pool should maintain min_ready")

	t.Log("E2E lifecycle test PASSED")
}

// TestDockerE2E_MultipleContainers tests allocating multiple containers concurrently
func TestDockerE2E_MultipleContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	// Check if Docker is available
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skip("Docker not available:", err)
	}
	defer dockerClient.Close()

	ctx := context.Background()
	_, err = dockerClient.Ping(ctx)
	if err != nil {
		t.Skip("Docker daemon not running:", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Less verbose for this test

	encryptor := createTestEncryptor(t)
	dockerProvider, err := docker.NewProvider(logger, encryptor)
	require.NoError(t, err)

	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	adapter := storage.NewResourceRepositoryAdapter(store)

	poolCfg := &pool.PoolConfig{
		Name:                "e2e-multi-pool",
		Type:                resource.ResourceTypeContainer,
		Backend:             "docker",
		Image:               "alpine:latest",
		MinReady:            3,
		MaxTotal:            10,
		CPUs:                1,
		MemoryMB:            128,
		HealthCheckInterval: 30 * time.Second,
	}

	poolManager, err := pool.NewManager(poolCfg, dockerProvider, adapter, logger)
	require.NoError(t, err)

	err = poolManager.Start()
	require.NoError(t, err)
	defer poolManager.Stop()

	// Wait for warmup
	t.Log("Waiting for pool warmup...")
	deadline := time.Now().Add(90 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("Timeout waiting for pool")
		}
		stats, _ := poolManager.GetStats(ctx)
		if stats != nil && stats.TotalReady >= 3 {
			break
		}
		time.Sleep(3 * time.Second)
	}

	// Allocate 5 containers
	t.Log("Allocating 5 containers...")
	containers := make([]*resource.Resource, 5)
	for i := 0; i < 5; i++ {
		sandboxID := fmt.Sprintf("e2e-multi-sandbox-%d", i)
		res, err := poolManager.Allocate(ctx, sandboxID)
		require.NoError(t, err, fmt.Sprintf("Failed to allocate container %d", i))
		containers[i] = res
		t.Logf("Allocated container %d: %s", i, res.ProviderID[:12])
	}

	// Verify all containers are running
	t.Log("Verifying all containers are running...")
	for i, res := range containers {
		containerJSON, err := dockerClient.ContainerInspect(ctx, res.ProviderID)
		require.NoError(t, err, fmt.Sprintf("Failed to inspect container %d", i))
		assert.True(t, containerJSON.State.Running, fmt.Sprintf("Container %d should be running", i))
	}

	// Release all containers
	t.Log("Releasing all containers...")
	for i, res := range containers {
		err := poolManager.Release(ctx, res.ID)
		require.NoError(t, err, fmt.Sprintf("Failed to release container %d", i))
	}

	// Verify pool is healthy
	stats, err := poolManager.GetStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalAllocated)
	assert.True(t, stats.Healthy)

	t.Log("Multiple containers test PASSED")
}

// TestDockerE2E_SandboxOrchestration tests full sandbox creation with real Docker
func TestDockerE2E_SandboxOrchestration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test")
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skip("Docker not available:", err)
	}
	defer dockerClient.Close()

	ctx := context.Background()
	_, err = dockerClient.Ping(ctx)
	if err != nil {
		t.Skip("Docker daemon not running:", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	// Setup providers and pools
	encryptor := createTestEncryptor(t)
	dockerProvider, err := docker.NewProvider(logger, encryptor)
	require.NoError(t, err)

	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	adapter := storage.NewResourceRepositoryAdapter(store)

	// Create two different pools (Alpine and BusyBox for variety)
	pool1Cfg := &pool.PoolConfig{
		Name:                "e2e-alpine",
		Type:                resource.ResourceTypeContainer,
		Backend:             "docker",
		Image:               "alpine:latest",
		MinReady:            2,
		MaxTotal:            5,
		CPUs:                1,
		MemoryMB:            128,
		HealthCheckInterval: 30 * time.Second,
	}

	pool2Cfg := &pool.PoolConfig{
		Name:                "e2e-busybox",
		Type:                resource.ResourceTypeContainer,
		Backend:             "docker",
		Image:               "busybox:latest",
		MinReady:            1,
		MaxTotal:            3,
		CPUs:                1,
		MemoryMB:            64,
		HealthCheckInterval: 30 * time.Second,
	}

	pool1Manager, err := pool.NewManager(pool1Cfg, dockerProvider, adapter, logger)
	require.NoError(t, err)
	err = pool1Manager.Start()
	require.NoError(t, err)
	defer pool1Manager.Stop()

	pool2Manager, err := pool.NewManager(pool2Cfg, dockerProvider, adapter, logger)
	require.NoError(t, err)
	err = pool2Manager.Start()
	require.NoError(t, err)
	defer pool2Manager.Stop()

	// Wait for pools to warm up
	t.Log("Waiting for both pools to warm up...")
	deadline := time.Now().Add(120 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("Timeout waiting for pools")
		}
		stats1, _ := pool1Manager.GetStats(ctx)
		stats2, _ := pool2Manager.GetStats(ctx)
		if stats1 != nil && stats1.TotalReady >= 2 &&
			stats2 != nil && stats2.TotalReady >= 1 {
			t.Log("Both pools ready!")
			break
		}
		time.Sleep(3 * time.Second)
	}

	// Create sandbox manager
	pools := map[string]allocator.PoolAllocator{
		"e2e-alpine":  pool1Manager,
		"e2e-busybox": pool2Manager,
	}

	registry := provider_pkg.NewRegistry()
	registry.Register("docker", dockerProvider)

	sandboxRepo := storage.NewSandboxRepositoryAdapter(store)
	resourceRepo := storage.NewResourceRepositoryAdapter(store)

	sandboxManager := sandbox.NewManager(pools, sandboxRepo, resourceRepo, registry, logger)
	sandboxManager.Start()
	defer sandboxManager.Stop()

	// Create a sandbox with containers from both pools
	t.Log("Creating sandbox with mixed containers...")
	req := &sandbox.CreateRequest{
		Name:     "e2e-mixed-sandbox",
		Duration: 10 * time.Minute,
		Resources: []sandbox.ResourceRequest{
			{PoolName: "e2e-alpine", Count: 2},
			{PoolName: "e2e-busybox", Count: 1},
		},
	}

	sb, err := sandboxManager.Create(ctx, req)
	require.NoError(t, err, "Failed to create sandbox")
	assert.Equal(t, 3, len(sb.ResourceIDs), "Should have 3 resources")
	t.Logf("Created sandbox %s with %d containers", sb.ID, len(sb.ResourceIDs))

	// Verify all containers are real and running
	t.Log("Verifying all containers are running...")
	resources, err := sandboxManager.GetResourcesForSandbox(ctx, sb.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(resources))

	for i, resWithConn := range resources {
		containerJSON, err := dockerClient.ContainerInspect(ctx, resWithConn.Resource.ProviderID)
		require.NoError(t, err)
		assert.True(t, containerJSON.State.Running, fmt.Sprintf("Container %d should be running", i))
		assert.NotEmpty(t, resWithConn.Connection.Password, "Should have password")
		t.Logf("Container %d: %s (pool: %s)", i, containerJSON.Name, resWithConn.Resource.PoolID)
	}

	// Destroy sandbox
	t.Log("Destroying sandbox...")
	err = sandboxManager.Destroy(ctx, sb.ID)
	require.NoError(t, err)

	// Verify containers are returned to pool (still running)
	time.Sleep(2 * time.Second)
	stats1, _ := pool1Manager.GetStats(ctx)
	stats2, _ := pool2Manager.GetStats(ctx)
	t.Logf("After destroy - Pool1: ready=%d, Pool2: ready=%d", stats1.TotalReady, stats2.TotalReady)

	t.Log("Sandbox orchestration test PASSED")
}

// TestMain ensures Docker cleanup after all tests
func TestMain(m *testing.M) {
	code := m.Run()

	// Cleanup any leftover Boxy containers after tests complete
	ctx := context.Background()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err == nil {
		// List containers with boxy label
		containers, err := dockerClient.ContainerList(ctx, container.ListOptions{
			All: true,
		})
		if err == nil {
			for _, cont := range containers {
				// Look for containers created by our tests
				for _, name := range cont.Names {
					if len(name) > 0 && (name[:5] == "/boxy" || name[:4] == "boxy") {
						dockerClient.ContainerRemove(ctx, cont.ID, container.RemoveOptions{
							Force:         true,
							RemoveVolumes: true,
						})
					}
				}
			}
		}
		dockerClient.Close()
	}

	os.Exit(code)
}

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/mock"
)

// TestLogger creates a logger for tests
func TestLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Quiet during tests
	if os.Getenv("TEST_VERBOSE") == "1" {
		logger.SetLevel(logrus.DebugLevel)
	}
	return logger
}

// SetupTestStore creates a temporary SQLite store for testing
func SetupTestStore(t *testing.T) storage.Store {
	// Use in-memory SQLite for tests
	// :memory: creates a fresh in-memory database for each test
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err, "Failed to create test store")

	// Verify store was created and migrated successfully by checking tables exist
	// We do this by trying to create a test resource which will fail if tables don't exist
	ctx := context.Background()
	testRes := &provider.Resource{
		ID:           "test-verification-resource",
		PoolID:       "test-pool",
		State:        provider.StateProvisioning,
		Type:         provider.ResourceTypeContainer,
		ProviderType: "mock",
		ProviderID:   "test-provider-id",
	}
	err = store.CreateResource(ctx, testRes)
	require.NoError(t, err, "Failed to verify store initialization - tables may not exist")

	// Clean up verification resource
	_ = store.DeleteResource(ctx, testRes.ID)

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("Failed to close test store: %v", err)
		}
	})

	return store
}

// SetupTestPool creates a test pool configuration
func SetupTestPool(name string, minReady, maxTotal int) *pool.PoolConfig {
	return &pool.PoolConfig{
		Name:                name,
		Type:                provider.ResourceTypeContainer,
		Backend:             "mock",
		Image:               "test-image:latest",
		MinReady:            minReady,
		MaxTotal:            maxTotal,
		CPUs:                1,
		MemoryMB:            128,
		HealthCheckInterval: 100 * time.Millisecond, // Fast for tests
	}
}

// SetupTestPoolManager creates a pool manager with mock provider
func SetupTestPoolManager(t *testing.T, config *pool.PoolConfig, mockCfg *mock.Config) (*pool.Manager, storage.Store) {
	store := SetupTestStore(t)
	logger := TestLogger()

	provider := mock.NewProvider(logger, mockCfg)
	adapter := storage.NewResourceRepositoryAdapter(store)

	manager, err := pool.NewManager(config, provider, adapter, logger)
	require.NoError(t, err, "Failed to create pool manager")

	return manager, store
}

// WaitForCondition waits for a condition to be true with timeout
func WaitForCondition(t *testing.T, timeout time.Duration, check func() bool, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			require.Fail(t, "Timeout waiting for: "+message)
			return
		case <-ticker.C:
			if check() {
				return
			}
		}
	}
}

// WaitForPoolReady waits for pool to reach min_ready
func WaitForPoolReady(t *testing.T, manager *pool.Manager, minReady int) {
	WaitForCondition(t, 10*time.Second, func() bool {
		stats, err := manager.GetStats(context.Background())
		if err != nil {
			return false
		}
		return stats.TotalReady >= minReady
	}, "pool to be ready")
}

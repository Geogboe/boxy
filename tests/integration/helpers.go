package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/provider/mock"
	"github.com/Geogboe/boxy/internal/storage"
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

// SetupTestStore creates an in-memory SQLite store for testing
func SetupTestStore(t *testing.T) storage.Store {
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err, "Failed to create test store")

	t.Cleanup(func() {
		store.Close()
	})

	return store
}

// SetupTestPool creates a test pool configuration
func SetupTestPool(name string, minReady, maxTotal int) *pool.PoolConfig {
	return &pool.PoolConfig{
		Name:                name,
		Type:                resource.ResourceTypeContainer,
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
func SetupTestPoolManager(t *testing.T, config *pool.PoolConfig, mockCfg *mock.Config) *pool.Manager {
	store := SetupTestStore(t)
	logger := TestLogger()

	provider := mock.NewProvider(logger, mockCfg)
	adapter := storage.NewResourceRepositoryAdapter(store)

	manager, err := pool.NewManager(config, provider, adapter, logger)
	require.NoError(t, err, "Failed to create pool manager")

	return manager
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

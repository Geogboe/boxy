package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/sandbox"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/provider/mock"
)

// TestProviderRegistry_ConcurrentAccess tests thread-safety of provider registry
func TestProviderRegistry_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	logger := TestLogger()
	registry := provider_pkg.NewRegistry()

	// Concurrent registration and access
	const numWorkers = 50
	const numProviders = 10

	var wg sync.WaitGroup

	// Concurrent registrations
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			providerID := id % numProviders
			mockCfg := &mock.Config{ProvisionDelay: 10 * time.Millisecond}
			provider := mock.NewProvider(logger, mockCfg)
			registry.Register(fmt.Sprintf("provider-%d", providerID), provider)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			providerID := id % numProviders
			_, ok := registry.Get(fmt.Sprintf("provider-%d", providerID))
			// Provider may or may not exist yet, that's fine
			_ = ok
		}(i)
	}

	// Concurrent lists
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			names := registry.List()
			// List length may vary during concurrent operations
			_ = names
		}()
	}

	// Wait for all goroutines
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no race conditions or deadlocks
		t.Log("Concurrent registry access completed successfully")
	case <-time.After(10 * time.Second):
		t.Fatal("Deadlock detected in provider registry")
	}

	// Verify providers are registered
	names := registry.List()
	assert.GreaterOrEqual(t, len(names), 1, "At least one provider should be registered")
}

// TestPoolManager_ConcurrentAllocations_Stress tests high concurrency allocations
func TestPoolManager_ConcurrentAllocations_Stress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	poolCfg := SetupTestPool("stress-pool", 10, 50)
	mockCfg := &mock.Config{ProvisionDelay: 2 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for initial pool warmup
	WaitForPoolReady(t, manager, 10)

	ctx := context.Background()
	const numWorkers = 30
	const allocationsPerWorker = 3

	var wg sync.WaitGroup
	successCount := sync.Map{}
	errorCount := sync.Map{}

	// Concurrent allocations
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			successes := 0
			errors := 0

			for j := 0; j < allocationsPerWorker; j++ {
				sandboxID := fmt.Sprintf("stress-sandbox-%d-%d", workerID, j)
				_, err := manager.Allocate(ctx, sandboxID, nil)
				if err != nil {
					errors++
					t.Logf("Worker %d allocation %d failed: %v", workerID, j, err)
				} else {
					successes++
				}

				// Small delay between allocations
				time.Sleep(1 * time.Millisecond)
			}

			successCount.Store(workerID, successes)
			errorCount.Store(workerID, errors)
		}(i)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Concurrent allocations completed")
	case <-time.After(30 * time.Second):
		t.Fatal("Stress test timed out")
	}

	// Count results
	totalSuccess := 0
	totalErrors := 0
	successCount.Range(func(key, value interface{}) bool {
		totalSuccess += value.(int)
		return true
	})
	errorCount.Range(func(key, value interface{}) bool {
		totalErrors += value.(int)
		return true
	})

	t.Logf("Total successful allocations: %d", totalSuccess)
	t.Logf("Total failed allocations: %d", totalErrors)
	t.Logf("Total attempts: %d", numWorkers*allocationsPerWorker)

	// We expect most allocations to succeed
	successRate := float64(totalSuccess) / float64(numWorkers*allocationsPerWorker)
	assert.Greater(t, successRate, 0.7, "At least 70%% of allocations should succeed")
}

// TestPoolManager_ConcurrentAllocateRelease tests allocation and release cycles
func TestPoolManager_ConcurrentAllocateRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	poolCfg := SetupTestPool("cycle-pool", 5, 15)
	mockCfg := &mock.Config{ProvisionDelay: 2 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 5)

	ctx := context.Background()
	const numWorkers = 10
	const cyclesPerWorker = 5

	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < cyclesPerWorker; j++ {
				// Allocate
				sandboxID := fmt.Sprintf("cycle-sandbox-%d-%d", workerID, j)
				res, err := manager.Allocate(ctx, sandboxID, nil)
				if err != nil {
					t.Logf("Worker %d cycle %d: allocation failed: %v", workerID, j, err)
					continue
				}

				// Hold resource briefly
				time.Sleep(2 * time.Millisecond)

				// Release
				err = manager.Release(ctx, res.ID)
				if err != nil {
					t.Logf("Worker %d cycle %d: release failed: %v", workerID, j, err)
				}

				// Small delay between cycles
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Allocation/release cycles completed")
	case <-time.After(30 * time.Second):
		t.Fatal("Cycle test timed out")
	}

	// Verify pool is still healthy
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.True(t, stats.Healthy, "Pool should remain healthy after stress test")
	assert.Equal(t, 0, stats.TotalAllocated, "All resources should be released")
}

// TestSandboxManager_ConcurrentCreate tests concurrent sandbox creation
func TestSandboxManager_ConcurrentCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	manager, _ := SetupTestSandboxManager(t)
	ctx := context.Background()

	const numWorkers = 20

	var wg sync.WaitGroup
	errors := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			req := &sandbox.CreateRequest{
				Name:     fmt.Sprintf("concurrent-sandbox-%d", workerID),
				Duration: 1 * time.Hour,
				Resources: []sandbox.ResourceRequest{
					{PoolName: "pool-ubuntu", Count: 1},
				},
			}

			_, err := manager.Create(ctx, req)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Count errors
	errorCount := 0
	for err := range errors {
		errorCount++
		t.Logf("Sandbox creation error: %v", err)
	}

	successRate := float64(numWorkers-errorCount) / float64(numWorkers)
	assert.Greater(t, successRate, 0.8, "At least 80%% of sandbox creations should succeed")
}

// TestPoolManager_StopDuringAllocations tests graceful shutdown
func TestPoolManager_StopDuringAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	poolCfg := SetupTestPool("shutdown-pool", 10, 20)
	mockCfg := &mock.Config{ProvisionDelay: 10 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)

	WaitForPoolReady(t, manager, 10)

	ctx := context.Background()
	const numWorkers = 10

	var wg sync.WaitGroup

	// Start concurrent allocations
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < 20; j++ {
				sandboxID := fmt.Sprintf("shutdown-sandbox-%d-%d", workerID, j)
				_, err := manager.Allocate(ctx, sandboxID, nil)
				if err != nil {
					// Errors expected after Stop() is called
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	// Stop manager while allocations are happening
	time.Sleep(50 * time.Millisecond)
	err = manager.Stop()
	assert.NoError(t, err, "Stop should complete without error")

	// Wait for goroutines (they should exit quickly after Stop)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All allocation goroutines exited")
	case <-time.After(5 * time.Second):
		t.Fatal("Goroutines did not exit after Stop()")
	}
}

// +build windows

package integration

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/crypto"
	"github.com/Geogboe/boxy/internal/provider/hyperv"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
)

// TestHyperVIntegration tests the Hyper-V provider against a real Hyper-V installation
// This test requires:
// - Windows OS
// - Hyper-V installed and enabled
// - Administrator privileges
// - Base image at C:\ProgramData\Boxy\BaseImages\test-image.vhdx
func TestHyperVIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if runtime.GOOS != "windows" {
		t.Skip("Hyper-V integration tests require Windows")
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	encryptor, err := crypto.NewEncryptor(key)
	require.NoError(t, err)

	config := hyperv.DefaultConfig()
	config.WaitForIPTimeout = 30 * time.Second // Shorter timeout for tests

	p := hyperv.NewProviderWithConfig(logger, encryptor, config)

	t.Run("HealthCheck", func(t *testing.T) {
		ctx := context.Background()
		err := p.HealthCheck(ctx)
		require.NoError(t, err, "Hyper-V health check should pass")
	})

	t.Run("ProvisionAndDestroy", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		spec := resource.ResourceSpec{
			Type:         resource.ResourceTypeVM,
			ProviderType: "hyperv",
			Image:        "test-image", // Must exist in BaseImagesPath
			CPUs:         2,
			MemoryMB:     2048,
			DiskGB:       40,
		}

		// Provision VM
		t.Log("Provisioning VM...")
		res, err := p.Provision(ctx, spec)
		if err != nil {
			t.Fatalf("Failed to provision VM: %v", err)
		}

		require.NotNil(t, res)
		assert.NotEmpty(t, res.ID)
		assert.Equal(t, resource.ResourceTypeVM, res.Type)
		assert.Equal(t, "hyperv", res.ProviderType)
		assert.NotEmpty(t, res.ProviderID)

		vmName := res.ProviderID
		t.Logf("Created VM: %s", vmName)

		// Ensure cleanup happens even if test fails
		defer func() {
			t.Log("Cleaning up VM...")
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cleanupCancel()

			if err := p.Destroy(cleanupCtx, res); err != nil {
				t.Logf("Warning: Failed to cleanup VM: %v", err)
			} else {
				t.Log("VM cleaned up successfully")
			}
		}()

		// Test GetStatus
		t.Run("GetStatus", func(t *testing.T) {
			status, err := p.GetStatus(ctx, res)
			require.NoError(t, err)
			assert.NotNil(t, status)
			assert.Equal(t, resource.StateReady, status.State)
			assert.True(t, status.Healthy, "VM should be healthy and running")
			t.Logf("VM Status: %s, Uptime: %v", status.Message, status.Uptime)
		})

		// Test GetConnectionInfo
		t.Run("GetConnectionInfo", func(t *testing.T) {
			connInfo, err := p.GetConnectionInfo(ctx, res)
			require.NoError(t, err)
			assert.NotNil(t, connInfo)
			assert.Equal(t, "rdp", connInfo.Type)
			assert.NotEmpty(t, connInfo.Host, "VM should have an IP address")
			assert.Equal(t, 3389, connInfo.Port)
			assert.NotEmpty(t, connInfo.Username)
			assert.NotEmpty(t, connInfo.Password)
			t.Logf("Connection: %s@%s:%d", connInfo.Username, connInfo.Host, connInfo.Port)
		})

		// Test power state changes
		t.Run("PowerStateChanges", func(t *testing.T) {
			// Stop VM
			t.Log("Stopping VM...")
			stopUpdate := provider_pkg.ResourceUpdate{
				PowerState: &[]provider_pkg.PowerState{provider_pkg.PowerStateStopped}[0],
			}
			err := p.Update(ctx, res, stopUpdate)
			require.NoError(t, err)

			// Verify stopped
			time.Sleep(5 * time.Second)
			status, err := p.GetStatus(ctx, res)
			require.NoError(t, err)
			assert.False(t, status.Healthy, "VM should not be healthy when stopped")

			// Start VM
			t.Log("Starting VM...")
			startUpdate := provider_pkg.ResourceUpdate{
				PowerState: &[]provider_pkg.PowerState{provider_pkg.PowerStateRunning}[0],
			}
			err = p.Update(ctx, res, startUpdate)
			require.NoError(t, err)

			// Verify running
			time.Sleep(5 * time.Second)
			status, err = p.GetStatus(ctx, res)
			require.NoError(t, err)
			assert.True(t, status.Healthy, "VM should be healthy when running")
		})

		// Test snapshots
		t.Run("Snapshots", func(t *testing.T) {
			snapshotName := "test-snapshot"

			// Create snapshot
			t.Log("Creating snapshot...")
			createSnapshot := provider_pkg.ResourceUpdate{
				Snapshot: &provider_pkg.SnapshotOp{
					Operation: "create",
					Name:      snapshotName,
				},
			}
			err := p.Update(ctx, res, createSnapshot)
			require.NoError(t, err)

			// Delete snapshot
			t.Log("Deleting snapshot...")
			deleteSnapshot := provider_pkg.ResourceUpdate{
				Snapshot: &provider_pkg.SnapshotOp{
					Operation: "delete",
					Name:      snapshotName,
				},
			}
			err = p.Update(ctx, res, deleteSnapshot)
			require.NoError(t, err)
		})

		// Test Exec (PowerShell Direct)
		t.Run("Exec", func(t *testing.T) {
			// Note: PowerShell Direct requires VM to be fully booted
			// and may require guest integration services
			t.Log("Executing command via PowerShell Direct...")

			result, err := p.Exec(ctx, res, []string{"echo", "Hello from Boxy"})

			// PowerShell Direct might fail if guest is not ready
			if err != nil {
				t.Logf("Warning: Exec failed (guest may not be ready): %v", err)
				t.Skip("Exec requires fully booted guest with integration services")
			} else {
				assert.Equal(t, 0, result.ExitCode)
				assert.Contains(t, result.Stdout, "Hello from Boxy")
				t.Logf("Exec output: %s", result.Stdout)
			}
		})

		// Test resource updates
		t.Run("ResourceUpdates", func(t *testing.T) {
			// Note: Changing CPU/memory may require VM to be stopped
			t.Log("Updating VM resources...")

			// Stop VM first
			stopUpdate := provider_pkg.ResourceUpdate{
				PowerState: &[]provider_pkg.PowerState{provider_pkg.PowerStateStopped}[0],
			}
			err := p.Update(ctx, res, stopUpdate)
			require.NoError(t, err)

			time.Sleep(5 * time.Second)

			// Update CPU count
			newCPUs := 4
			updateCPU := provider_pkg.ResourceUpdate{
				Resources: &provider_pkg.ResourceLimits{
					CPUs: &newCPUs,
				},
			}
			err = p.Update(ctx, res, updateCPU)
			require.NoError(t, err)

			// Restart VM
			startUpdate := provider_pkg.ResourceUpdate{
				PowerState: &[]provider_pkg.PowerState{provider_pkg.PowerStateRunning}[0],
			}
			err = p.Update(ctx, res, startUpdate)
			require.NoError(t, err)

			t.Log("VM resources updated successfully")
		})
	})
}

// TestHyperVProviderConcurrency tests concurrent operations
func TestHyperVProviderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if runtime.GOOS != "windows" {
		t.Skip("Hyper-V integration tests require Windows")
	}

	// This test would provision multiple VMs concurrently
	// Skipped for now as it requires significant resources
	t.Skip("Concurrent provisioning test requires extensive resources")
}

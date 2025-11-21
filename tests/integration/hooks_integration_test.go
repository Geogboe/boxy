package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/hooks"
	"github.com/Geogboe/boxy/internal/provider/mock"
)

func TestPoolManager_Integration_HooksAfterProvision(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup pool with after_provision hooks
	poolCfg := SetupTestPool("test-pool-hooks", 2, 5)
	poolCfg.Hooks = hooks.HookConfig{
		AfterProvision: []hooks.Hook{
			{
				Name:   "validate-network",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Validating network: ${resource.ip}'",
			},
			{
				Name:   "setup-monitoring",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Setting up monitoring for ${resource.id}'",
			},
		},
	}

	mockCfg := &mock.Config{
		ProvisionDelay: 50 * time.Millisecond,
	}

	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	// Start the pool manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for pool to provision resources (hooks should execute)
	WaitForPoolReady(t, manager, 2)

	// Verify pool stats - resources should be ready (hooks succeeded)
	stats, err := manager.GetStats(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalReady, 2)
	assert.True(t, stats.Healthy)
}

func TestPoolManager_Integration_HooksBeforeAllocate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup pool with before_allocate hooks
	poolCfg := SetupTestPool("test-pool-allocate-hooks", 2, 5)
	poolCfg.Hooks = hooks.HookConfig{
		BeforeAllocate: []hooks.Hook{
			{
				Name:   "create-user",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Creating user ${username} with password ${password}'",
			},
			{
				Name:   "set-hostname",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Setting hostname for resource ${resource.id}'",
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 2)

	ctx := context.Background()

	// Allocate a resource (should trigger before_allocate hooks)
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)
	assert.NotNil(t, res)

	// Check that personalization hooks were recorded in metadata
	assert.Contains(t, res.Metadata, "personalization_hooks")
	assert.Contains(t, res.Metadata, "allocated_username")
}

func TestPoolManager_Integration_HooksBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup pool with both hook types
	poolCfg := SetupTestPool("test-pool-both-hooks", 2, 5)
	poolCfg.Hooks = hooks.HookConfig{
		AfterProvision: []hooks.Hook{
			{
				Name:    "finalization",
				Type:    hooks.HookTypeScript,
				Shell:   hooks.ShellBash,
				Inline:  "echo 'Finalization for ${resource.id}'",
				Timeout: 1 * time.Minute,
			},
		},
		BeforeAllocate: []hooks.Hook{
			{
				Name:    "personalization",
				Type:    hooks.HookTypeScript,
				Shell:   hooks.ShellBash,
				Inline:  "echo 'Personalizing for user ${username}'",
				Timeout: 30 * time.Second,
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for initial provisioning (after_provision hooks execute)
	WaitForPoolReady(t, manager, 2)

	ctx := context.Background()

	// Allocate (before_allocate hooks execute)
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)

	// Both hook types should have executed
	assert.Contains(t, res.Metadata, "finalization_hooks")
	assert.Contains(t, res.Metadata, "personalization_hooks")
}

func TestPoolManager_Integration_HooksWithRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool-retry", 1, 3)
	poolCfg.Hooks = hooks.HookConfig{
		AfterProvision: []hooks.Hook{
			{
				Name:   "flaky-hook",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Attempting operation...'", // Mock provider always succeeds
				Retry:  2, // Allow 2 retries
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Should succeed (mock provider doesn't fail)
	WaitForPoolReady(t, manager, 1)

	stats, err := manager.GetStats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalReady)
}

func TestPoolManager_Integration_HooksContinueOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool-continue", 1, 3)
	poolCfg.Hooks = hooks.HookConfig{
		BeforeAllocate: []hooks.Hook{
			{
				Name:              "optional-step",
				Type:              hooks.HookTypeScript,
				Shell:             hooks.ShellBash,
				Inline:            "echo 'Optional operation'",
				ContinueOnFailure: true, // Don't fail allocation if this fails
			},
			{
				Name:   "required-step",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Required operation'",
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 1)

	ctx := context.Background()

	// Allocation should succeed even if optional hook fails (mock doesn't fail though)
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)
	assert.NotNil(t, res)
}

func TestPoolManager_Integration_HooksTemplateExpansion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool-templates", 1, 3)
	poolCfg.Hooks = hooks.HookConfig{
		BeforeAllocate: []hooks.Hook{
			{
				Name:   "test-templates",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Resource: ${resource.id}, User: ${username}, Pool: ${pool.name}'",
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	WaitForPoolReady(t, manager, 1)

	ctx := context.Background()

	// Allocate to trigger template expansion
	res, err := manager.Allocate(ctx, "sandbox-1")
	require.NoError(t, err)

	// Verify hook executed (check metadata)
	hookResults, ok := res.Metadata["personalization_hooks"]
	require.True(t, ok, "personalization_hooks should be in metadata")

	results, ok := hookResults.([]hooks.HookResult)
	require.True(t, ok, "hook results should be []HookResult")
	require.Len(t, results, 1, "should have 1 hook result")

	// Hook should have succeeded (template expansion doesn't fail)
	assert.True(t, results[0].Success)
}

func TestPoolManager_Integration_HooksTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool-timeout", 1, 3)
	poolCfg.Hooks = hooks.HookConfig{
		AfterProvision: []hooks.Hook{
			{
				Name:    "quick-hook",
				Type:    hooks.HookTypeScript,
				Shell:   hooks.ShellBash,
				Inline:  "echo 'Quick operation'",
				Timeout: 10 * time.Second, // Generous timeout
			},
		},
	}
	// Set short finalization timeout
	poolCfg.Timeouts.Finalization = 30 * time.Second

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Should succeed (mock completes quickly)
	WaitForPoolReady(t, manager, 1)

	stats, err := manager.GetStats(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalReady, 1)
}

func TestPoolManager_Integration_HooksMultipleShellTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	poolCfg := SetupTestPool("test-pool-shells", 1, 3)
	poolCfg.Hooks = hooks.HookConfig{
		AfterProvision: []hooks.Hook{
			{
				Name:   "bash-hook",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellBash,
				Inline: "echo 'Bash script'",
			},
			{
				Name:   "python-hook",
				Type:   hooks.HookTypeScript,
				Shell:  hooks.ShellPython,
				Inline: "print('Python script')",
			},
		},
	}

	mockCfg := &mock.Config{ProvisionDelay: 50 * time.Millisecond}
	manager, _ := SetupTestPoolManager(t, poolCfg, mockCfg)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Both hooks should execute successfully
	WaitForPoolReady(t, manager, 1)

	stats, err := manager.GetStats(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalReady, 1)
}

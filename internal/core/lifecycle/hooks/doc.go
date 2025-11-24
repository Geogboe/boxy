// Package hooks provides resource lifecycle hook execution.
//
// Hooks are scripts that run at specific points during a resource's lifecycle,
// allowing customization and automation of provisioning and allocation steps.
//
// # Hook Points
//
// AfterProvision: Runs after provider.Provision() during pool warming.
// Purpose: Slow, background finalization tasks (network validation, optional setup).
//
// BeforeAllocate: Runs before allocating a resource to a user.
// Purpose: Fast personalization tasks (create user account, set hostname).
//
// # Execution Model
//
// Hooks execute with:
//   - Individual hook timeouts (default: 5 minutes)
//   - Phase-level timeouts (finalization: 10 min, personalization: 30 sec)
//   - Retry logic with configurable attempts
//   - Optional continue-on-failure semantics
//
// Template variables are expanded before execution, providing context like
// resource IDs, IP addresses, credentials, and custom metadata.
//
// # Example
//
//	executor := hooks.NewExecutor(logger)
//	results, err := executor.ExecuteHooks(
//		ctx,
//		poolConfig.Hooks.AfterProvision,
//		hooks.HookPointAfterProvision,
//		provider,
//		resource,
//		hookContext,
//		finalizationTimeout,
//	)
package hooks

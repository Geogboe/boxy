// Package sandbox provides sandbox lifecycle management.
//
// A sandbox is a user-created environment consisting of one or more allocated
// resources from configured pools. Sandboxes have a defined duration and are
// automatically cleaned up after expiration.
//
// # Lifecycle
//
// Sandbox states progress through:
//
//	StateCreating → StateReady → StateExpiring → StateDestroyed
//	             ↘ StateError
//
// Creation is asynchronous: Create() returns immediately with a sandbox in
// StateCreating, while resource allocation happens in the background.
// Use WaitForReady() to block until resources are allocated.
//
// # Resource Allocation
//
// The Manager coordinates with pool allocators to provision resources:
//
//  1. Create sandbox record (StateCreating)
//  2. Launch async goroutine for resource allocation
//  3. For each requested resource:
//     - Request allocation from appropriate pool
//     - Pool runs before_allocate hooks (personalization)
//     - Mark resource as allocated
//  4. Update sandbox to StateReady
//
// # Cleanup
//
// A background cleanup worker runs every 30 seconds, destroying sandboxes
// past their expiration time. Sandboxes enter StateExpiring before final
// StateDestroyed to allow graceful teardown.
//
// # Example
//
//	manager := sandbox.NewManager(poolAllocators, sandboxRepo, resourceRepo, providerRegistry, logger)
//	manager.Start()
//	defer manager.Stop()
//
//	sandbox, err := manager.Create(ctx, &sandbox.CreateRequest{
//		Name: "dev-environment",
//		Resources: []sandbox.ResourceRequest{
//			{PoolName: "docker-pool", Count: 1},
//		},
//		Duration: 30 * time.Minute,
//	})
//
//	sandbox, err = manager.WaitForReady(ctx, sandbox.ID, 5*time.Minute)
package sandbox

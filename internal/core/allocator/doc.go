// Package allocator provides lightweight orchestration between pools and sandboxes.
//
// The Allocator acts as a routing layer, abstracting pool internals from the
// sandbox manager. It coordinates multi-pool resource allocation for a single
// sandbox and routes release operations back to the correct pool.
//
// # Design
//
// This package intentionally maintains minimal responsibility:
//   - Maps pool names to pool allocator interfaces
//   - Routes allocation requests to appropriate pool
//   - Routes release requests to appropriate pool
//   - No business logic beyond routing
//
// The thin design allows the sandbox manager to work with multiple pools
// without knowing pool implementation details.
//
// # Example
//
//	allocators := map[string]allocator.PoolAllocator{
//		"docker-pool": dockerPoolManager,
//		"vm-pool":     vmPoolManager,
//	}
//
//	alloc := allocator.NewAllocator(allocators, resourceRepo)
//
//	// Allocate from specific pool
//	resource, err := alloc.Allocate(ctx, "docker-pool", sandboxID)
//
//	// Release back to its pool
//	err = alloc.ReleaseResource(ctx, resource.ID)
package allocator

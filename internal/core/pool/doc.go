// Package pool provides resource pool management with warm resource provisioning.
//
// Pools maintain a configurable number of ready resources (min_ready), enabling
// instant allocation to sandboxes. Resources progress through lifecycle states
// with automated background workers for replenishment and health checking.
//
// # Resource Lifecycle
//
// Resource states:
//
//	StateProvisioning → StateReady → StateAllocated → StateDestroying → StateDestroyed
//	                 ↘ StateError
//
// Hooks execute at key points:
//   - after_provision: Finalization tasks during pool warming (slow, async)
//   - before_allocate: Personalization tasks before user gets resource (fast, sync)
//
// # Background Workers
//
// Pool managers run two background goroutines:
//
// Replenishment Worker (10s interval):
//   - Ensures min_ready count is maintained
//   - Provisions new resources as needed
//   - Runs after_provision hooks for finalization
//
// Health Check Worker (configurable interval):
//   - Validates ready resources are still healthy
//   - Marks unhealthy resources as StateError
//   - Triggers replenishment for failed resources
//
// # Configuration
//
//	pool:
//	  name: docker-pool
//	  backend: docker
//	  min_ready: 3
//	  max_resources: 10
//	  health_check_interval: 30s
//	  hooks:
//	    after_provision:
//	      - name: network-validation
//	        shell: bash
//	        inline: "ping -c 1 8.8.8.8"
//	    before_allocate:
//	      - name: create-user
//	        shell: bash
//	        inline: "useradd ${username}"
//
// # Example
//
//	manager, err := pool.NewManager(&config, provider, resourceRepo, logger)
//	if err != nil {
//		log.Fatal(err)
//	}
//	if err := manager.Start(); err != nil {
//		log.Fatal(err)
//	}
//	defer manager.Stop()
//
//	// Allocate a resource
//	resource, err := manager.Allocate(ctx, sandboxID)
//
//	// Release when done
//	err = manager.Release(ctx, resource.ID)
package pool

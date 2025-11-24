// Package storage provides persistence abstractions and SQLite implementation.
//
// This package uses the repository pattern to abstract data persistence,
// allowing core business logic to remain independent of storage details.
//
// # Interfaces
//
// Store: Combines ResourceRepository and SandboxRepository interfaces.
//
// ResourceRepository: CRUD and queries for provider resources.
//   - Create, Update, Delete, GetByID
//   - Query by pool, state, sandbox
//   - Count resources by pool and state
//
// SandboxRepository: CRUD and queries for sandboxes.
//   - Create, Update, Delete, GetByID
//   - List all or active sandboxes
//   - Find expired sandboxes for cleanup
//
// # Adapters
//
// Storage adapters translate between the Store interface and domain-specific
// repository interfaces required by managers:
//
//	ResourceRepositoryAdapter: Store → pool.ResourceRepository
//	SandboxRepositoryAdapter:  Store → sandbox.SandboxRepository
//
// # Implementation
//
// SQLiteStore provides a modernc.org/sqlite-based implementation using
// database/sql with:
//   - Two tables: resources, sandboxes
//   - Automatic schema creation
//   - JSON encoding for complex fields (resource_ids, metadata, specs)
//   - Indexes for common queries
//
// # Example
//
//	store, err := storage.NewSQLiteStore("~/.config/boxy/boxy.db")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer store.Close()
//
//	// Use directly
//	resource, err := store.GetResourceByID(ctx, resourceID)
//
//	// Or adapt for managers
//	resourceRepo := storage.NewResourceRepositoryAdapter(store)
//	poolManager := pool.NewManager(config, provider, resourceRepo, logger)
package storage

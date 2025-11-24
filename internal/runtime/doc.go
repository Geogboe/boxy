// Package runtime provides application composition and lifecycle management.
//
// This package wires together all components of boxy (storage, providers, pools,
// sandbox manager, HTTP API) and manages the application's startup and shutdown.
//
// # Responsibilities
//
//   - Initialize storage (SQLite) and encryption
//   - Build provider registry from configuration
//   - Register remote agents
//   - Create and start pool managers
//   - Create and start sandbox manager
//   - Start HTTP API server (if enabled)
//   - Coordinate graceful shutdown
//
// # Runtime Structure
//
// The Runtime struct holds references to all running managers and provides
// a Stop() method for coordinated shutdown in the correct order:
//
//  1. Stop API server
//  2. Stop sandbox manager (halts cleanup worker)
//  3. Stop pool managers (halts replenishment/health check workers)
//  4. Close storage
//
// # Usage
//
//	runtime, err := runtime.Start(ctx, config, logger)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer runtime.Stop(logger)
//
// # Package Organization
//
//   - runtime.go: Main Runtime type and Start/Stop functions
//   - providers.go: Provider registry construction
//   - agents.go: Remote agent registration and agent bootstrap
//   - storage.go: Storage initialization helpers
//   - sandbox.go: Lightweight sandbox runtime for CLI commands
package runtime

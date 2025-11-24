// Package httpapi provides a REST HTTP API for boxy sandbox and pool operations.
//
// # Endpoints
//
// Sandboxes:
//   - POST   /sandboxes           Create a sandbox
//   - GET    /sandboxes           List all sandboxes
//   - GET    /sandboxes/{id}      Get sandbox details
//   - DELETE /sandboxes/{id}      Destroy a sandbox
//   - PATCH  /sandboxes/{id}      Extend sandbox duration
//   - GET    /sandboxes/{id}/resources  Get sandbox resources with connection info
//
// Pools:
//   - GET    /pools               List all pools with stats
//
// Health:
//   - GET    /health              Health check endpoint
//
// # Architecture
//
// The API uses an adapter pattern to decouple HTTP concerns from core business logic:
//
//	SandboxService interface   → SandboxManagerAdapter
//	PoolStatsProvider interface → PoolStatsAdapter
//
// Adapters translate between HTTP API contracts and internal manager implementations,
// allowing core packages to remain independent of HTTP details.
//
// # Server
//
// The Server provides graceful shutdown support via context cancellation:
//
//	server := http.NewServer(addr, sandboxService, poolStatsProvider, logger, timeouts)
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	go func() {
//		if err := server.Run(ctx); err != nil {
//			logger.Error(err)
//		}
//	}()
//
// # Configuration
//
//	api:
//	  enabled: true
//	  listen: ":8080"
//	  read_timeout_secs: 10
//	  write_timeout_secs: 10
//	  idle_timeout_secs: 60
package httpapi

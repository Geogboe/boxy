// Package providers contains helpers to build and return a populated provider
// registry for CLI and server entry points.
//
// Overview
//
// This small internal package inspects pool configs, decides which provider
// implementations are needed, and constructs them. It performs lightweight
// availability checks where appropriate (for example: Docker health checks)
// and registers only backends that are available on the host environment.
//
// Responsibilities
//
// - Map pool backends to provider constructors and create provider instances
// - Honor per-pool `extra_config` for provider-specific overrides
// - Perform minimal runtime checks (availability, health) and skip unavailable
//   providers instead of failing the bootstrap
//
// Non-goals
//
// This package intentionally avoids application-level concerns such as CLI
// flag parsing, persistence, or pool orchestration. It is only responsible for
// creating the provider registry used by consumers.
//
// Security
//
// Provider instances created by this package should be configured with secure
// settings by higher-level callers and must follow the provider-specific
// guidance for secret management.
package providers

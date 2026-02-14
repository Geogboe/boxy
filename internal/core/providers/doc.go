// Package providers defines the extension seam for talking to external providers.
//
// Documentation pillars for this package:
// 1) What: Driver is the code plugin for one ProviderType (docker/hyperv/...).
// 2) Why: It keeps provider-specific IO/behavior out of Boxy's core.
// 3) When: Add a Driver when introducing a new external system type.
// 4) How: Keep the base interface small; add optional capability interfaces.
//
// Vocabulary (this is where OO instincts commonly collide with Go):
// - Provider (internal/core/model.Provider) is configured data representing an external
//   system instance (e.g., "docker-local", "hyperv-lab").
// - Driver (this package) is code that knows how to talk to a ProviderType.
//
// Layout:
// - internal/core/providers/registry.go: runtime mapping ProviderType -> Driver
// - internal/core/providers/<type>/: per-type driver implementation (typed config + validation)
// - internal/config/schema/providers/<type>.config.schema.json: JSON Schema for
//   Provider.Config editor UX (runtime remains authoritative via drivers)
package providers

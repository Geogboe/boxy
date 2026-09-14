package sandbox

import (
	"context"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// SandboxAllocator is called when resources are allocated to a sandbox.
// It runs provider-level allocation hooks and returns additional Properties
// to merge into the resource at allocation time.
//
// This is separate from pool.Provisioner, which handles supply-side lifecycle
// (creating and destroying resources in the pool).
type SandboxAllocator interface {
	Allocate(ctx context.Context, pool model.Pool, res model.Resource) (providersdk.AllocationResult, error)
}

// PackageSandboxAllocator is an optional capability for allocators that can
// apply allocation-scoped resource packages while retaining the base
// allocator contract for existing providers and embedders.
type PackageSandboxAllocator interface {
	SandboxAllocator
	AllocateWithPackages(ctx context.Context, pool model.Pool, res model.Resource, packages []string) (providersdk.AllocationResult, error)
}

// NetworkIsolatingAllocator is an optional capability for allocators whose
// underlying agent/driver supports per-sandbox network isolation
// (providersdk.NetworkIsolator, via agentsdk.NetworkIsolatingAgent). Not
// every provider implements it (devfactory deliberately does not -- see
// the design spec's Decision 1), so Manager must type-assert.
//
// CreateSegment also returns the resolved provider type (needed by Manager
// to record model.NetworkSegment.ProviderType for later destroy calls,
// which have no model.Pool/model.Resource in scope to re-resolve it from).
type NetworkIsolatingAllocator interface {
	SandboxAllocator
	CreateSegment(ctx context.Context, pool model.Pool, res model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error)
	AttachToSegment(ctx context.Context, pool model.Pool, res model.Resource, ref providersdk.SegmentRef) error
}

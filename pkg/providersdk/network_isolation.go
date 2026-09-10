package providersdk

import "context"

// SegmentRef is an opaque, provider-defined identifier for a per-sandbox
// network segment (a switch name for Hyper-V, a network ID for Docker).
// Callers never interpret it — they hold onto whatever CreateSegment
// returns and pass it back to AttachToSegment/DestroySegment unchanged.
type SegmentRef string

// NetworkIsolator is an optional provider capability for per-sandbox
// network isolation. Not every driver implements it — callers that care
// must type-assert, the same pattern as AvailabilityReporter,
// ResourceLister, GuestPersonalizer, and NetworkRangeReporter.
//
// Segment creation cannot happen at Driver.Create time: Boxy provisions
// resources into a pool's ready inventory ahead of any sandbox (preheat),
// and a sandbox later claims an already-created resource via a separate
// allocation step. CreateSegment is therefore called once, on a sandbox's
// first resource claim; AttachToSegment moves each claimed resource off
// whatever pool-level network it was created on and onto the sandbox's
// segment, at allocation time. AttachToSegment must be fast — a single
// lightweight reconnect, not a provisioning-style operation — because
// sandboxes need to be ready near-instantly.
type NetworkIsolator interface {
	// CreateSegment creates a new, empty private network segment for the
	// given sandbox. Called once per sandbox, on its first resource claim.
	CreateSegment(ctx context.Context, sandboxID string) (SegmentRef, error)

	// AttachToSegment moves an already-created resource (identified by the
	// driver's own provider-specific resource ID, as returned in
	// Resource.ID from Driver.Create) onto the given segment.
	AttachToSegment(ctx context.Context, providerResourceID string, ref SegmentRef) error

	// DestroySegment tears down a segment created by CreateSegment. Must be
	// idempotent for an already-gone segment, matching Driver.Delete's
	// idempotency contract.
	DestroySegment(ctx context.Context, ref SegmentRef) error
}

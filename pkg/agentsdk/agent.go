// Package agentsdk defines the contract between the Boxy server and agents.
//
// An agent is the communications layer for one or more provider drivers.
// The server talks to agents — never to drivers directly. Whether the
// agent is embedded (in-process) or remote (gRPC) is transparent to the
// server; both implement the same Agent interface.
//
// Lifecycle:
//
//  1. Agent starts and registers with the server (token-based auth)
//  2. Agent advertises which provider types it supports
//  3. Server routes CRUD requests to agents based on provider type
//  4. Agent dispatches to the appropriate local driver
package agentsdk

import (
	"context"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// Agent is the interface the server uses to communicate with any agent,
// whether embedded or remote. It wraps one or more provider drivers and
// routes CRUD operations to them.
type Agent interface {
	// Info returns the agent's identity and the providers it supports.
	Info() AgentInfo

	// Create provisions a resource through the named provider.
	Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error)

	// Read returns the current status of a resource.
	Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error)

	// Update performs an operation on an existing resource.
	Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error)

	// Delete destroys a resource. It follows the providersdk.Driver Delete
	// contract: deleting an already-missing provider resource is successful.
	Delete(ctx context.Context, provider providersdk.Type, id string) error

	// Allocate runs allocation-time hooks on an existing resource and returns
	// additional Properties to merge. Returns nil, nil if the provider has no
	// allocation work to perform.
	Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error)
}

// StreamingAgent is an optional capability for agents that can carry live
// provider events back to the server.
type StreamingAgent interface {
	UpdateStream(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation, sink eventstream.Sink) (*providersdk.Result, error)
}

// GuestPersonalizingAgent is an optional agent capability for providers that
// expose the typed guest-personalization contract. opts.ApplyNetwork
// distinguishes allocation-time personalization (true) from admission/
// promotion-time personalization (false) — see
// providersdk.GuestPersonalizationOptions.
type GuestPersonalizingAgent interface {
	PersonalizeGuest(ctx context.Context, provider providersdk.Type, id string, opts providersdk.GuestPersonalizationOptions) (*providersdk.GuestPersonalizationResult, error)
}

// NetworkIsolatingAgent is an optional agent capability for providers that
// implement providersdk.NetworkIsolator. Unlike GuestPersonalizingAgent
// (which degrades to nil, nil for an unsupported driver so callers fall
// back to the generic Allocate path), there is no fallback here: a caller
// that reaches CreateSegment/AttachToSegment/DestroySegment already
// type-asserted for this capability specifically, so an unsupported
// driver is a caller bug, not an expected degrade path — it should error.
//
// A SegmentRef is only meaningful to the agent that returned it. A segment is
// a host-local object — a vSwitch on one specific Hyper-V host, a network on
// one specific Docker daemon — so all three calls for a given segment
// (CreateSegment, then every later AttachToSegment/DestroySegment against the
// ref it returned) must be routed to the same agent instance that created it.
// Re-resolving by provider type is not sufficient: resolution round-robins
// across agents advertising the same type, so a second call could land on a
// different host where the segment simply does not exist. This is the same
// per-agent-provenance constraint model.ProviderRef.AgentID already codifies
// for regular resource operations — see
// docs/adr/0005-remote-agent-transport-and-registration.md.
type NetworkIsolatingAgent interface {
	// CreateSegment creates a new, empty private network segment for the
	// given sandbox on the agent's driver for provider.
	//
	// Idempotent per sandboxID: repeated calls for the same sandbox return
	// the same SegmentRef without erroring, rather than creating a second
	// segment. Callers therefore retry a failed or interrupted CreateSegment
	// freely; an implementation that created a fresh segment each time would
	// strand the earlier ones, since only the ref the caller ends up holding
	// is ever passed to DestroySegment. This mirrors
	// providersdk.NetworkIsolator.CreateSegment's contract, which is where a
	// driver actually has to honor it.
	//
	// An empty SegmentRef returned without an error is not currently rejected
	// by either agent implementation: EmbeddedAgent returns whatever the
	// driver gave it and RemoteAgent returns whatever arrived on the wire,
	// both verbatim. Validating that case is deliberately deferred to the
	// caller (Plan 1c), which is the layer that decides whether to persist a
	// ref and what to do when one is unusable.
	CreateSegment(ctx context.Context, provider providersdk.Type, sandboxID string) (providersdk.SegmentRef, error)

	// AttachToSegment moves an already-created resource onto a segment. Must
	// be routed to the agent that returned ref.
	AttachToSegment(ctx context.Context, provider providersdk.Type, providerResourceID string, ref providersdk.SegmentRef) error

	// DestroySegment tears down a segment created by CreateSegment. Must be
	// routed to the agent that returned ref, and is idempotent for an
	// already-gone segment.
	DestroySegment(ctx context.Context, provider providersdk.Type, ref providersdk.SegmentRef) error
}

// ResourceListingAgent is an optional agent capability for providers whose
// underlying driver implements providersdk.ResourceLister. Not every driver
// supports enumeration, so callers must type-assert for this rather than
// relying on it being part of Agent.
type ResourceListingAgent interface {
	List(ctx context.Context, provider providersdk.Type) ([]providersdk.ResourceStatus, error)
}

// AvailabilitySnapshot is one agent's most recently received per-provider
// providersdk.AvailabilityReporter sample, plus when the server received it.
// At is stamped by the server on receipt, not taken from the agent's
// self-reported clock — the same trust boundary that keeps liveness keyed to
// the authenticated connection rather than a claimed value in the message.
//
// A provider type absent from Data means "no reporter, a reporter error, or
// a sampling timeout" on the most recent heartbeat — never "zero
// availability". Callers must not conflate the two.
type AvailabilitySnapshot struct {
	Data map[providersdk.Type]providersdk.ResourceAvailability
	At   time.Time
}

// AvailabilityReportingAgent is an optional agent capability for exposing
// the latest AvailabilitySnapshot received over an agent's heartbeat stream.
// Only RemoteAgent implements this today: EmbeddedAgent has no heartbeat to
// carry a snapshot on, and querying its local drivers live is a different
// (ctx-bound, error-returning) operation a future caller can add separately
// if it needs one — see #178 and #179. Callers must type-assert for this
// capability rather than relying on it being part of Agent.
type AvailabilityReportingAgent interface {
	// Availability returns the latest snapshot and whether one has been
	// received yet. Each heartbeat wholly replaces the previous snapshot,
	// including replacing it with an empty one — a reporter that starts
	// erroring is reflected as missing data on the next heartbeat, not
	// stale-but-plausible leftover numbers.
	Availability() (AvailabilitySnapshot, bool)
}

// AgentInfo describes an agent and the providers it hosts.
type AgentInfo struct {
	// ID is a unique identifier for this agent instance.
	ID string

	// Name is a human-readable label (e.g. "docker-host-1", "lab-hypervisor").
	Name string

	// Providers lists the provider types this agent can handle.
	Providers []providersdk.Type

	// NetworkIsolatingProviders is the subset of Providers whose driver on
	// this agent actually implements providersdk.NetworkIsolator. Empty or
	// nil means none — the correct default for an agent hosting only
	// non-isolating drivers (a devfactory-only agent, say).
	//
	// It exists because NetworkIsolatingAgent is implemented
	// unconditionally by both EmbeddedAgent and RemoteAgent, so a
	// type-assertion for that capability tells a caller nothing about
	// whether the driver behind it can isolate anything. For a remote
	// agent the daemon cannot type-assert the real driver at all. Without
	// this advertisement the control plane only learns "unsupported" as a
	// hard error from deep inside a CreateSegment call it had already
	// committed to. Callers consult this BEFORE asking an agent to create a
	// segment, and treat a provider's absence as "skip isolation for this
	// resource", not as a failure.
	//
	// Both agent implementations compute it the same way, from their own
	// local drivers, via NetworkIsolatingProviderTypes: EmbeddedAgent at
	// construction, a remote agent at registration (carried in
	// RegisterRequest.network_isolating_provider_types and filtered against
	// Providers server-side).
	NetworkIsolatingProviders []providersdk.Type
}

// NetworkIsolatingProviderTypes returns the subset of providers whose driver
// in drivers implements providersdk.NetworkIsolator — the canonical way to
// compute AgentInfo.NetworkIsolatingProviders, shared by EmbeddedAgent's
// constructor and the remote agent client's registration frame so the two
// can never drift apart.
//
// It iterates providers (an ordered slice) and looks each one up in drivers,
// rather than ranging drivers directly, so the result is deterministic:
// Go map iteration order is randomized, and this value goes both into wire
// frames and into test assertions. A provider with no driver entry is
// skipped rather than treated as isolating.
func NetworkIsolatingProviderTypes(drivers DriverSet, providers []providersdk.Type) []providersdk.Type {
	isolating := make([]providersdk.Type, 0, len(providers))
	for _, p := range providers {
		d, ok := drivers[p]
		if !ok {
			continue
		}
		if _, ok := d.(providersdk.NetworkIsolator); ok {
			isolating = append(isolating, p)
		}
	}
	if len(isolating) == 0 {
		return nil
	}
	return isolating
}

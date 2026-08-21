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
// expose the typed guest-personalization contract.
type GuestPersonalizingAgent interface {
	PersonalizeGuest(ctx context.Context, provider providersdk.Type, id string) (*providersdk.GuestPersonalizationResult, error)
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
}

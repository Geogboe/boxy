package pool

import (
	"errors"
	"slices"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// ErrNetworkIsolationUnsupported is the sentinel AgentProvisioner.CreateSegment
// returns when the agent that owns a resource does not advertise
// providersdk.NetworkIsolator support for that resource's resolved provider
// type (see agentsdk.AgentInfo.NetworkIsolatingProviders).
//
// It is not a failure. Per-sandbox network isolation is best-effort across a
// heterogeneous fleet: a sandbox can mix resources from an isolation-capable
// Hyper-V or Docker pool with resources from a provider that has no such
// concept at all. internal/sandbox.Manager.ensureNetworkSegment matches this
// with errors.Is and skips segment creation for that resource, recording no
// segment; every other error from CreateSegment stays a hard failure.
//
// Why it lives in internal/pool rather than in internal/sandbox, which is
// where the sandbox.NetworkIsolatingAllocator contract this value is part of
// is actually declared: internal/sandbox already imports internal/pool (see
// internal/sandbox/fulfiller.go), so the reverse edge would be an import
// cycle. The same constraint is documented on
// internal/pool/provisioner_agent_isolation_test.go, which has to be an
// external test package for exactly this reason. Producing package owns the
// sentinel; the consuming package references it.
var ErrNetworkIsolationUnsupported = errors.New("agent does not advertise network isolation for this provider type")

// ErrSegmentAgentUnavailable is the sentinel AgentProvisioner.DestroySegment
// returns when the agent recorded on a model.NetworkSegment is no longer
// registered with the daemon.
//
// internal/sandbox.DeletionReconciler matches it with errors.Is to log and
// skip that one segment rather than fail the whole sandbox cleanup: an agent
// that has been permanently decommissioned would otherwise block its
// sandboxes from ever being deleted and, because Reconcile returns on the
// first cleanup error, stall every later sandbox in the same tick behind it.
// Reclaiming the host-side object left behind on a since-returned agent is
// the deferred segment orphan sweep's job, not sandbox deletion's. A
// DestroySegment failure for any other reason stays a hard error.
//
// It lives here for the same import-direction reason as
// ErrNetworkIsolationUnsupported above.
var ErrSegmentAgentUnavailable = errors.New("segment's agent is not registered")

// advertisesNetworkIsolation reports whether agent claims real
// providersdk.NetworkIsolator support for provider.
//
// This is deliberately a plain helper over the agent's own AgentInfo rather
// than a new AgentRegistry method: the registry already hands callers the
// resolved agentsdk.Agent (via Get, which is what every existing-resource
// path must use), so a registry-level lookup would add a second, separately
// resolvable source of truth for no benefit. The check reads the same
// AgentInfo the caller already holds.
func advertisesNetworkIsolation(agent agentsdk.Agent, provider providersdk.Type) bool {
	if agent == nil {
		return false
	}
	return slices.Contains(agent.Info().NetworkIsolatingProviders, provider)
}

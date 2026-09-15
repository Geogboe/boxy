package pool

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// SegmentDestroyingProvisioner is an optional Provisioner capability for
// tearing down a network segment by agent ID directly -- unlike
// sandbox.NetworkIsolatingAllocator's CreateSegment/AttachToSegment (which
// need a model.Pool/model.Resource to resolve the owning agent), sandbox
// deletion only has model.Sandbox.NetworkSegments (agent ID + provider type
// + ref) to work from, with no pool/resource in scope any more.
type SegmentDestroyingProvisioner interface {
	DestroySegment(ctx context.Context, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error
}

// DestroySegment satisfies SegmentDestroyingProvisioner.
//
// Two conditions are reported as skip sentinels rather than failures, because
// neither should hold up a sandbox's deletion -- and because
// internal/sandbox.DeletionReconciler.Reconcile returns on the first cleanup
// error, so a permanently-failing segment stalls every later sandbox in the
// same tick, not just its own:
//
//   - The agent isn't registered at all: ErrSegmentAgentUnavailable, "this
//     segment's host is gone for good".
//   - The agent is registered but no longer advertises isolation for this
//     segment's provider type (reconfigured or downgraded between allocation
//     and deletion): ErrNetworkIsolationUnsupported, the same sentinel
//     CreateSegment returns for the same condition. Checked BEFORE the
//     agentsdk.NetworkIsolatingAgent type assertion for the same reason
//     CreateSegment checks it there -- both EmbeddedAgent and RemoteAgent
//     satisfy that interface unconditionally, so the assertion always
//     succeeds and the real capability check happens too far down, as a hard
//     error.
//
// Any other DestroySegment failure stays a hard error. See both sentinels'
// doc comments.
func (ap *AgentProvisioner) DestroySegment(ctx context.Context, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error {
	agent, ok := ap.Registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q unavailable to destroy segment %q: %w", agentID, ref, ErrSegmentAgentUnavailable)
	}
	if !advertisesNetworkIsolation(agent, providerType) {
		return fmt.Errorf("agent %q, provider %q, segment %q: %w", agentID, providerType, ref, ErrNetworkIsolationUnsupported)
	}
	isolator, ok := agent.(agentsdk.NetworkIsolatingAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support network isolation", agentID)
	}
	return isolator.DestroySegment(ctx, providerType, ref)
}

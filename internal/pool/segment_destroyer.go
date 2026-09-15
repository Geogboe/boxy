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
// An unregistered agent is reported as ErrSegmentAgentUnavailable so
// internal/sandbox.DeletionReconciler can tell "this segment's host is gone
// for good" apart from "tearing this segment down failed" -- only the latter
// should hold up a sandbox's deletion. See that sentinel's doc comment.
func (ap *AgentProvisioner) DestroySegment(ctx context.Context, agentID string, providerType providersdk.Type, ref providersdk.SegmentRef) error {
	agent, ok := ap.Registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q unavailable to destroy segment %q: %w", agentID, ref, ErrSegmentAgentUnavailable)
	}
	isolator, ok := agent.(agentsdk.NetworkIsolatingAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support network isolation", agentID)
	}
	return isolator.DestroySegment(ctx, providerType, ref)
}

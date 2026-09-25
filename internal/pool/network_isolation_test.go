package pool

import (
	"context"
	"errors"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// isolationTestProvisioner wires ap against one agent owning one resource in
// pool-a, the shared setup for every case below.
func isolationTestProvisioner(t *testing.T, agent *mockAgent) (*AgentProvisioner, model.Resource) {
	t.Helper()
	return &AgentProvisioner{
			Registry: registryWith(t, agent),
			Specs:    map[model.PoolName]boxyconfig.PoolSpec{"pool-a": {Type: "hyperv"}},
		},
		model.Resource{ID: "res-1", Provider: model.ProviderRef{AgentID: agent.Info().ID}}
}

// TestAgentProvisioner_CreateSegmentUnadvertisedReturnsSentinel is C1's core
// unit: the agent DOES implement agentsdk.NetworkIsolatingAgent (mockAgent
// always does, exactly like the real EmbeddedAgent and RemoteAgent), but
// advertises no isolation for the resolved provider type. That combination
// must produce the skip sentinel, and must never reach the agent at all.
func TestAgentProvisioner_CreateSegmentUnadvertisedReturnsSentinel(t *testing.T) {
	agent := newMockAgent("hyperv")
	agent.info.NetworkIsolatingProviders = nil
	agent.createSegmentRef = "boxy-sb-sb-1"
	ap, res := isolationTestProvisioner(t, agent)

	if _, ok := agentsdk.Agent(agent).(agentsdk.NetworkIsolatingAgent); !ok {
		t.Fatal("test precondition: the fake must implement NetworkIsolatingAgent, or this proves nothing")
	}

	_, _, _, err := ap.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, res, "sb-1", "10.250.0.0/29")
	if !errors.Is(err, ErrNetworkIsolationUnsupported) {
		t.Fatalf("err = %v, want ErrNetworkIsolationUnsupported", err)
	}
	if agent.gotCreateSegmentSandboxID != "" {
		t.Fatalf("agent was asked to create a segment (%q) despite advertising no isolation support",
			agent.gotCreateSegmentSandboxID)
	}
}

// An agent advertising isolation for a DIFFERENT provider than the pool
// resolves to must be treated the same as advertising none.
func TestAgentProvisioner_CreateSegmentAdvertisedForOtherProviderReturnsSentinel(t *testing.T) {
	agent := newMockAgent("hyperv", "devfactory")
	agent.info.NetworkIsolatingProviders = []providersdk.Type{"devfactory"}
	ap, res := isolationTestProvisioner(t, agent)

	_, _, _, err := ap.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, res, "sb-1", "10.250.0.0/29")
	if !errors.Is(err, ErrNetworkIsolationUnsupported) {
		t.Fatalf("err = %v, want ErrNetworkIsolationUnsupported for a pool resolving to hyperv", err)
	}
}

// I3: an empty SegmentRef returned without an error is this layer's job to
// reject (see agentsdk.NetworkIsolatingAgent's doc comment). It must be a
// plain error, NOT the skip sentinel -- a driver that advertised the
// capability and then answered with nothing is misbehaving and must not be
// silently skipped the way an honest non-isolating provider is.
func TestAgentProvisioner_CreateSegmentRejectsEmptyRef(t *testing.T) {
	for name, ref := range map[string]string{"empty": "", "whitespace": "   "} {
		t.Run(name, func(t *testing.T) {
			agent := newMockAgent("hyperv")
			agent.createSegmentRef = providersdk.SegmentRef(ref)
			ap, res := isolationTestProvisioner(t, agent)

			_, _, _, err := ap.CreateSegment(context.Background(), model.Pool{Name: "pool-a"}, res, "sb-1", "10.250.0.0/29")
			if err == nil {
				t.Fatal("expected an error for an empty segment ref returned without one")
			}
			if errors.Is(err, ErrNetworkIsolationUnsupported) {
				t.Fatalf("err = %v, must not be the skip sentinel: a blank ref is driver misbehavior, not an honest opt-out", err)
			}
		})
	}
}

// I5's prerequisite: the "agent is gone" case must be distinguishable with
// errors.Is, or internal/sandbox's deleter cannot tell it apart from a real
// teardown failure.
func TestAgentProvisioner_DestroySegmentUnregisteredAgentReturnsSentinel(t *testing.T) {
	ap := &AgentProvisioner{Registry: NewAgentRegistry()}
	err := ap.DestroySegment(context.Background(), "long-gone-agent", "hyperv", "boxy-sb-sb-1")
	if !errors.Is(err, ErrSegmentAgentUnavailable) {
		t.Fatalf("err = %v, want ErrSegmentAgentUnavailable", err)
	}
}

// The teardown counterpart to CreateSegment's advertisement check: an agent
// that is registered but no longer advertises isolation for this segment's
// provider type (reconfigured or downgraded between allocation and deletion)
// must be reported as ErrNetworkIsolationUnsupported, not as a hard error.
// Without this, sandbox deletion fails permanently on that segment and --
// because DeletionReconciler.Reconcile returns on the first cleanup error --
// stalls every later sandbox in the same tick.
func TestAgentProvisioner_DestroySegmentUnadvertisedAgentReturnsSentinel(t *testing.T) {
	agent := newMockAgent("hyperv")
	agent.info.NetworkIsolatingProviders = nil
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	err := ap.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1")
	if !errors.Is(err, ErrNetworkIsolationUnsupported) {
		t.Fatalf("err = %v, want ErrNetworkIsolationUnsupported", err)
	}
	if errors.Is(err, ErrSegmentAgentUnavailable) {
		t.Fatalf("err = %v must not claim the agent is unregistered; it is registered, just incapable", err)
	}
	if agent.gotDestroySegmentRef != "" {
		t.Fatalf("agent was asked to destroy segment %q despite advertising no isolation support", agent.gotDestroySegmentRef)
	}
}

// An agent advertising isolation for a DIFFERENT provider than the segment
// records must be treated the same as advertising none -- the mirror of
// CreateSegment's equivalent case.
func TestAgentProvisioner_DestroySegmentAdvertisedForOtherProviderReturnsSentinel(t *testing.T) {
	agent := newMockAgent("hyperv", "devfactory")
	agent.info.NetworkIsolatingProviders = []providersdk.Type{"devfactory"}
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	err := ap.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1")
	if !errors.Is(err, ErrNetworkIsolationUnsupported) {
		t.Fatalf("err = %v, want ErrNetworkIsolationUnsupported for a hyperv segment", err)
	}
}

// A registered agent that really does fail teardown must NOT look like the
// agent-gone case, or the deleter would swallow a genuine failure.
func TestAgentProvisioner_DestroySegmentRealFailureIsNotTheSentinel(t *testing.T) {
	agent := newMockAgent("hyperv")
	agent.destroySegmentErr = errors.New("vswitch still has connected adapters")
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	err := ap.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1")
	if err == nil {
		t.Fatal("expected the driver's teardown failure to surface")
	}
	if errors.Is(err, ErrSegmentAgentUnavailable) {
		t.Fatalf("err = %v must not match ErrSegmentAgentUnavailable", err)
	}
}

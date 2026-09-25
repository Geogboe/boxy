package pool

import (
	"context"
	"strings"
	"testing"
)

// TestAgentProvisioner_MeshIdentity_RejectsNonMeshCapableAgent and its
// AddMeshPeer counterpart below cover #379 blocker 6: mockAgent implements
// agentsdk.MeshPeeringAgent unconditionally (see provisioner_agent_test.go),
// the same way both EmbeddedAgent and RemoteAgent do in production -- so
// without AgentProvisioner consulting the agent's own probed
// AgentInfo.MeshCapable first, these calls would reach the driver and only
// fail deep inside real WireGuard device creation. They must instead fail
// fast, with a clear reason, and never touch the driver at all.
func TestAgentProvisioner_MeshIdentity_RejectsNonMeshCapableAgent(t *testing.T) {
	agent := newMockAgent("hyperv")
	// newMockAgent leaves MeshCapable at its zero value (false).
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	_, _, _, err := ap.MeshIdentity(context.Background(), "hyperv", agent.Info().ID, "seg-ref")
	if err == nil {
		t.Fatal("expected an error for a non-mesh-capable agent")
	}
	if !strings.Contains(err.Error(), "cannot create a WireGuard device") {
		t.Fatalf("error = %q, want it to explain the agent cannot create a WireGuard device", err.Error())
	}
	if agent.meshIdentityCalls != 0 {
		t.Fatalf("expected MeshIdentity to never reach the driver, got %d calls", agent.meshIdentityCalls)
	}
}

func TestAgentProvisioner_AddMeshPeer_RejectsNonMeshCapableAgent(t *testing.T) {
	agent := newMockAgent("hyperv")
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	err := ap.AddMeshPeer(context.Background(), "hyperv", agent.Info().ID, "seg-ref", "peer-pub", "peer-endpoint", "peer-cidr")
	if err == nil {
		t.Fatal("expected an error for a non-mesh-capable agent")
	}
	if !strings.Contains(err.Error(), "cannot create a WireGuard device") {
		t.Fatalf("error = %q, want it to explain the agent cannot create a WireGuard device", err.Error())
	}
	if agent.addMeshPeerCalls != 0 {
		t.Fatalf("expected AddMeshPeer to never reach the driver, got %d calls", agent.addMeshPeerCalls)
	}
}

// TestAgentProvisioner_MeshIdentity_DelegatesWhenMeshCapable and its
// AddMeshPeer counterpart prove the gate above is additive, not a
// regression: a mesh-capable agent's calls still reach the driver and
// return its result unchanged.
func TestAgentProvisioner_MeshIdentity_DelegatesWhenMeshCapable(t *testing.T) {
	agent := newMockAgent("hyperv")
	agent.info.MeshCapable = true
	agent.meshPublicKey, agent.meshEndpoint, agent.meshCIDR = "pub-key", "host:51820", "10.250.0.0/29"
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	pub, endpoint, cidr, err := ap.MeshIdentity(context.Background(), "hyperv", agent.Info().ID, "seg-ref")
	if err != nil {
		t.Fatalf("MeshIdentity: %v", err)
	}
	if pub != "pub-key" || endpoint != "host:51820" || cidr != "10.250.0.0/29" {
		t.Fatalf("got (%q, %q, %q), want (pub-key, host:51820, 10.250.0.0/29)", pub, endpoint, cidr)
	}
	if agent.meshIdentityCalls != 1 {
		t.Fatalf("expected 1 call to the driver, got %d", agent.meshIdentityCalls)
	}
}

func TestAgentProvisioner_AddMeshPeer_DelegatesWhenMeshCapable(t *testing.T) {
	agent := newMockAgent("hyperv")
	agent.info.MeshCapable = true
	ap := &AgentProvisioner{Registry: registryWith(t, agent)}

	if err := ap.AddMeshPeer(context.Background(), "hyperv", agent.Info().ID, "seg-ref", "peer-pub", "peer-endpoint", "peer-cidr"); err != nil {
		t.Fatalf("AddMeshPeer: %v", err)
	}
	if agent.addMeshPeerCalls != 1 {
		t.Fatalf("expected 1 call to the driver, got %d", agent.addMeshPeerCalls)
	}
}

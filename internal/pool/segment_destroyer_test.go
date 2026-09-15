package pool

import (
	"context"
	"testing"
)

func TestAgentProvisioner_DestroySegment(t *testing.T) {
	agent := newMockAgent("hyperv")
	registry := registryWith(t, agent)
	ap := &AgentProvisioner{Registry: registry}

	if err := ap.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if agent.gotDestroySegmentRef != "boxy-sb-sb-1" {
		t.Fatalf("agent got destroy ref = %q, want boxy-sb-sb-1", agent.gotDestroySegmentRef)
	}
}

func TestAgentProvisioner_DestroySegmentUnknownAgentErrors(t *testing.T) {
	ap := &AgentProvisioner{Registry: NewAgentRegistry()}
	if err := ap.DestroySegment(context.Background(), "missing-agent", "hyperv", "boxy-sb-sb-1"); err == nil {
		t.Fatal("expected an error for an unresolvable agent")
	}
}

func TestManager_DestroySegment_DelegatesToProvisionerCapability(t *testing.T) {
	agent := newMockAgent("hyperv")
	registry := registryWith(t, agent)
	ap := &AgentProvisioner{Registry: registry}
	m := New(nil, ap)

	if err := m.DestroySegment(context.Background(), agent.Info().ID, "hyperv", "boxy-sb-sb-1"); err != nil {
		t.Fatalf("DestroySegment: %v", err)
	}
	if agent.gotDestroySegmentRef != "boxy-sb-sb-1" {
		t.Fatalf("agent got destroy ref = %q, want boxy-sb-sb-1", agent.gotDestroySegmentRef)
	}
}

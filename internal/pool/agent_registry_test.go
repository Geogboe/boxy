package pool

import (
	"testing"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestAgentRegistry_ResolveByType(t *testing.T) {
	r := NewAgentRegistry()
	a := newMockAgent(providersdk.Type("docker"))
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Resolve("docker", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Info().ID != "mock-agent" {
		t.Fatalf("expected mock-agent, got %q", got.Info().ID)
	}
}

func TestAgentRegistry_ResolveNoAvailableAgent(t *testing.T) {
	r := NewAgentRegistry()
	if _, err := r.Resolve("docker", ""); err == nil {
		t.Fatal("expected an error when no agent supports the provider type")
	}
}

func TestAgentRegistry_ResolvePinnedSuccess(t *testing.T) {
	r := NewAgentRegistry()
	agentA := newMockAgent(providersdk.Type("hyperv"))
	agentA.info.ID = "agent-a"
	agentB := newMockAgent(providersdk.Type("hyperv"))
	agentB.info.ID = "agent-b"
	if err := r.Register(agentA); err != nil {
		t.Fatalf("Register agentA: %v", err)
	}
	if err := r.Register(agentB); err != nil {
		t.Fatalf("Register agentB: %v", err)
	}

	got, err := r.Resolve("hyperv", "agent-b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Info().ID != "agent-b" {
		t.Fatalf("expected the pinned agent-b, got %q", got.Info().ID)
	}
}

func TestAgentRegistry_ResolvePinnedNonexistentAgentFailsFast(t *testing.T) {
	r := NewAgentRegistry()
	if err := r.Register(newMockAgent(providersdk.Type("hyperv"))); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := r.Resolve("hyperv", "no-such-agent"); err == nil {
		t.Fatal("expected an error pinning to a nonexistent agent, not a silent fallback")
	}
}

func TestAgentRegistry_ResolvePinnedAgentLackingProviderFailsFast(t *testing.T) {
	r := NewAgentRegistry()
	a := newMockAgent(providersdk.Type("docker"))
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := r.Resolve("hyperv", "mock-agent"); err == nil {
		t.Fatal("expected an error pinning to an agent that doesn't support the requested provider type")
	}
}

func TestAgentRegistry_SetAvailableGatesResolve(t *testing.T) {
	r := NewAgentRegistry()
	a := newMockAgent(providersdk.Type("docker"))
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r.SetAvailable("mock-agent", false)
	if _, err := r.Resolve("docker", ""); err == nil {
		t.Fatal("expected Resolve to skip an agent marked unavailable")
	}

	// Get must still succeed for an unavailable-but-registered agent:
	// existing resources still need to route lifecycle calls to it.
	if _, ok := r.Get("mock-agent"); !ok {
		t.Fatal("expected Get to still find an unavailable-but-registered agent")
	}

	r.SetAvailable("mock-agent", true)
	if _, err := r.Resolve("docker", ""); err != nil {
		t.Fatalf("expected Resolve to succeed again once marked available: %v", err)
	}
}

func TestAgentRegistry_GetExactInstance(t *testing.T) {
	r := NewAgentRegistry()
	agentA := newMockAgent(providersdk.Type("hyperv"))
	agentA.info.ID = "agent-a"
	agentB := newMockAgent(providersdk.Type("hyperv"))
	agentB.info.ID = "agent-b"
	if err := r.Register(agentA); err != nil {
		t.Fatalf("Register agentA: %v", err)
	}
	if err := r.Register(agentB); err != nil {
		t.Fatalf("Register agentB: %v", err)
	}

	got, ok := r.Get("agent-a")
	if !ok {
		t.Fatal("expected agent-a to be found")
	}
	if got.Info().ID != "agent-a" {
		t.Fatalf("expected agent-a, got %q", got.Info().ID)
	}

	if _, ok := r.Get("no-such-agent"); ok {
		t.Fatal("expected Get to report not-found for an unregistered agent")
	}
}

func TestAgentRegistry_DeregisterRemovesEntirely(t *testing.T) {
	r := NewAgentRegistry()
	a := newMockAgent(providersdk.Type("docker"))
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r.Deregister("mock-agent")

	if _, ok := r.Get("mock-agent"); ok {
		t.Fatal("expected Get to fail after Deregister")
	}
	if _, err := r.Resolve("docker", ""); err == nil {
		t.Fatal("expected Resolve to fail after Deregister")
	}
}

func TestAgentRegistry_ReregisterReplacesEntry(t *testing.T) {
	r := NewAgentRegistry()
	first := newMockAgent(providersdk.Type("docker"))
	if err := r.Register(first); err != nil {
		t.Fatalf("Register first: %v", err)
	}

	second := newMockAgent(providersdk.Type("docker"))
	second.nextResourceID = "second-instance-resource"
	if err := r.Register(second); err != nil {
		t.Fatalf("Register second: %v", err)
	}

	got, ok := r.Get("mock-agent")
	if !ok {
		t.Fatal("expected mock-agent to still be found after reconnect")
	}
	if got != agentsdk.Agent(second) {
		t.Fatal("expected Get to resolve to the newest registration for a reconnecting agent ID")
	}

	// byType bookkeeping must not have accumulated a duplicate entry.
	resolved, err := r.Resolve("docker", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != agentsdk.Agent(second) {
		t.Fatal("expected Resolve to also return the newest registration")
	}
}

func TestAgentRegistry_List(t *testing.T) {
	r := NewAgentRegistry()
	a := newMockAgent(providersdk.Type("docker"), providersdk.Type("hyperv"))
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.SetAvailable("mock-agent", false)

	summaries := r.List()
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]
	if s.ID != "mock-agent" || s.Available {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if len(s.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %+v", s.Providers)
	}
}

func TestAgentRegistry_RegisterRejectsNilOrEmptyID(t *testing.T) {
	r := NewAgentRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected an error registering a nil agent")
	}

	empty := newMockAgent(providersdk.Type("docker"))
	empty.info.ID = ""
	if err := r.Register(empty); err == nil {
		t.Fatal("expected an error registering an agent with an empty ID")
	}
}

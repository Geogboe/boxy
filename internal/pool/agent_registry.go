package pool

import (
	"fmt"
	"slices"
	"sync"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// AgentRegistry tracks every agent currently available to the daemon — the
// embedded agent plus zero or more connected remote agents — and resolves
// which agent should serve a given provider type / pin.
//
// Resolve is for choosing an agent for NEW provisioning only. Existing
// resources must route back to the exact agent that created them via Get,
// not be re-resolved by type — see AgentProvisioner's Destroy/Allocate and
// docs/adr/0005-remote-agent-transport-and-registration.md.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]agentsdk.Agent
	byType map[providersdk.Type][]string // provider type -> agent IDs, insertion order
	avail  map[string]bool               // agent ID -> available for new provisioning
}

// NewAgentRegistry creates an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]agentsdk.Agent),
		byType: make(map[providersdk.Type][]string),
		avail:  make(map[string]bool),
	}
}

// Register adds or replaces an agent (keyed by its AgentInfo.ID) and marks
// it available. Replacing an existing ID (e.g. a remote agent reconnecting
// with a new stream) is intentional: it lets Get(agentID) transparently
// resolve to the new connection.
func (r *AgentRegistry) Register(a agentsdk.Agent) error {
	if a == nil {
		return fmt.Errorf("cannot register a nil agent")
	}
	info := a.Info()
	if info.ID == "" {
		return fmt.Errorf("cannot register an agent with an empty ID")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[info.ID]; exists {
		r.removeFromByTypeLocked(info.ID)
	}

	r.agents[info.ID] = a
	r.avail[info.ID] = true
	for _, t := range info.Providers {
		r.byType[t] = append(r.byType[t], info.ID)
	}
	return nil
}

// Deregister removes an agent entirely. Used when an operator explicitly
// revokes an agent's identity, not on a transient disconnect (see
// SetAvailable for that case).
func (r *AgentRegistry) Deregister(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeFromByTypeLocked(agentID)
	delete(r.agents, agentID)
	delete(r.avail, agentID)
}

func (r *AgentRegistry) removeFromByTypeLocked(agentID string) {
	old, ok := r.agents[agentID]
	if !ok {
		return
	}
	for _, t := range old.Info().Providers {
		ids := r.byType[t]
		for i, id := range ids {
			if id == agentID {
				r.byType[t] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
	}
}

// SetAvailable marks an agent as available or unavailable for NEW
// provisioning, without removing it from the registry. Used by the
// heartbeat-miss monitor: an unavailable agent's providers are skipped by
// Resolve, but resources already attributed to it (via Get) are untouched.
func (r *AgentRegistry) SetAvailable(agentID string, available bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[agentID]; ok {
		r.avail[agentID] = available
	}
}

// Get looks up an agent by its exact ID, regardless of availability. Used
// for lifecycle operations (Destroy, Allocate) on a resource that must go
// back to the specific agent that created it.
func (r *AgentRegistry) Get(agentID string) (agentsdk.Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[agentID]
	return a, ok
}

// Resolve picks an agent to serve a NEW provisioning request for the given
// provider type. If pinnedAgentID is non-empty, it must name a registered,
// currently-available agent that supports the provider type — pinning to
// the wrong, nonexistent, or unavailable agent is a fail-fast config/state
// error, never a silent fallback to a different agent. Otherwise, the
// first available agent (by registration order) offering the provider
// type is returned. Load-balancing across multiple available agents
// offering the same type is out of scope for now.
func (r *AgentRegistry) Resolve(provider providersdk.Type, pinnedAgentID string) (agentsdk.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if pinnedAgentID != "" {
		a, ok := r.agents[pinnedAgentID]
		if !ok {
			return nil, fmt.Errorf("pinned agent %q is not registered", pinnedAgentID)
		}
		if !supportsProvider(a, provider) {
			return nil, fmt.Errorf("pinned agent %q does not support provider %q", pinnedAgentID, provider)
		}
		// A pin never bypasses the heartbeat-miss availability gate —
		// otherwise it would silently defeat the whole mechanism for any
		// pinned pool.
		if !r.avail[pinnedAgentID] {
			return nil, fmt.Errorf("pinned agent %q is currently unavailable", pinnedAgentID)
		}
		return a, nil
	}

	for _, id := range r.byType[provider] {
		if r.avail[id] {
			return r.agents[id], nil
		}
	}
	return nil, fmt.Errorf("no available agent for provider %q", provider)
}

func supportsProvider(a agentsdk.Agent, provider providersdk.Type) bool {
	return slices.Contains(a.Info().Providers, provider)
}

// AgentSummary is a read-only snapshot of a registered agent, for
// `GET /api/v1/agents` and `boxy agent list`.
type AgentSummary struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Providers []providersdk.Type `json:"providers"`
	Available bool               `json:"available"`
}

// List returns a snapshot of every registered agent.
func (r *AgentRegistry) List() []AgentSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AgentSummary, 0, len(r.agents))
	for id, a := range r.agents {
		info := a.Info()
		out = append(out, AgentSummary{
			ID:        id,
			Name:      info.Name,
			Providers: info.Providers,
			Available: r.avail[id],
		})
	}
	return out
}

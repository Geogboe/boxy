package pool

import (
	"context"
	"fmt"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// AgentProvisioner adapts agentsdk.Agent instances into the pool.Provisioner
// interface. It routes CRUD operations through an agent, which transparently
// dispatches to the appropriate driver (whether local or remote).
//
// Provision resolves an agent by provider type (optionally pinned via
// spec.Agent) since it's creating a brand new resource. Destroy and
// Allocate operate on an *existing* resource and must instead route back to
// res.Provider.AgentID — the exact agent instance that created it — via
// Registry.Get. Once more than one agent can advertise the same provider
// type, re-resolving by type at Destroy/Allocate time could silently route
// to a different agent than the one that owns the resource; because
// providersdk.Driver.Delete is contractually idempotent for an
// already-missing resource, a misrouted Destroy would report success while
// the resource keeps running, unmanaged, on its real host. See
// docs/adr/0005-remote-agent-transport-and-registration.md.
type AgentProvisioner struct {
	Registry  *AgentRegistry
	Specs     map[model.PoolName]boxyconfig.PoolSpec
	Providers map[string]providersdk.Instance
	Now       func() time.Time
}

func (ap *AgentProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return model.Resource{}, fmt.Errorf("unknown pool %q", pool.Name)
	}

	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.Registry.Resolve(driverType, spec.Agent)
	if err != nil {
		return model.Resource{}, fmt.Errorf("resolve agent for pool %q: %w", pool.Name, err)
	}

	res, err := agent.Create(ctx, driverType, spec.Config)
	if err != nil {
		return model.Resource{}, fmt.Errorf("agent create for pool %q: %w", pool.Name, err)
	}

	now := time.Now().UTC()
	if ap.Now != nil {
		now = ap.Now().UTC()
	}

	// Merge connection info and metadata into properties.
	props := make(map[string]any, len(res.ConnectionInfo)+len(res.Metadata))
	for k, v := range res.ConnectionInfo {
		props[k] = v
	}
	for k, v := range res.Metadata {
		props[k] = v
	}

	return model.Resource{
		ID:         model.ResourceID(res.ID),
		Type:       pool.Inventory.ExpectedType,
		Profile:    pool.Inventory.ExpectedProfile,
		OriginPool: pool.Name,
		Provider:   model.ProviderRef{Name: string(driverType), AgentID: agent.Info().ID},
		State:      model.ResourceStateReady,
		Properties: props,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (ap *AgentProvisioner) Allocate(ctx context.Context, pool model.Pool, res model.Resource) (map[string]any, error) {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return nil, fmt.Errorf("unknown pool %q", pool.Name)
	}
	driverType := ap.driverTypeForPool(spec)
	agent, err := ap.agentForResource(res)
	if err != nil {
		return nil, err
	}
	if gp, ok := agent.(agentsdk.GuestPersonalizingAgent); ok {
		result, err := gp.PersonalizeGuest(ctx, driverType, string(res.ID))
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result.AccessDetails.ToProperties(), nil
		}
	}
	return agent.Allocate(ctx, driverType, string(res.ID))
}

func (ap *AgentProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	spec, ok := ap.Specs[pool.Name]
	if !ok {
		return fmt.Errorf("unknown pool %q", pool.Name)
	}

	driverType := ap.driverTypeForPool(spec)
	id := strings.TrimSpace(string(res.ID))
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	agent, err := ap.agentForResource(res)
	if err != nil {
		return err
	}

	if err := agent.Delete(ctx, driverType, id); err != nil {
		return fmt.Errorf("agent delete for pool %q: %w", pool.Name, err)
	}
	return nil
}

// agentForResource resolves the exact agent instance that created res, via
// its recorded AgentID — never by re-resolving the provider type, which
// could pick a different agent than the one that actually owns the
// resource. If that specific agent isn't currently registered/connected,
// the caller's existing retry/backoff path handles retrying later; this
// never silently substitutes a different agent.
func (ap *AgentProvisioner) agentForResource(res model.Resource) (agentsdk.Agent, error) {
	agent, ok := ap.Registry.Get(res.Provider.AgentID)
	if !ok {
		return nil, fmt.Errorf("agent %q unavailable for resource %q", res.Provider.AgentID, res.ID)
	}
	return agent, nil
}

// driverTypeForPool resolves the provider type for a pool spec.
// Priority:
// 1. If spec.Provider is set, resolve via Providers map or use as direct type
// 2. If spec.Type is docker/container, use "docker" driver type
// 3. Otherwise, use spec.Type as the driver type
func (ap *AgentProvisioner) driverTypeForPool(spec boxyconfig.PoolSpec) providersdk.Type {
	if strings.TrimSpace(spec.Provider) != "" {
		// Try to resolve as a named provider instance first
		if inst, ok := ap.Providers[spec.Provider]; ok {
			return inst.Type
		}
		// Otherwise treat provider name as a direct driver type
		return providersdk.Type(spec.Provider)
	}

	// Map pool type to driver type
	switch strings.TrimSpace(spec.Type) {
	case "docker", "container", "":
		return "docker"
	default:
		return providersdk.Type(spec.Type)
	}
}

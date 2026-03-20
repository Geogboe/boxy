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
type AgentProvisioner struct {
	Agent     agentsdk.Agent
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
	res, err := ap.Agent.Create(ctx, driverType, spec.Config)
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
		Provider:   model.ProviderRef{Name: string(driverType)},
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
	return ap.Agent.Allocate(ctx, driverType, string(res.ID))
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

	if err := ap.Agent.Delete(ctx, driverType, id); err != nil {
		return fmt.Errorf("agent delete for pool %q: %w", pool.Name, err)
	}
	return nil
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

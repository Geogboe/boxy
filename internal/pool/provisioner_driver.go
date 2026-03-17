package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// DriverProvisioner adapts providersdk.Driver instances into the pool.Provisioner
// interface. It dispatches to the correct driver based on each pool's provider
// configuration.
type DriverProvisioner struct {
	Registry  *providersdk.Registry
	Specs     map[model.PoolName]boxyconfig.PoolSpec
	Providers map[string]providersdk.Instance
	Now       func() time.Time
}

func (dp *DriverProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	driver, providerName, err := dp.driverForPool(pool.Name)
	if err != nil {
		return model.Resource{}, fmt.Errorf("provision pool %q: %w", pool.Name, err)
	}

	spec := dp.Specs[pool.Name]
	res, err := driver.Create(ctx, spec.Config)
	if err != nil {
		return model.Resource{}, fmt.Errorf("driver create for pool %q: %w", pool.Name, err)
	}

	now := time.Now().UTC()
	if dp.Now != nil {
		now = dp.Now().UTC()
	}

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
		Provider:   model.ProviderRef{Name: providerName},
		State:      model.ResourceStateReady,
		Properties: props,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (dp *DriverProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	driver, _, err := dp.driverForPool(pool.Name)
	if err != nil {
		return fmt.Errorf("destroy pool %q: %w", pool.Name, err)
	}

	id := strings.TrimSpace(string(res.ID))
	if id == "" {
		return fmt.Errorf("resource id is required")
	}

	if err := driver.Delete(ctx, id); err != nil {
		return fmt.Errorf("driver delete %q: %w", id, err)
	}
	return nil
}

func (dp *DriverProvisioner) driverForPool(name model.PoolName) (providersdk.Driver, string, error) {
	spec, ok := dp.Specs[name]
	if !ok {
		return nil, "", fmt.Errorf("unknown pool %q", name)
	}

	providerName := effectiveProviderName(spec)
	inst, ok := dp.Providers[providerName]
	if !ok {
		return nil, "", fmt.Errorf("pool %q references unknown provider %q", name, providerName)
	}

	reg, ok := dp.Registry.Get(inst.Type)
	if !ok {
		return nil, "", fmt.Errorf("no registered driver for provider type %q", inst.Type)
	}

	cfg := reg.ConfigProto()
	if err := decodeConfig(inst.Config, cfg); err != nil {
		return nil, "", fmt.Errorf("decode config for provider %q: %w", providerName, err)
	}

	driver, err := reg.NewDriver(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("create driver for provider %q: %w", providerName, err)
	}

	return driver, providerName, nil
}

func effectiveProviderName(spec boxyconfig.PoolSpec) string {
	if strings.TrimSpace(spec.Provider) != "" {
		return spec.Provider
	}
	switch strings.TrimSpace(spec.Type) {
	case "docker", "container", "":
		return "docker-local"
	default:
		return spec.Provider
	}
}

// decodeConfig does a basic map[string]any → struct decode using JSON round-trip.
func decodeConfig(raw map[string]any, target any) error {
	if len(raw) == 0 {
		return nil
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	return nil
}

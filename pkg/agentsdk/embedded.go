package agentsdk

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

// EmbeddedAgent is an in-process agent that dispatches directly to
// drivers. No network involved — the server calls driver methods in the
// same process. Used when boxy serve hosts providers locally.
type EmbeddedAgent struct {
	info    AgentInfo
	drivers map[providersdk.Type]providersdk.Driver
}

// NewEmbeddedAgent creates an agent backed by the given drivers.
// Each driver must have a unique Type — one driver per provider type per agent.
func NewEmbeddedAgent(id, name string, drivers ...providersdk.Driver) (*EmbeddedAgent, error) {
	dm := make(map[providersdk.Type]providersdk.Driver, len(drivers))
	providers := make([]providersdk.Type, 0, len(drivers))

	for _, d := range drivers {
		if _, exists := dm[d.Type()]; exists {
			return nil, fmt.Errorf("agent %q: duplicate provider type %q", id, d.Type())
		}
		dm[d.Type()] = d
		providers = append(providers, d.Type())
	}

	return &EmbeddedAgent{
		info: AgentInfo{
			ID:        id,
			Name:      name,
			Providers: providers,
		},
		drivers: dm,
	}, nil
}

func (a *EmbeddedAgent) Info() AgentInfo {
	return a.info
}

func (a *EmbeddedAgent) Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error) {
	d, err := a.driver(provider)
	if err != nil {
		return nil, err
	}
	return d.Create(ctx, cfg)
}

func (a *EmbeddedAgent) Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error) {
	d, err := a.driver(provider)
	if err != nil {
		return nil, err
	}
	return d.Read(ctx, id)
}

func (a *EmbeddedAgent) Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error) {
	d, err := a.driver(provider)
	if err != nil {
		return nil, err
	}
	return d.Update(ctx, id, op)
}

func (a *EmbeddedAgent) Delete(ctx context.Context, provider providersdk.Type, id string) error {
	d, err := a.driver(provider)
	if err != nil {
		return err
	}
	return d.Delete(ctx, id)
}

func (a *EmbeddedAgent) Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error) {
	d, err := a.driver(provider)
	if err != nil {
		return nil, err
	}
	return d.Allocate(ctx, id)
}

func (a *EmbeddedAgent) driver(provider providersdk.Type) (providersdk.Driver, error) {
	d, ok := a.drivers[provider]
	if !ok {
		return nil, fmt.Errorf("agent %q: provider %q not available", a.info.ID, provider)
	}
	return d, nil
}

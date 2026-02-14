package providers

import (
	"context"
	"fmt"
	"sort"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// Registry maps ProviderType -> Driver.
type Registry struct {
	drivers map[model.ProviderType]Driver
}

func NewRegistry() *Registry {
	return &Registry{drivers: make(map[model.ProviderType]Driver)}
}

func (r *Registry) Register(d Driver) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if d == nil {
		return fmt.Errorf("driver is nil")
	}
	t := d.Type()
	if t == "" {
		return fmt.Errorf("driver type is empty")
	}
	if _, exists := r.drivers[t]; exists {
		return fmt.Errorf("driver already registered for type %q", t)
	}
	r.drivers[t] = d
	return nil
}

func (r *Registry) Has(t model.ProviderType) bool {
	if r == nil {
		return false
	}
	_, ok := r.drivers[t]
	return ok
}

func (r *Registry) Get(t model.ProviderType) (Driver, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.drivers[t]
	return d, ok
}

func (r *Registry) Types() []model.ProviderType {
	if r == nil {
		return nil
	}
	types := make([]model.ProviderType, 0, len(r.drivers))
	for k := range r.drivers {
		types = append(types, k)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

// ValidateProviders enforces that every Provider.Type is supported and that each
// provider's config validates against its Driver.
func (r *Registry) ValidateProviders(ctx context.Context, providers []model.Provider) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	for i := range providers {
		p := providers[i]
		d, ok := r.Get(p.Type)
		if !ok {
			return fmt.Errorf("provider[%d] has unsupported type %q", i, p.Type)
		}
		if err := d.ValidateConfig(ctx, p); err != nil {
			return fmt.Errorf("provider[%d] config invalid: %w", i, err)
		}
	}
	return nil
}

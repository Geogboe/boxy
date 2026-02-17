package providersdk

import (
	"context"
	"fmt"
	"sort"
)

// Registry maps provider Type -> Driver.
type Registry struct {
	drivers map[Type]Driver
}

func NewRegistry() *Registry {
	return &Registry{drivers: make(map[Type]Driver)}
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

func (r *Registry) Get(t Type) (Driver, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.drivers[t]
	return d, ok
}

func (r *Registry) Types() []Type {
	if r == nil {
		return nil
	}
	types := make([]Type, 0, len(r.drivers))
	for k := range r.drivers {
		types = append(types, k)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

// ValidateInstances enforces that every Instance.Type is supported and that each
// instance's config validates against its Driver.
func (r *Registry) ValidateInstances(ctx context.Context, instances []Instance) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	for i := range instances {
		inst := instances[i]
		d, ok := r.Get(inst.Type)
		if !ok {
			return fmt.Errorf("provider[%d] has unsupported type %q", i, inst.Type)
		}
		if err := d.ValidateConfig(ctx, inst); err != nil {
			return fmt.Errorf("provider[%d] config invalid: %w", i, err)
		}
	}
	return nil
}

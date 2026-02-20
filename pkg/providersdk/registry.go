package providersdk

import (
	"context"
	"fmt"
	"sort"
)

// Registration bundles everything a provider type contributes to the system:
// a config prototype for unmarshaling pool config blocks, and a factory
// that produces a Driver from parsed config.
type Registration struct {
	// Type is the provider type identifier (e.g. "docker", "hyperv").
	Type Type

	// ConfigProto returns a zero-value config struct for this driver type.
	// The system unmarshals the pool's config: YAML block into this struct.
	ConfigProto func() any

	// NewDriver creates a Driver instance from a parsed config struct.
	// The cfg argument is the same type returned by ConfigProto, populated
	// by the YAML unmarshaler.
	NewDriver func(cfg any) (Driver, error)
}

// Instance is a configured provider — a named, typed instance with its raw config.
// These are declared in the boxy.yaml providers: list and passed to ValidateInstances.
type Instance struct {
	Name   string         `json:"name" yaml:"name"`
	Type   Type           `json:"type" yaml:"type"`
	Config map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
}

// Registry maps provider Type -> Registration.
type Registry struct {
	registrations map[Type]Registration
}

func NewRegistry() *Registry {
	return &Registry{registrations: make(map[Type]Registration)}
}

// Register adds a provider registration. Returns an error if the type is
// already registered or the registration is invalid.
func (r *Registry) Register(reg Registration) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if reg.Type == "" {
		return fmt.Errorf("registration type is empty")
	}
	if reg.ConfigProto == nil {
		return fmt.Errorf("registration %q: ConfigProto is nil", reg.Type)
	}
	if reg.NewDriver == nil {
		return fmt.Errorf("registration %q: NewDriver is nil", reg.Type)
	}
	if _, exists := r.registrations[reg.Type]; exists {
		return fmt.Errorf("driver already registered for type %q", reg.Type)
	}
	r.registrations[reg.Type] = reg
	return nil
}

// Get returns the registration for a provider type.
func (r *Registry) Get(t Type) (Registration, bool) {
	if r == nil {
		return Registration{}, false
	}
	reg, ok := r.registrations[t]
	return reg, ok
}

// Types returns all registered provider types in sorted order.
func (r *Registry) Types() []Type {
	if r == nil {
		return nil
	}
	types := make([]Type, 0, len(r.registrations))
	for k := range r.registrations {
		types = append(types, k)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

// ValidateInstances checks that every instance references a registered provider type.
func (r *Registry) ValidateInstances(_ context.Context, instances []Instance) error {
	for _, inst := range instances {
		if _, ok := r.Get(inst.Type); !ok {
			return fmt.Errorf("provider %q: unknown type %q", inst.Name, inst.Type)
		}
	}
	return nil
}

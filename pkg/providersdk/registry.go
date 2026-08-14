package providersdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Registration bundles everything a provider type contributes to the system:
// a config prototype for unmarshaling provider-instance config, and a factory
// that produces a Driver from parsed config. Pool-level create settings are
// decoded separately by each driver's Create method.
type Registration struct {
	// Type is the provider type identifier (e.g. "docker", "hyperv").
	Type Type

	// ConfigProto returns a zero-value config struct for this driver type.
	// The system unmarshals a provider instance's config YAML block into this
	// struct before calling NewDriver.
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

// NewDriverFromInstance decodes a configured provider instance and creates its
// driver. An instance with an empty config receives the registration's
// zero-value configuration. This keeps provider configuration plumbing in the
// provider-agnostic registry rather than duplicating it in each application
// entrypoint.
func (r *Registry) NewDriverFromInstance(instance Instance) (Driver, error) {
	registration, ok := r.Get(instance.Type)
	if !ok {
		return nil, fmt.Errorf("provider type %q not found in registry", instance.Type)
	}
	cfg := registration.ConfigProto()
	if len(instance.Config) != 0 {
		b, err := json.Marshal(instance.Config)
		if err != nil {
			return nil, fmt.Errorf("marshal config for provider type %q: %w", instance.Type, err)
		}
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config for provider type %q: %w", instance.Type, err)
		}
	}
	driver, err := registration.NewDriver(cfg)
	if err != nil {
		return nil, fmt.Errorf("create driver for provider type %q: %w", instance.Type, err)
	}
	return driver, nil
}

// ValidateInstances checks that every instance references a registered provider type
// and that each provider type is configured at most once. Driver lookup is keyed
// by provider type, so duplicate instances would otherwise be ambiguous.
func (r *Registry) ValidateInstances(_ context.Context, instances []Instance) error {
	seen := make(map[Type]string, len(instances))
	for _, inst := range instances {
		if _, ok := r.Get(inst.Type); !ok {
			return fmt.Errorf("provider %q: unknown type %q", inst.Name, inst.Type)
		}
		if previous, ok := seen[inst.Type]; ok {
			return fmt.Errorf("provider type %q has multiple configured instances %q and %q; drivers are keyed by provider type", inst.Type, previous, inst.Name)
		}
		seen[inst.Type] = inst.Name
	}
	return nil
}

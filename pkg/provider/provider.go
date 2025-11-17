package provider

import (
	"context"

	"github.com/Geogboe/boxy/internal/core/resource"
)

// Provider is the interface that all backend providers must implement
type Provider interface {
	// Provision creates a new resource based on the specification
	// Returns the created resource with provider-specific metadata populated
	Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error)

	// Destroy removes a resource and cleans up all associated data
	Destroy(ctx context.Context, res *resource.Resource) error

	// GetStatus returns the current status of a resource
	GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error)

	// GetConnectionInfo returns connection details for a resource
	GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error)

	// HealthCheck verifies the provider is operational
	HealthCheck(ctx context.Context) error

	// Name returns the provider name (docker, hyperv, kvm, etc.)
	Name() string

	// Type returns the resource type this provider handles
	Type() resource.ResourceType
}

// Registry manages available providers
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry
func (r *Registry) Register(name string, provider Provider) {
	r.providers[name] = provider
}

// Get retrieves a provider by name
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// List returns all registered provider names
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

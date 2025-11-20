package provider

import (
	"context"
	"sync"

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

	// Execute runs a command inside the resource and returns the output
	// For Docker: uses 'docker exec'
	// For Hyper-V: uses PowerShell Direct (Invoke-Command -VMName)
	// For SSH-based: uses SSH connection
	Execute(ctx context.Context, res *resource.Resource, cmd []string) (*ExecuteResult, error)

	// HealthCheck verifies the provider is operational
	HealthCheck(ctx context.Context) error

	// Name returns the provider name (docker, hyperv, etc.)
	Name() string

	// Type returns the resource type this provider handles
	Type() resource.ResourceType
}

// ExecuteResult contains the result of executing a command in a resource
type ExecuteResult struct {
	ExitCode int    // Command exit code
	Stdout   string // Standard output
	Stderr   string // Standard error
	Error    error  // Execution error (connection failed, etc.)
}

// Registry manages available providers with thread-safe access
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry (thread-safe)
func (r *Registry) Register(name string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = provider
}

// Get retrieves a provider by name (thread-safe)
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// List returns all registered provider names (thread-safe)
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

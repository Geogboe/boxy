package provider

import (
	"context"
	"sync"

	"github.com/Geogboe/boxy/internal/core/resource"
)

// Provider is the interface that all backend providers must implement.
// Providers are stateless and dumb - they just translate Boxy commands to backend APIs.
type Provider interface {
	// Lifecycle operations
	Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error)
	Destroy(ctx context.Context, res *resource.Resource) error

	// Status and information
	GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error)
	GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error)

	// Resource management
	Update(ctx context.Context, res *resource.Resource, updates ResourceUpdate) error
	// Exec runs a command inside the resource (aligns with docker exec, kubectl exec)
	Exec(ctx context.Context, res *resource.Resource, cmd []string) (*ExecResult, error)

	// Provider health
	HealthCheck(ctx context.Context) error

	// Metadata
	Name() string
	Type() resource.ResourceType
}

// ResourceUpdate contains provider-specific update operations
type ResourceUpdate struct {
	// VM-specific operations
	PowerState *PowerState   // start, stop, pause, reset
	Snapshot   *SnapshotOp   // create, restore, delete snapshot

	// Container-specific operations
	Resources *ResourceLimits // CPU, memory adjustments

	// Providers implement what they support, return error for unsupported operations
}

// PowerState represents VM power states
type PowerState string

const (
	PowerStateRunning PowerState = "running"
	PowerStateStopped PowerState = "stopped"
	PowerStatePaused  PowerState = "paused"
	PowerStateReset   PowerState = "reset"
)

// SnapshotOp represents snapshot operations
type SnapshotOp struct {
	Operation string // "create", "restore", "delete"
	Name      string // Snapshot name
}

// ResourceLimits for resource updates
type ResourceLimits struct {
	CPUs     *int // CPU count
	MemoryMB *int // Memory in MB
}

// ExecResult contains the result of executing a command inside a resource.
// Used by hooks to run setup scripts inside resources.
type ExecResult struct {
	ExitCode int    // Command exit code
	Stdout   string // Standard output
	Stderr   string // Standard error
	Error    error  // Execution error (connection failed, timeout, etc.)
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

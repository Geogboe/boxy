package providersdk

import "context"

// Type identifies the provider kind (e.g. "docker", "hyperv").
type Type string

// Driver is the CRUD interface that every provider driver implements.
//
// A driver is a thin adapter between Boxy and the underlying infrastructure
// (Docker, Hyper-V, VMware, etc.). It uses its native mechanism for all
// operations — the same client that creates resources also reads, updates,
// and deletes them.
//
// Create and Delete manage resource lifecycle. Read checks current state.
// Update performs post-creation operations through the driver's native
// mechanism (docker exec, PowerShell Direct, VMware Tools, SSH, etc.).
type Driver interface {
	// Type returns the provider type identifier (e.g. "docker", "hyperv").
	Type() Type

	// Create provisions a new resource from the given configuration.
	// The config is the driver's own typed struct, already unmarshaled.
	Create(ctx context.Context, cfg any) (*Resource, error)

	// Read returns the current status of a resource.
	Read(ctx context.Context, id string) (*ResourceStatus, error)

	// Update performs an operation on an existing resource. What "update"
	// means depends on the resource type: exec a command in a container,
	// set permissions on a share, attach a node to a network, etc.
	Update(ctx context.Context, id string, op Operation) (*Result, error)

	// Delete destroys a resource and cleans up associated state.
	Delete(ctx context.Context, id string) error

	// Allocate is called when a resource transitions from ready to allocated
	// (i.e., when it is assigned to a sandbox). It performs allocation-time
	// work such as generating credentials or injecting SSH keys.
	//
	// Returns additional Properties to merge into the resource.
	// Returns nil, nil if no allocation work is needed.
	Allocate(ctx context.Context, id string) (map[string]any, error)
}

// Resource is returned by Driver.Create — the driver's output after
// provisioning a new resource.
type Resource struct {
	// ID is the provider-specific resource identifier
	// (container ID, VM name, etc.).
	ID string

	// ConnectionInfo describes how to reach the resource. Keys and values
	// are driver-defined (e.g. "host", "port", "container_id").
	ConnectionInfo map[string]string

	// Metadata holds additional driver-specific data that Boxy may
	// surface but does not interpret.
	Metadata map[string]string
}

// ResourceStatus is returned by Driver.Read.
type ResourceStatus struct {
	ID    string
	State string // Driver-defined: "running", "stopped", "error", etc.
}

// Operation is the input to Driver.Update. Each driver defines its own
// concrete operation types.
type Operation interface{}

// Result is returned by Driver.Update.
type Result struct {
	// Outputs holds key/value pairs produced by the operation
	// (e.g. generated credentials, captured stdout).
	Outputs map[string]string
}

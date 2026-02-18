package model

import "time"

// ResourceID is a stable identifier for a resource tracked by Boxy.
type ResourceID string

// ResourceType is the domain category of a resource (vm/container/share/etc).
type ResourceType string

const (
	ResourceTypeUnknown   ResourceType = "unknown"
	ResourceTypeVM        ResourceType = "vm"
	ResourceTypeContainer ResourceType = "container"
	ResourceTypeShare     ResourceType = "share"
	ResourceTypeNetwork   ResourceType = "network"
	ResourceTypeDB        ResourceType = "db"
)

// ResourceState is the lifecycle state of a resource.
type ResourceState string

const (
	ResourceStateUnknown      ResourceState = "unknown"
	ResourceStateProvisioning ResourceState = "provisioning"
	ResourceStateReady        ResourceState = "ready"
	ResourceStateAllocated    ResourceState = "allocated"
	ResourceStateReleased     ResourceState = "released"
	ResourceStateDestroying   ResourceState = "destroying"
	ResourceStateDestroyed    ResourceState = "destroyed"
	ResourceStateError        ResourceState = "error"
)

// Resource is a provisioned instance tracked by Boxy.
type Resource struct {
	ID ResourceID `json:"id,omitempty" yaml:"id,omitempty"`

	// Type is intrinsic to the resource, independent of which container holds it.
	Type ResourceType `json:"type,omitempty" yaml:"type,omitempty"`

	// Profile is a Boxy-defined variant identifier for this resource's Type.
	// Example: vm "win-2022", container "ubuntu-2204".
	Profile ResourceProfile `json:"profile,omitempty" yaml:"profile,omitempty"`

	// Provider identifies the external system instance this resource belongs to.
	Provider ProviderRef `json:"provider" yaml:"provider"`

	State ResourceState `json:"state,omitempty" yaml:"state,omitempty"`

	// Properties holds provider-specific data that Boxy core should not interpret.
	Properties map[string]any `json:"properties,omitempty" yaml:"properties,omitempty"`

	CreatedAt time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

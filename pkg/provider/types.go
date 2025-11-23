package provider

import (
	"time"

	"github.com/google/uuid"
)

// ResourceType represents the type of resource
type ResourceType string

const (
	ResourceTypeContainer ResourceType = "container"
	ResourceTypeVM        ResourceType = "vm"
	ResourceTypeProcess   ResourceType = "process"
)

// ResourceState represents the current state of a resource
type ResourceState string

const (
	StateProvisioning ResourceState = "provisioning" // Being created
	StateReady        ResourceState = "ready"        // Available in pool
	StateAllocated    ResourceState = "allocated"    // In use by a sandbox
	StateDestroying   ResourceState = "destroying"   // Being removed
	StateDestroyed    ResourceState = "destroyed"    // Fully removed
	StateError        ResourceState = "error"        // Failed provision or health check
)

// Resource represents a single compute unit (VM, container, or process)
// This is the core type that providers create and manage
type Resource struct {
	ID           string                 `json:"id"`
	PoolID       string                 `json:"pool_id"`
	SandboxID    *string                `json:"sandbox_id,omitempty"`
	Type         ResourceType           `json:"type"`
	State        ResourceState          `json:"state"`
	ProviderType string                 `json:"provider_type"` // docker, hyperv, kvm, etc.
	ProviderID   string                 `json:"provider_id"`   // Provider-specific ID
	Spec         map[string]interface{} `json:"spec"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	ExpiresAt    *time.Time             `json:"expires_at,omitempty"`
}

// NewResource creates a new resource with a generated UUID
func NewResource(poolID string, resourceType ResourceType, providerType string) *Resource {
	now := time.Now()
	return &Resource{
		ID:           uuid.New().String(),
		PoolID:       poolID,
		Type:         resourceType,
		State:        StateProvisioning,
		ProviderType: providerType,
		Spec:         make(map[string]interface{}),
		Metadata:     make(map[string]interface{}),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// IsAvailable returns true if the resource is ready to be allocated
func (r *Resource) IsAvailable() bool {
	return r.State == StateReady && r.SandboxID == nil
}

// IsAllocated returns true if the resource is currently in use
func (r *Resource) IsAllocated() bool {
	return r.SandboxID != nil && r.State == StateAllocated
}

// IsExpired returns true if the resource has passed its expiration time
func (r *Resource) IsExpired() bool {
	if r.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*r.ExpiresAt)
}

// ConnectionInfo contains the details needed to connect to a resource
type ConnectionInfo struct {
	Type        string                 `json:"type"` // ssh, rdp, http, docker-exec, etc.
	Host        string                 `json:"host,omitempty"`
	Port        int                    `json:"port,omitempty"`
	Username    string                 `json:"username,omitempty"`
	Password    string                 `json:"password,omitempty"`
	SSHKey      string                 `json:"ssh_key,omitempty"`
	ExtraFields map[string]interface{} `json:"extra_fields,omitempty"`
}

// ResourceStatus contains detailed status information about a resource
type ResourceStatus struct {
	State      ResourceState `json:"state"`
	Healthy    bool          `json:"healthy"`
	Message    string        `json:"message,omitempty"`
	LastCheck  time.Time     `json:"last_check"`
	Uptime     time.Duration `json:"uptime,omitempty"`
	CPUUsage   float64       `json:"cpu_usage,omitempty"`
	MemoryUsed uint64        `json:"memory_used,omitempty"`
}

// ResourceSpec defines the specification for provisioning a resource
type ResourceSpec struct {
	Type         ResourceType           `json:"type"`
	ProviderType string                 `json:"provider_type"`
	Image        string                 `json:"image"` // Docker image, VM template, base image, etc.
	CPUs         int                    `json:"cpus,omitempty"`
	MemoryMB     int                    `json:"memory_mb,omitempty"`
	DiskGB       int                    `json:"disk_gb,omitempty"`
	Labels       map[string]string      `json:"labels,omitempty"`
	Environment  map[string]string      `json:"environment,omitempty"`
	ExtraConfig  map[string]interface{} `json:"extra_config,omitempty"` // Provider-specific config
}

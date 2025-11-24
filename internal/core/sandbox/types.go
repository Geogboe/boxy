package sandbox

import (
	"time"

	"github.com/google/uuid"
)

// SandboxState represents the current state of a sandbox
type SandboxState string

const (
	StateCreating  SandboxState = "creating"  // Resources being provisioned
	StateReady     SandboxState = "ready"     // All resources allocated and ready
	StateExpiring  SandboxState = "expiring"  // Past expiration, cleanup pending
	StateDestroyed SandboxState = "destroyed" // All resources cleaned up
	StateError     SandboxState = "error"     // Failed to create or manage
)

// ResourceRequest specifies the resources needed for a sandbox
type ResourceRequest struct {
	PoolName string `json:"pool_name" yaml:"pool_name"`
	Count    int    `json:"count" yaml:"count"`
}

// Sandbox represents a logical collection of allocated resources
type Sandbox struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	State       SandboxState      `json:"state"`
	ResourceIDs []string          `json:"resource_ids"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
	CreatedBy   string            `json:"created_by,omitempty"` // Future: user/tenant ID
}

// NewSandbox creates a new sandbox with generated UUID
func NewSandbox(name string, duration time.Duration) *Sandbox {
	now := time.Now()
	expiresAt := now.Add(duration)

	return &Sandbox{
		ID:          uuid.New().String(),
		Name:        name,
		State:       StateCreating,
		ResourceIDs: []string{},
		Metadata:    make(map[string]string),
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   &expiresAt,
	}
}

// IsExpired returns true if the sandbox has passed its expiration time
func (s *Sandbox) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*s.ExpiresAt)
}

// TimeRemaining returns the duration until expiration
func (s *Sandbox) TimeRemaining() time.Duration {
	if s.ExpiresAt == nil {
		return 0
	}
	remaining := time.Until(*s.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// CreateRequest contains the parameters for creating a sandbox
type CreateRequest struct {
	Name      string            `json:"name,omitempty"`
	Resources []ResourceRequest `json:"resources"`
	Duration  time.Duration     `json:"duration"` // How long until auto-cleanup
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Validate checks if the create request is valid
func (r *CreateRequest) Validate() error {
	if len(r.Resources) == 0 {
		return ErrNoResourcesRequested
	}
	if r.Duration <= 0 {
		return ErrInvalidDuration
	}
	for _, res := range r.Resources {
		if res.PoolName == "" {
			return ErrInvalidPoolName
		}
		if res.Count <= 0 {
			return ErrInvalidResourceCount
		}
	}
	return nil
}

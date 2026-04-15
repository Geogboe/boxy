package model

// SandboxID is a stable identifier for a sandbox (user-facing handle).
type SandboxID string

// SandboxStatus is the lifecycle state of a sandbox request.
type SandboxStatus string

const (
	SandboxStatusPending      SandboxStatus = "pending"
	SandboxStatusProvisioning SandboxStatus = "provisioning"
	SandboxStatusReady        SandboxStatus = "ready"
	SandboxStatusFailed       SandboxStatus = "failed"
)

// Sandbox is a user-facing environment that contains 1..N resources.
//
// This model is intentionally minimal. Orchestration state and richer composition
// semantics are layered on later.
type Sandbox struct {
	ID   SandboxID `json:"id" yaml:"id"`
	Name string    `json:"name,omitempty" yaml:"name,omitempty"`

	// Policies are sandbox-level behavioral controls (security, retention, etc).
	Policies SandboxPolicies `json:"policies,omitempty" yaml:"policies,omitempty"`

	// Status is the async lifecycle state of the sandbox request.
	Status SandboxStatus `json:"status,omitempty" yaml:"status,omitempty"`

	// Requests are the desired resources for this sandbox.
	Requests []ResourceRequest `json:"requests,omitempty" yaml:"requests,omitempty"`

	// Error is a human-readable failure detail when Status=failed.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`

	// Resources are the resources that make up this sandbox.
	Resources []ResourceID `json:"resources,omitempty" yaml:"resources,omitempty"`
}

// SandboxPolicies captures sandbox-level behavior without prescribing a specific
// CLI/API surface yet.
type SandboxPolicies struct {
	// AutoDestroyAfter is an optional retention setting (e.g. "30m", "8h").
	// Empty means "no policy set here".
	AutoDestroyAfter string `json:"auto_destroy_after,omitempty" yaml:"auto_destroy_after,omitempty"`

	// SecurityProfile is an optional label for sandbox hardening posture.
	// Examples: "default", "lab", "pentest", "vdi".
	SecurityProfile string `json:"security_profile,omitempty" yaml:"security_profile,omitempty"`
}

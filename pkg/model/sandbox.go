package model

import "time"

// SandboxID is a stable identifier for a sandbox (user-facing handle).
type SandboxID string

// SandboxStatus is the lifecycle state of a sandbox request.
type SandboxStatus string

const (
	SandboxStatusPending      SandboxStatus = "pending"
	SandboxStatusProvisioning SandboxStatus = "provisioning"
	SandboxStatusReady        SandboxStatus = "ready"
	SandboxStatusDeleting     SandboxStatus = "deleting"
	SandboxStatusFailed       SandboxStatus = "failed"
)

// IsTransient reports whether the sandbox is actively changing state
// (pending/provisioning/deleting) rather than settled into a terminal state
// (ready/failed). This is the single source of truth for that distinction —
// consumers such as the web dashboard's "in progress" badge styling should
// call this instead of hardcoding the transient status list, so a future
// lifecycle status only needs to be classified here once.
func (s SandboxStatus) IsTransient() bool {
	switch s {
	case SandboxStatusPending, SandboxStatusProvisioning, SandboxStatusDeleting:
		return true
	default:
		return false
	}
}

// Sandbox is a user-facing environment that contains 1..N resources.
//
// This model is intentionally minimal. Orchestration state and richer composition
// semantics are layered on later.
type Sandbox struct {
	ID      SandboxID `json:"id" yaml:"id"`
	Name    string    `json:"name,omitempty" yaml:"name,omitempty"`
	OwnerID string    `json:"owner_id,omitempty" yaml:"owner_id,omitempty"`

	// Policies are sandbox-level behavioral controls (security, retention, etc).
	Policies SandboxPolicies `json:"policies,omitzero" yaml:"policies,omitempty"`

	// Status is the async lifecycle state of the sandbox request.
	Status SandboxStatus `json:"status,omitempty" yaml:"status,omitempty"`

	// Requests are the desired resources for this sandbox.
	Requests []ResourceRequest `json:"requests,omitempty" yaml:"requests,omitempty"`

	// Error is a human-readable failure detail when Status=failed.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`

	// Resources are the resources that make up this sandbox.
	Resources []ResourceID `json:"resources,omitempty" yaml:"resources,omitempty"`

	// ExpiresAt is the absolute time this sandbox should be automatically
	// destroyed, computed from Policies.AutoDestroyAfter at creation time.
	// Nil means no automatic expiry.
	ExpiresAt *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`

	// NetworkSegments records the private network segment(s) this sandbox
	// was given, one per agent/host its resources landed on (see
	// providersdk.NetworkIsolator / internal/sandbox.NetworkIsolatingAllocator).
	// Empty for any sandbox whose allocator doesn't support network
	// isolation (e.g. devfactory-backed) -- this is the expected steady
	// state for such sandboxes, not a partial/incomplete one.
	NetworkSegments []NetworkSegment `json:"network_segments,omitempty" yaml:"network_segments,omitempty"`
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

// NetworkSegment is one per-host private network segment a sandbox was
// given. Ref is an opaque provider-defined identifier (mirrors
// providersdk.SegmentRef as a plain string, so this package doesn't import
// providersdk) -- callers pass it back to the same agent/provider type
// unchanged, never interpreting it themselves.
// CIDR is the address range allocated to this segment. It is recorded here
// rather than in a separate allocation ledger so that the set of in-use
// ranges is derivable from the sandboxes that actually exist: a standalone
// ledger can drift from reality, and releasing a range becomes something
// that has to be remembered rather than something that falls out of
// deleting the sandbox. See #370 and
// docs/superpowers/specs/2026-09-22-global-segment-cidr-allocation-design.md.
type NetworkSegment struct {
	AgentID      string `json:"agent_id" yaml:"agent_id"`
	ProviderType string `json:"provider_type" yaml:"provider_type"`
	Ref          string `json:"ref" yaml:"ref"`
	CIDR         string `json:"cidr,omitempty" yaml:"cidr,omitempty"`
}

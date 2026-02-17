package model

// ProviderID is a stable identifier for a Provider.
//
// This is intended to be immutable once created (e.g., a UUID). It exists so
// other objects can reference a Provider even if its Name changes.
type ProviderID string

// ProviderName is the stable, user-facing handle for a Provider (typed in CLI/config).
type ProviderName string

// ProviderType identifies the provider type (e.g. "docker", "hyperv").
//
// A ProviderType selects which driver/adapter implementation can talk to a Provider.
type ProviderType string

// Provider is an external system Boxy can provision resources on.
//
// Important: Provider is data (a configured target). The code that "knows how to
// talk to a provider kind" lives elsewhere (often called a driver/adapter).
type Provider struct {
	// ID is an immutable identifier for stable references (optional early on).
	ID ProviderID `json:"id,omitempty" yaml:"id,omitempty"`

	// Name is a user-facing label (typed in CLI/config). You may treat this as
	// mutable; use ID for stable references if so.
	Name ProviderName `json:"name" yaml:"name"`
	Type ProviderType `json:"type" yaml:"type"`

	// Config is provider-specific connection/auth/settings.
	// Schema is defined by the driver for this Type.
	Config map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
}

// ProviderRef references a Provider without embedding its full configuration.
//
// Use this when an object needs stable routing ("which provider owns this?") and
// optional display/debug context, without duplicating Provider.Config everywhere.
type ProviderRef struct {
	// ID is the stable identifier used for routing and joins.
	ID ProviderID `json:"id" yaml:"id"`

	// Name and Type are optional cached fields for display/debugging.
	// Treat them as hints; the canonical values live on the referenced Provider.
	Name ProviderName `json:"name,omitempty" yaml:"name,omitempty"`
	Type ProviderType `json:"type,omitempty" yaml:"type,omitempty"`
}

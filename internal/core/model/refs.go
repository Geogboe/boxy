package model

// Ref Types
//
// These are small value objects used to reference other domain objects without
// embedding their full records (and especially without duplicating configs).

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


package model

// ProviderRef identifies the provider instance that owns a resource.
//
// This intentionally does not embed provider connection/config data. The
// canonical configured providers live in the runtime config (see providersdk.Instance).
//
// For now, Name is the stable handle and must match a configured provider name.
// If/when renames become a real feature, introduce an immutable ID here.
type ProviderRef struct {
	Name string `json:"name" yaml:"name"`

	// AgentID is the specific agent instance (embedded or remote) that
	// provisioned this resource, stamped once at creation time. It is
	// deliberately distinct from Name (the provider type/instance): once
	// more than one agent can advertise the same provider type, any
	// lifecycle call on an existing resource (Destroy, Allocate) must
	// route back to this exact agent rather than re-resolving by type,
	// or it risks silently misrouting to a different agent that knows
	// nothing about the resource. See docs/adr/0005-remote-agent-transport-and-registration.md.
	AgentID string `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`
}

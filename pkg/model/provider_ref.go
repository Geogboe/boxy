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
}

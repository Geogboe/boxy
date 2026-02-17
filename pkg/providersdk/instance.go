package providersdk

// Type identifies the provider kind (e.g. "docker", "hyperv").
type Type string

// Instance is a configured provider endpoint.
//
// Config is intentionally untyped in the SDK layer. Each Driver owns the schema
// for its Type.
type Instance struct {
	// Name is a stable, user-facing handle (sufficient for a POC).
	Name string `json:"name" yaml:"name"`

	Type Type `json:"type" yaml:"type"`

	// Labels can be used for selection/placement policies.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`

	// Config is a provider-kind-specific config blob.
	Config map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
}

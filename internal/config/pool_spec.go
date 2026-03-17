package config

// PoolSpec is the user-facing YAML/JSON representation of a poolmanager.
//
// This is intentionally decoupled from internal/model.Pool so we can evolve the
// runtime model while keeping the config interface stable.
type PoolSpec struct {
	Name string `json:"name" yaml:"name"`

	// Type is the pool kind as expressed in config.
	//
	// Examples: "container", "vm", and (for docker-based container pools) "docker".
	Type string `json:"type" yaml:"type"`

	// Provider is an optional provider instance name (e.g. "docker-local").
	// Some pool types (like "docker") may imply a default provider.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`

	// Config is provider/pool-type-specific configuration.
	Config map[string]any `json:"config,omitempty" yaml:"config,omitempty"`

	// Policy is the pool policy surface in config (examples use `policy:`).
	Policy PoolPolicySpec `json:"policy,omitempty" yaml:"policy,omitempty"`

	// Policies is accepted as an alias for Policy.
	Policies PoolPolicySpec `json:"policies,omitempty" yaml:"policies,omitempty"`
}

func (p PoolSpec) EffectivePolicy() PoolPolicySpec {
	if p.Policies.Preheat.MinReady != 0 || p.Policies.Preheat.MaxTotal != 0 || p.Policies.Recycle.MaxAge != "" {
		return p.Policies
	}
	return p.Policy
}

type PoolPolicySpec struct {
	Preheat PreheatPolicySpec `json:"preheat,omitempty" yaml:"preheat,omitempty"`
	Recycle RecyclePolicySpec `json:"recycle,omitempty" yaml:"recycle,omitempty"`
}

type PreheatPolicySpec struct {
	MinReady int `json:"min_ready,omitempty" yaml:"min_ready,omitempty"`
	MaxTotal int `json:"max_total,omitempty" yaml:"max_total,omitempty"`
}

type RecyclePolicySpec struct {
	MaxAge string `json:"max_age,omitempty" yaml:"max_age,omitempty"`
}

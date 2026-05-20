package model

// PoolName is the stable, user-facing handle for a pool (the thing typed in CLI/config).
type PoolName string

// Pool is a user-facing container of resources.
type Pool struct {
	Name PoolName `json:"name" yaml:"name"`

	// Policies are pool-level behavioral controls (preheating, limits, etc).
	Policies PoolPolicies `json:"policies,omitempty" yaml:"policies,omitempty"`

	// Drain records desired drain state for unused pool inventory.
	Drain PoolDrainState `json:"drain,omitempty" yaml:"drain,omitempty"`

	// Inventory is the current contents of the pool.
	Inventory ResourceCollection `json:"inventory" yaml:"inventory"`
}

// PoolPolicies captures pool-level behavior without prescribing a specific
// CLI/API surface yet.
type PoolPolicies struct {
	// Preheat controls keeping resources ready ahead of time.
	Preheat PreheatPolicy `json:"preheat,omitempty" yaml:"preheat,omitempty"`

	// Recycle controls periodic replacement of unused pool inventory.
	//
	// This does NOT mean resources are returned to the pool after sandbox use.
	// Resources remain single-use; recycle applies only to unused inventory.
	Recycle RecyclePolicy `json:"recycle,omitempty" yaml:"recycle,omitempty"`
}

// PoolDrainState records whether a pool should destroy and avoid creating
// unused ready inventory.
type PoolDrainState struct {
	// ConfigDeclared is true when config declares the pool drained.
	ConfigDeclared bool `json:"config_declared,omitempty" yaml:"config_declared,omitempty"`

	// Operator is a persisted operator/debug override.
	Operator bool `json:"operator,omitempty" yaml:"operator,omitempty"`
}

// Effective reports whether either config or operator state keeps the pool drained.
func (d PoolDrainState) Effective() bool {
	return d.ConfigDeclared || d.Operator
}

// EffectivelyDrained reports whether the pool should currently be drained.
func (p Pool) EffectivelyDrained() bool {
	return p.Drain.Effective()
}

// PreheatPolicy is the pool policy for keeping resources ready ahead of time.
type PreheatPolicy struct {
	// MinReady is the number of ready units the pool should try to keep available.
	MinReady int `json:"min_ready,omitempty" yaml:"min_ready,omitempty"`

	// MaxTotal is the maximum total units that may exist for the pool.
	MaxTotal int `json:"max_total,omitempty" yaml:"max_total,omitempty"`
}

// RecyclePolicy describes when unused resources should be destroyed and replaced.
type RecyclePolicy struct {
	// MaxAge is an optional upper bound on how long an unused resource may sit in
	// a pool before it should be recycled (destroy + replace).
	// Example values: "30m", "8h", "24h".
	MaxAge string `json:"max_age,omitempty" yaml:"max_age,omitempty"`
}

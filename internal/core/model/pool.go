package model

// Pool is a user-facing container of resources.
type Pool struct {
	Name PoolName `json:"name" yaml:"name"`

	// Policies are pool-level behavioral controls (preheating, limits, etc).
	Policies PoolPolicies `json:"policies,omitempty" yaml:"policies,omitempty"`

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

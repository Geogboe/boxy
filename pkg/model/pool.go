package model

import "time"

// PoolName is the stable, user-facing handle for a pool (the thing typed in CLI/config).
type PoolName string

// Pool is a user-facing container of resources.
type Pool struct {
	Name PoolName `json:"name" yaml:"name"`

	// Template is the reusable desired resource shape used by this pool.
	Template string `json:"template,omitempty" yaml:"template,omitempty"`

	// Source and Packages are the resolved resource configuration carried by
	// this pool. They are optional for legacy inline pools.
	Source   string   `json:"source,omitempty" yaml:"source,omitempty"`
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`

	// Policies are pool-level behavioral controls (preheating, limits, etc).
	Policies PoolPolicies `json:"policies,omitempty" yaml:"policies,omitempty"`

	// Drain records desired drain state for unused pool inventory.
	Drain PoolDrainState `json:"drain,omitempty" yaml:"drain,omitempty"`

	// Configuration records where editable policy values came from. Static
	// provider, template, store, and secret definitions remain local-file owned.
	Configuration PoolConfigurationState `json:"configuration,omitempty" yaml:"configuration,omitempty"`

	// Inventory is the current contents of the pool.
	Inventory ResourceCollection `json:"inventory" yaml:"inventory"`
}

type PoolConfigurationState struct {
	Provenance    string    `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	LocalRevision string    `json:"local_revision,omitempty" yaml:"local_revision,omitempty"`
	Pending       bool      `json:"pending,omitempty" yaml:"pending,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
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

	// Debug controls operator-opt-in troubleshooting behavior that trades
	// safety/cost for investigability. Debug settings are local-config-owned
	// only, like provider/template/store/secret definitions -- they are not
	// exposed through the pool Save-and-Apply web/API surface.
	Debug PoolDebugPolicy `json:"debug,omitempty" yaml:"debug,omitempty"`
}

// PoolDebugPolicy controls operator-opt-in troubleshooting behavior for a pool.
type PoolDebugPolicy struct {
	// RetainFailedResources keeps a resource's VM and rotated guest
	// credential running after admission (personalization or package
	// application) fails, instead of the default power-down-and-remove
	// teardown. A manual Retry against a retained resource re-admits it in
	// place -- reusing the same VM and credential -- rather than destroying
	// and recreating it from the template. Intended only for
	// troubleshooting: a retained failed resource still consumes pool
	// max_total capacity and can make the pool blocked exactly like any
	// other failed resource.
	RetainFailedResources bool `json:"retain_failed_resources,omitempty" yaml:"retain_failed_resources,omitempty"`
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

// PoolSummary is the safe, user-accessible view of a pool: just enough to
// construct a sandbox request (see internal/cli/sandbox_create.go's
// compileSandboxRequests). It deliberately omits everything Pool carries
// beyond name/type/profile -- inventory, policies, drain state, and
// configuration provenance are administrative detail a `user`-role caller
// has no need to see. See docs/adr for the accompanying decision record.
type PoolSummary struct {
	Name    PoolName        `json:"name" yaml:"name"`
	Type    ResourceType    `json:"type" yaml:"type"`
	Profile ResourceProfile `json:"profile" yaml:"profile"`
}

// Summary reduces a Pool to its PoolSummary. Adding a field to Pool never
// changes what Summary exposes -- callers who need to expose more must do so
// explicitly here, not by relying on struct-copy-and-blank-fields.
func (p Pool) Summary() PoolSummary {
	return PoolSummary{
		Name:    p.Name,
		Type:    p.Inventory.ExpectedType,
		Profile: p.Inventory.ExpectedProfile,
	}
}

// PreheatPolicy is the pool policy for keeping resources ready ahead of time.
type PreheatPolicy struct {
	// MinReady is the number of ready units the pool should try to keep
	// available in the background. It is a soft preheat target, not a
	// second hard admission cap stacked on top of MaxTotal: failing to
	// reach MinReady alone never rejects an allocation request (see
	// #240) -- though a request can still fail for other reasons, e.g. a
	// drained pool or a provisioning error. A live allocation call may
	// still opportunistically top up toward MinReady in the background,
	// so a failure in that best-effort top-up can itself surface as an
	// error even when the request was already satisfiable -- see #249
	// for that residual gap.
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

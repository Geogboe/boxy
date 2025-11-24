package hooks

import (
	"time"
)

// HookType represents the type of hook
type HookType string

const (
	HookTypeScript HookType = "script"
	// Future: HookTypeDomainJoin, HookTypeAnsible, etc.
)

// HookPoint represents when a hook executes in the lifecycle
type HookPoint string

const (
	// HookPointAfterProvision runs after provider.Provision() during pool warming
	// Purpose: Slow, background finalization (network validation, optional setup)
	HookPointAfterProvision HookPoint = "after_provision"

	// HookPointBeforeAllocate runs before allocating resource to user
	// Purpose: Fast personalization (create user account, set hostname)
	HookPointBeforeAllocate HookPoint = "before_allocate"
)

// ShellType represents the shell interpreter to use
type ShellType string

const (
	ShellBash       ShellType = "bash"
	ShellPowerShell ShellType = "powershell"
	ShellPython     ShellType = "python"
)

// Hook defines a lifecycle hook
type Hook struct {
	Name              string        `yaml:"name" json:"name"`
	Type              HookType      `yaml:"type" json:"type"`
	Shell             ShellType     `yaml:"shell" json:"shell"`                                                 // bash, powershell, python
	Inline            string        `yaml:"inline,omitempty" json:"inline,omitempty"`                           // Inline script content
	Path              string        `yaml:"path,omitempty" json:"path,omitempty"`                               // Path to script file
	Timeout           time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`                         // Individual hook timeout
	Retry             int           `yaml:"retry,omitempty" json:"retry,omitempty"`                             // Number of retry attempts
	ContinueOnFailure bool          `yaml:"continue_on_failure,omitempty" json:"continue_on_failure,omitempty"` // Don't fail if this hook fails
}

// HookConfig contains all hooks for a pool
type HookConfig struct {
	AfterProvision []Hook `yaml:"after_provision,omitempty" json:"after_provision,omitempty"`
	BeforeAllocate []Hook `yaml:"before_allocate,omitempty" json:"before_allocate,omitempty"`
	UseSystemHooks bool   `yaml:"use_system_hooks,omitempty" json:"use_system_hooks,omitempty"` // Enable Boxy's default hooks
}

// TimeoutConfig contains phase-level timeouts
type TimeoutConfig struct {
	Provision       time.Duration `yaml:"provision,omitempty" json:"provision,omitempty"`             // Provider.Provision() max time
	Finalization    time.Duration `yaml:"finalization,omitempty" json:"finalization,omitempty"`       // All after_provision hooks combined
	Personalization time.Duration `yaml:"personalization,omitempty" json:"personalization,omitempty"` // All before_allocate hooks combined
	Destroy         time.Duration `yaml:"destroy,omitempty" json:"destroy,omitempty"`                 // Provider.Destroy() max time
}

// DefaultTimeouts returns default timeout values
func DefaultTimeouts() TimeoutConfig {
	return TimeoutConfig{
		Provision:       5 * time.Minute,  // 300s
		Finalization:    10 * time.Minute, // 600s
		Personalization: 30 * time.Second, // 30s
		Destroy:         1 * time.Minute,  // 60s
	}
}

// HookResult contains the result of hook execution
type HookResult struct {
	Hook     string        `json:"hook_name"`
	Success  bool          `json:"success"`
	ExitCode int           `json:"exit_code"`
	Stdout   string        `json:"stdout,omitempty"`
	Stderr   string        `json:"stderr,omitempty"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
	Attempt  int           `json:"attempt"` // Which retry attempt (1 = first try)
}

// HookContext contains context information for hook execution
type HookContext struct {
	// Resource information
	ResourceID   string `json:"resource_id"`
	ResourceIP   string `json:"resource_ip,omitempty"`
	ResourceType string `json:"resource_type"`
	ProviderID   string `json:"provider_id"`
	PoolName     string `json:"pool_name"`

	// Credentials (for before_allocate hooks)
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// Additional metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

package pool

import (
	"time"

	"github.com/Geogboe/boxy/internal/core/lifecycle/hooks"
	"github.com/Geogboe/boxy/pkg/provider"
)

// PoolConfig defines the configuration for a resource pool
type PoolConfig struct {
	Name        string                 `yaml:"name" json:"name" mapstructure:"name"`
	Type        provider.ResourceType  `yaml:"type" json:"type" mapstructure:"type"`
	Backend     string                 `yaml:"backend" json:"backend" mapstructure:"backend"` // docker, hyperv, kvm, etc.
	Image       string                 `yaml:"image" json:"image" mapstructure:"image"`
	MinReady    int                    `yaml:"min_ready" json:"min_ready" mapstructure:"min_ready"` // Minimum resources to keep ready
	MaxTotal    int                    `yaml:"max_total" json:"max_total" mapstructure:"max_total"` // Maximum total resources (ready + allocated)
	CPUs        int                    `yaml:"cpus,omitempty" json:"cpus,omitempty" mapstructure:"cpus"`
	MemoryMB    int                    `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty" mapstructure:"memory_mb"`
	DiskGB      int                    `yaml:"disk_gb,omitempty" json:"disk_gb,omitempty" mapstructure:"disk_gb"`
	Labels      map[string]string      `yaml:"labels,omitempty" json:"labels,omitempty" mapstructure:"labels"`
	Environment map[string]string      `yaml:"environment,omitempty" json:"environment,omitempty" mapstructure:"environment"`
	ExtraConfig map[string]interface{} `yaml:"extra_config,omitempty" json:"extra_config,omitempty" mapstructure:"extra_config"`

	// Health check configuration
	HealthCheckInterval time.Duration `yaml:"health_check_interval,omitempty" json:"health_check_interval,omitempty" mapstructure:"health_check_interval"`

	// Hook configuration
	Hooks    hooks.HookConfig    `yaml:"hooks,omitempty" json:"hooks,omitempty" mapstructure:"hooks"`
	Timeouts hooks.TimeoutConfig `yaml:"timeouts,omitempty" json:"timeouts,omitempty" mapstructure:"timeouts"`
}

// Validate checks if the pool configuration is valid
func (c *PoolConfig) Validate() error {
	if c.Name == "" {
		return ErrInvalidPoolName
	}
	if c.Type == "" {
		return ErrInvalidResourceType
	}
	if c.Backend == "" {
		return ErrInvalidBackend
	}
	if c.Image == "" && c.Type != provider.ResourceTypeProcess {
		return ErrInvalidImage
	}
	if c.MinReady < 0 {
		return ErrInvalidMinReady
	}
	if c.MaxTotal < c.MinReady {
		return ErrInvalidMaxTotal
	}

	// Validate hooks (using new names after normalization)
	// ADR-008: Validate on_provision and on_allocate hooks
	for _, hook := range c.Hooks.OnProvision {
		if err := hooks.ValidateHook(hook); err != nil {
			return err
		}
	}
	for _, hook := range c.Hooks.OnAllocate {
		if err := hooks.ValidateHook(hook); err != nil {
			return err
		}
	}

	return nil
}

// ApplyDefaults applies default values for optional fields
func (c *PoolConfig) ApplyDefaults() {
	// Apply default timeouts if not set
	if c.Timeouts.Provision == 0 && c.Timeouts.Finalization == 0 &&
		c.Timeouts.Personalization == 0 && c.Timeouts.Destroy == 0 {
		c.Timeouts = hooks.DefaultTimeouts()
	} else {
		// Apply defaults for individual zero values
		defaults := hooks.DefaultTimeouts()
		if c.Timeouts.Provision == 0 {
			c.Timeouts.Provision = defaults.Provision
		}
		if c.Timeouts.Finalization == 0 {
			c.Timeouts.Finalization = defaults.Finalization
		}
		if c.Timeouts.Personalization == 0 {
			c.Timeouts.Personalization = defaults.Personalization
		}
		if c.Timeouts.Destroy == 0 {
			c.Timeouts.Destroy = defaults.Destroy
		}
	}
}

// ToResourceSpec converts pool configuration to a resource specification
func (c *PoolConfig) ToResourceSpec() provider.ResourceSpec {
	return provider.ResourceSpec{
		Type:         c.Type,
		ProviderType: c.Backend,
		Image:        c.Image,
		CPUs:         c.CPUs,
		MemoryMB:     c.MemoryMB,
		DiskGB:       c.DiskGB,
		Labels:       c.Labels,
		Environment:  c.Environment,
		ExtraConfig:  c.ExtraConfig,
	}
}

// PoolStats contains statistics about a pool
type PoolStats struct {
	Name              string    `json:"name"`
	TotalReady        int       `json:"total_ready"`
	TotalAllocated    int       `json:"total_allocated"`
	TotalProvisioning int       `json:"total_provisioning"`
	TotalError        int       `json:"total_error"`
	Total             int       `json:"total"`
	MinReady          int       `json:"min_ready"`
	MaxTotal          int       `json:"max_total"`
	Healthy           bool      `json:"healthy"` // true if TotalReady >= MinReady
	LastUpdate        time.Time `json:"last_update"`
}

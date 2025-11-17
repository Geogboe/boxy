package pool

import (
	"time"

	"github.com/Geogboe/boxy/internal/core/resource"
)

// PoolConfig defines the configuration for a resource pool
type PoolConfig struct {
	Name         string                 `yaml:"name" json:"name"`
	Type         resource.ResourceType  `yaml:"type" json:"type"`
	Backend      string                 `yaml:"backend" json:"backend"` // docker, hyperv, kvm, etc.
	Image        string                 `yaml:"image" json:"image"`
	MinReady     int                    `yaml:"min_ready" json:"min_ready"`         // Minimum resources to keep ready
	MaxTotal     int                    `yaml:"max_total" json:"max_total"`         // Maximum total resources (ready + allocated)
	CPUs         int                    `yaml:"cpus,omitempty" json:"cpus,omitempty"`
	MemoryMB     int                    `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`
	DiskGB       int                    `yaml:"disk_gb,omitempty" json:"disk_gb,omitempty"`
	Labels       map[string]string      `yaml:"labels,omitempty" json:"labels,omitempty"`
	Environment  map[string]string      `yaml:"environment,omitempty" json:"environment,omitempty"`
	ExtraConfig  map[string]interface{} `yaml:"extra_config,omitempty" json:"extra_config,omitempty"`

	// Health check configuration
	HealthCheckInterval time.Duration `yaml:"health_check_interval,omitempty" json:"health_check_interval,omitempty"`
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
	if c.Image == "" {
		return ErrInvalidImage
	}
	if c.MinReady < 0 {
		return ErrInvalidMinReady
	}
	if c.MaxTotal < c.MinReady {
		return ErrInvalidMaxTotal
	}
	return nil
}

// ToResourceSpec converts pool configuration to a resource specification
func (c *PoolConfig) ToResourceSpec() resource.ResourceSpec {
	return resource.ResourceSpec{
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
	Name          string    `json:"name"`
	TotalReady    int       `json:"total_ready"`
	TotalAllocated int      `json:"total_allocated"`
	TotalProvisioningint    `json:"total_provisioning"`
	TotalError    int       `json:"total_error"`
	Total         int       `json:"total"`
	MinReady      int       `json:"min_ready"`
	MaxTotal      int       `json:"max_total"`
	Healthy       bool      `json:"healthy"` // true if TotalReady >= MinReady
	LastUpdate    time.Time `json:"last_update"`
}

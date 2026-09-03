// Package docker provides a providersdk.Driver backed by the local docker CLI.
package docker

import "github.com/Geogboe/boxy/pkg/providersdk"

// ProviderType is the registry key for Docker providers.
const ProviderType = "docker"

// Config holds connection settings for a Docker daemon.
// This is the provider-level config from boxy.yaml providers[].config.
type Config struct {
	Host string `json:"host" yaml:"host"`
}

// CreateConfig holds pool-level config for creating a container.
// This is the pool-level config from boxy.yaml pools[].config.
type CreateConfig struct {
	Image string `json:"image" yaml:"image"`
	// Source is intentionally unsupported: Docker consumes image references,
	// not raw disk/source bytes. The field exists so JSON decoding can produce
	// an explicit incompatibility error instead of silently ignoring a source.
	Source  *providersdk.SourceDescriptor `json:"source,omitempty" yaml:"source,omitempty"`
	Command []any                         `json:"command" yaml:"command"`
	Env     map[string]string             `json:"env" yaml:"env"`
	Labels  map[string]string             `json:"labels" yaml:"labels"`
	Ports   []string                      `json:"ports" yaml:"ports"`

	// Resources
	CPU    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
}

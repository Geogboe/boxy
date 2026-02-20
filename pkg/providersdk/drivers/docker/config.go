// Package docker provides config types for the Docker provider driver.
package docker

import "fmt"

// Config holds connection settings for a Docker daemon.
type Config struct {
	// Host is the Docker endpoint, e.g. "unix:///var/run/docker.sock" or "tcp://host:2376".
	Host string
}

// DecodeConfig decodes a Docker provider config from a raw map (as stored in providersdk.Instance.Config).
// Defaults Host to "unix:///var/run/docker.sock" if absent or empty.
func DecodeConfig(m map[string]any) (Config, error) {
	var cfg Config
	if h, ok := m["host"]; ok {
		s, ok := h.(string)
		if !ok {
			return Config{}, fmt.Errorf("docker config: host must be a string, got %T", h)
		}
		cfg.Host = s
	}
	if cfg.Host == "" {
		cfg.Host = "unix:///var/run/docker.sock"
	}
	return cfg, nil
}

// Validate returns an error if the config is invalid.
func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("docker config: host is required")
	}
	return nil
}

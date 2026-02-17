package docker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config is the typed configuration for a docker providersdk.Instance.
//
// This is intentionally small scaffolding and may expand over time.
type Config struct {
	// Host is the docker daemon address.
	// Examples: unix:///var/run/docker.sock, tcp://docker-host:2376
	Host string `json:"host" yaml:"host"`

	// Context is a docker context name (alternative to Host).
	Context string `json:"context" yaml:"context"`

	// Namespace is a prefix/namespace for names/labels Boxy creates.
	Namespace string `json:"namespace" yaml:"namespace"`

	// DefaultLabels are labels applied to all created resources.
	DefaultLabels map[string]string `json:"default_labels" yaml:"default_labels"`

	TLS TLSConfig `json:"tls" yaml:"tls"`
}

type TLSConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	CAFile             string `json:"ca_file" yaml:"ca_file"`
	CertFile           string `json:"cert_file" yaml:"cert_file"`
	KeyFile            string `json:"key_file" yaml:"key_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
}

func DecodeConfig(raw map[string]any) (Config, error) {
	if len(raw) == 0 {
		return Config{}, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return Config{}, fmt.Errorf("marshal config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	// Require exactly one of host or context for determinism.
	if c.Host == "" && c.Context == "" {
		return fmt.Errorf("either host or context is required")
	}
	if c.Host != "" && c.Context != "" {
		return fmt.Errorf("only one of host or context may be set")
	}

	if c.Namespace == "" {
		c.Namespace = "boxy"
	}

	if c.Host != "" {
		if !(strings.HasPrefix(c.Host, "unix://") || strings.HasPrefix(c.Host, "tcp://")) {
			return fmt.Errorf("host must start with unix:// or tcp://")
		}
	}

	if c.TLS.Enabled {
		// TLS only makes sense for remote TCP endpoints.
		if c.Host == "" || !strings.HasPrefix(c.Host, "tcp://") {
			return fmt.Errorf("tls.enabled requires a tcp:// host")
		}
	}

	return nil
}

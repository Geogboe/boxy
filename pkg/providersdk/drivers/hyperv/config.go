package hyperv

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config is the typed configuration for a hyperv providersdk.Instance.
//
// This is intentionally minimal scaffolding.
type Config struct {
	// Endpoint is the Hyper-V management endpoint (preferred).
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	// Host is accepted as an alias for Endpoint to match example configs.
	Host string `json:"host" yaml:"host"`

	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`

	// Credential is an optional reference to an external credential (future).
	Credential string `json:"credential" yaml:"credential"`
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
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(c.Host)
	}
	if endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	return nil
}

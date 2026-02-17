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
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
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
	if strings.TrimSpace(c.Endpoint) == "" {
		return fmt.Errorf("endpoint is required")
	}
	return nil
}

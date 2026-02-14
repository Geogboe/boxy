package docker

import (
	"encoding/json"
	"fmt"
)

func DecodeConfig(m map[string]any) (Config, error) {
	if m == nil {
		return Config{}, nil
	}

	// Decode via JSON so nested objects (tls/default_labels) map cleanly.
	b, err := json.Marshal(m)
	if err != nil {
		return Config{}, fmt.Errorf("marshal: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}
	return cfg, nil
}


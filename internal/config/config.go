package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"gopkg.in/yaml.v3"
)

// Config is the top-level Boxy configuration file structure.
//
// Keep this intentionally small while the CLI wiring lands. Expand as core
// managers gain real behavior.
type Config struct {
	Providers []providersdk.Instance `json:"providers" yaml:"providers"`
	Pools     []PoolSpec             `json:"pools,omitempty" yaml:"pools,omitempty"`

	Server ServerSpec `json:"server,omitempty" yaml:"server,omitempty"`

	Agents []AgentSpec `json:"agents,omitempty" yaml:"agents,omitempty"`
}

type ServerSpec struct {
	Listen    string   `json:"listen,omitempty" yaml:"listen,omitempty"`
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`

	// UI controls whether the web dashboard is served alongside the API.
	// Pointer so nil = default (enabled). Set to false to disable.
	UI *bool `json:"ui,omitempty" yaml:"ui,omitempty"`
}

// UIEnabled reports whether the web UI should be served.
// Returns true when UI is nil (unset) or explicitly true.
func (s ServerSpec) UIEnabled() bool {
	return s.UI == nil || *s.UI
}

type AgentSpec struct {
	Name      string   `json:"name" yaml:"name"`
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`
}

func LoadFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	switch ext := filepath.Ext(path); ext {
	case ".yaml", ".yml":
		return decodeYAML(b)
	case ".json":
		return decodeJSON(b)
	default:
		return Config{}, fmt.Errorf("unsupported config extension %q (supported: .yaml, .yml, .json)", ext)
	}
}

func decodeYAML(b []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode yaml: %w", err)
	}
	return cfg, nil
}

func decodeJSON(b []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode json: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("decode json: unexpected extra content after document")
	} else if err != io.EOF {
		return fmt.Errorf("decode json: trailing content: %w", err)
	}
	return nil
}

// Validate checks semantic config constraints that decoding alone does not enforce.
func (c Config) Validate() error {
	for _, pool := range c.Pools {
		if _, err := ResolvePoolExpectedType(pool.Type); err != nil {
			return fmt.Errorf("pool %q type invalid: %w", pool.Name, err)
		}
	}
	return nil
}

// ResolvePoolExpectedType maps a config pool type to the runtime resource type.
func ResolvePoolExpectedType(t string) (model.ResourceType, error) {
	switch strings.TrimSpace(t) {
	case "", "container", "docker":
		return model.ResourceTypeContainer, nil
	case "vm":
		return model.ResourceTypeVM, nil
	case "share":
		return model.ResourceTypeShare, nil
	default:
		return model.ResourceTypeUnknown, fmt.Errorf("unsupported pool type %q", t)
	}
}

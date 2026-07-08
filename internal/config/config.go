package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	Server ServerSpec `json:"server,omitzero" yaml:"server,omitempty"`
}

type ServerSpec struct {
	Listen    string   `json:"listen,omitempty" yaml:"listen,omitempty"`
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`

	// UI controls whether the web dashboard is served alongside the API.
	// Pointer so nil = default (enabled). Set to false to disable.
	UI *bool `json:"ui,omitempty" yaml:"ui,omitempty"`

	// GRPCListen is the address the agent-transport gRPC server listens
	// on (see docs/adr/0005-remote-agent-transport-and-registration.md).
	// Empty means the default (":9091").
	GRPCListen string `json:"grpc_listen,omitempty" yaml:"grpc_listen,omitempty"`

	// AgentHeartbeatInterval is how often connected remote agents send
	// heartbeats, as a Go duration string (e.g. "15s"). Empty means the
	// default (15s). Note: --insecure/--dev is deliberately a CLI flag
	// only, never a config field, so a stale or copy-pasted config file
	// can't silently disable mTLS in a real deployment.
	AgentHeartbeatInterval string `json:"agent_heartbeat_interval,omitempty" yaml:"agent_heartbeat_interval,omitempty"`
}

// UIEnabled reports whether the web UI should be served.
// Returns true when UI is nil (unset) or explicitly true.
func (s ServerSpec) UIEnabled() bool {
	return s.UI == nil || *s.UI
}

// DefaultAgentHeartbeatInterval is used when agent_heartbeat_interval is
// unset — close to the daemon's existing 10s reconcile tick.
const DefaultAgentHeartbeatInterval = 15 * time.Second

// EffectiveAgentHeartbeatInterval parses AgentHeartbeatInterval, applying
// the default when unset. Invalid values error (Validate also rejects them
// at load time, so a running daemon should never hit that path).
func (s ServerSpec) EffectiveAgentHeartbeatInterval() (time.Duration, error) {
	if strings.TrimSpace(s.AgentHeartbeatInterval) == "" {
		return DefaultAgentHeartbeatInterval, nil
	}
	d, err := time.ParseDuration(s.AgentHeartbeatInterval)
	if err != nil {
		return 0, fmt.Errorf("agent_heartbeat_interval %q: %w", s.AgentHeartbeatInterval, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("agent_heartbeat_interval %q must be positive", s.AgentHeartbeatInterval)
	}
	return d, nil
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
	if _, err := c.Server.EffectiveAgentHeartbeatInterval(); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	for _, pool := range c.Pools {
		if _, err := ResolvePoolExpectedType(pool.Type); err != nil {
			return fmt.Errorf("pool %q type invalid: %w", pool.Name, err)
		}
		if pool.PolicySet() && pool.PoliciesSet() {
			return fmt.Errorf("pool %q sets both policy and policies; use only one", pool.Name)
		}
		policy := pool.EffectivePolicy()
		if policy.Preheat.ConfiguresDrain() && policy.Preheat.MinReady > 0 {
			return fmt.Errorf("pool %q preheat max_total: 0 conflicts with min_ready: %d", pool.Name, policy.Preheat.MinReady)
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

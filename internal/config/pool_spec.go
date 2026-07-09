package config

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// PoolSpec is the user-facing YAML/JSON representation of a poolmanager.
//
// This is intentionally decoupled from internal/model.Pool so we can evolve the
// runtime model while keeping the config interface stable.
type PoolSpec struct {
	Name string `json:"name" yaml:"name"`

	// Type is the pool kind as expressed in config.
	//
	// Examples: "container", "vm", and (for docker-based container pools) "docker".
	Type string `json:"type" yaml:"type"`

	// Provider is an optional provider instance name (e.g. "docker-local").
	// Some pool types (like "docker") may imply a default provider.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`

	// Config is provider/pool-type-specific configuration.
	Config map[string]any `json:"config,omitempty" yaml:"config,omitempty"`

	// Agent optionally pins this pool to a specific agent instance ID
	// (embedded or remote), for when more than one agent supports the
	// same provider type. Empty means "resolve by provider type across
	// all available agents" (the only behavior possible before remote
	// agents existed).
	Agent string `json:"agent,omitempty" yaml:"agent,omitempty"`

	// Policy is the pool policy surface in config (examples use `policy:`).
	Policy PoolPolicySpec `json:"policy,omitempty" yaml:"policy,omitempty"`

	// Policies is accepted as an alias for Policy.
	Policies PoolPolicySpec `json:"policies,omitempty" yaml:"policies,omitempty"`

	policySet   bool
	policiesSet bool
}

func (p PoolSpec) EffectivePolicy() PoolPolicySpec {
	if p.PoliciesSet() {
		return p.Policies
	}
	return p.Policy
}

func (p PoolSpec) PolicySet() bool {
	return p.policySet || p.Policy.hasValues()
}

func (p PoolSpec) PoliciesSet() bool {
	return p.policiesSet || p.Policies.hasValues()
}

type PoolPolicySpec struct {
	Preheat PreheatPolicySpec `json:"preheat,omitempty" yaml:"preheat,omitempty"`
	Recycle RecyclePolicySpec `json:"recycle,omitempty" yaml:"recycle,omitempty"`
}

func (p PoolPolicySpec) hasValues() bool {
	return p.Preheat.MinReady != 0 ||
		p.Preheat.MaxTotal != 0 ||
		p.Preheat.MinReadySet() ||
		p.Preheat.MaxTotalSet() ||
		p.Recycle.MaxAge != ""
}

type PreheatPolicySpec struct {
	MinReady int `json:"min_ready,omitempty" yaml:"min_ready,omitempty"`
	MaxTotal int `json:"max_total,omitempty" yaml:"max_total,omitempty"`

	minReadySet bool
	maxTotalSet bool
}

func (p PreheatPolicySpec) MinReadySet() bool {
	return p.minReadySet
}

func (p PreheatPolicySpec) MaxTotalSet() bool {
	return p.maxTotalSet
}

func (p PreheatPolicySpec) ConfiguresDrain() bool {
	return p.maxTotalSet && p.MaxTotal == 0
}

type RecyclePolicySpec struct {
	MaxAge string `json:"max_age,omitempty" yaml:"max_age,omitempty"`
}

func (p *PoolPolicySpec) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		switch key {
		case "preheat":
			if err := json.Unmarshal(raw, &p.Preheat); err != nil {
				return fmt.Errorf("preheat: %w", err)
			}
		case "recycle":
			if err := json.Unmarshal(raw, &p.Recycle); err != nil {
				return fmt.Errorf("recycle: %w", err)
			}
		default:
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	return nil
}

func (p *PoolPolicySpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("pool policy must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]
		switch key {
		case "preheat":
			if err := val.Decode(&p.Preheat); err != nil {
				return fmt.Errorf("preheat: %w", err)
			}
		case "recycle":
			if err := val.Decode(&p.Recycle); err != nil {
				return fmt.Errorf("recycle: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type config.PoolPolicySpec", key)
		}
	}
	return nil
}

func (p *RecyclePolicySpec) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		switch key {
		case "max_age":
			if err := json.Unmarshal(raw, &p.MaxAge); err != nil {
				return fmt.Errorf("max_age: %w", err)
			}
		default:
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	return nil
}

func (p *RecyclePolicySpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("recycle policy must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]
		switch key {
		case "max_age":
			if err := val.Decode(&p.MaxAge); err != nil {
				return fmt.Errorf("max_age: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type config.RecyclePolicySpec", key)
		}
	}
	return nil
}

func (p *PoolSpec) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		switch key {
		case "name":
			if err := json.Unmarshal(raw, &p.Name); err != nil {
				return fmt.Errorf("name: %w", err)
			}
		case "type":
			if err := json.Unmarshal(raw, &p.Type); err != nil {
				return fmt.Errorf("type: %w", err)
			}
		case "provider":
			if err := json.Unmarshal(raw, &p.Provider); err != nil {
				return fmt.Errorf("provider: %w", err)
			}
		case "config":
			if err := json.Unmarshal(raw, &p.Config); err != nil {
				return fmt.Errorf("config: %w", err)
			}
		case "agent":
			if err := json.Unmarshal(raw, &p.Agent); err != nil {
				return fmt.Errorf("agent: %w", err)
			}
		case "policy":
			p.policySet = true
			if err := json.Unmarshal(raw, &p.Policy); err != nil {
				return fmt.Errorf("policy: %w", err)
			}
		case "policies":
			p.policiesSet = true
			if err := json.Unmarshal(raw, &p.Policies); err != nil {
				return fmt.Errorf("policies: %w", err)
			}
		default:
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	return nil
}

func (p *PoolSpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("pool spec must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]
		switch key {
		case "name":
			if err := val.Decode(&p.Name); err != nil {
				return fmt.Errorf("name: %w", err)
			}
		case "type":
			if err := val.Decode(&p.Type); err != nil {
				return fmt.Errorf("type: %w", err)
			}
		case "provider":
			if err := val.Decode(&p.Provider); err != nil {
				return fmt.Errorf("provider: %w", err)
			}
		case "config":
			if err := val.Decode(&p.Config); err != nil {
				return fmt.Errorf("config: %w", err)
			}
		case "agent":
			if err := val.Decode(&p.Agent); err != nil {
				return fmt.Errorf("agent: %w", err)
			}
		case "policy":
			p.policySet = true
			if err := val.Decode(&p.Policy); err != nil {
				return fmt.Errorf("policy: %w", err)
			}
		case "policies":
			p.policiesSet = true
			if err := val.Decode(&p.Policies); err != nil {
				return fmt.Errorf("policies: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type config.PoolSpec", key)
		}
	}
	return nil
}

func (p *PreheatPolicySpec) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for key, raw := range fields {
		switch key {
		case "min_ready":
			p.minReadySet = true
			if err := json.Unmarshal(raw, &p.MinReady); err != nil {
				return fmt.Errorf("min_ready: %w", err)
			}
		case "max_total":
			p.maxTotalSet = true
			if err := json.Unmarshal(raw, &p.MaxTotal); err != nil {
				return fmt.Errorf("max_total: %w", err)
			}
		default:
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	return nil
}

func (p *PreheatPolicySpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("preheat policy must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]
		switch key {
		case "min_ready":
			p.minReadySet = true
			if err := val.Decode(&p.MinReady); err != nil {
				return fmt.Errorf("min_ready: %w", err)
			}
		case "max_total":
			p.maxTotalSet = true
			if err := val.Decode(&p.MaxTotal); err != nil {
				return fmt.Errorf("max_total: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type config.PreheatPolicySpec", key)
		}
	}
	return nil
}

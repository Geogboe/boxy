package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SandboxSpec is the user-facing YAML representation used by `boxy sandbox create`.
type SandboxSpec struct {
	Name      string            `json:"name" yaml:"name"`
	Resources []SandboxResource `json:"resources" yaml:"resources"`
	Policies  map[string]any    `json:"policies,omitempty" yaml:"policies,omitempty"`
	Metadata  map[string]any    `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type SandboxResource struct {
	// Name is an optional label for the resource group.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	Pool  string `json:"pool" yaml:"pool"`
	Count int    `json:"count" yaml:"count"`
}

func LoadSandboxFile(path string) (SandboxSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SandboxSpec{}, fmt.Errorf("read sandbox spec %q: %w", path, err)
	}
	switch ext := filepath.Ext(path); ext {
	case ".yaml", ".yml":
		return decodeSandboxYAML(b)
	default:
		return SandboxSpec{}, fmt.Errorf("unsupported sandbox spec extension %q (supported: .yaml, .yml)", ext)
	}
}

func decodeSandboxYAML(b []byte) (SandboxSpec, error) {
	var spec SandboxSpec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		if err == io.EOF {
			return SandboxSpec{}, nil
		}
		return SandboxSpec{}, fmt.Errorf("decode sandbox yaml: %w", err)
	}
	return spec, nil
}

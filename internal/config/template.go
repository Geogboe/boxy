package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/Geogboe/boxy/pkg/artifact"
	"github.com/Geogboe/boxy/pkg/model"
	"gopkg.in/yaml.v3"
)

// TemplateSpec is the user-facing definition under Config.templates.
type TemplateSpec struct {
	Extends  string         `json:"extends,omitempty" yaml:"extends,omitempty"`
	Type     string         `json:"type,omitempty" yaml:"type,omitempty"`
	Provider string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Agent    string         `json:"agent,omitempty" yaml:"agent,omitempty"`
	Source   string         `json:"source,omitempty" yaml:"source,omitempty"`
	Config   map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
	Packages []string       `json:"packages,omitempty" yaml:"packages,omitempty"`
}

// SourceSpec registers an externally owned immutable source in a named store.
type SourceSpec struct {
	Store    string            `json:"store" yaml:"store"`
	Path     string            `json:"path" yaml:"path"`
	Digest   string            `json:"digest" yaml:"digest"`
	Format   string            `json:"format,omitempty" yaml:"format,omitempty"`
	OS       string            `json:"os,omitempty" yaml:"os,omitempty"`
	Provider string            `json:"provider,omitempty" yaml:"provider,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ArtifactStoreSpec describes a physical artifact backend. Credentials are
// references, not secret values.
type ArtifactStoreSpec struct {
	Type      string `json:"type" yaml:"type"`
	Endpoint  string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	Path      string `json:"path,omitempty" yaml:"path,omitempty"`
	AccessKey string `json:"access_key,omitempty" yaml:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty" yaml:"secret_key,omitempty"`
}

// ResolveTemplate returns a fully inherited template. Parent package lists
// are retained in order and child packages are appended. Child scalar fields
// override non-empty parent values and config keys are shallow-merged.
func (c Config) ResolveTemplate(name string) (model.ResourceTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.ResourceTemplate{}, fmt.Errorf("template name is required")
	}
	visiting := make(map[string]bool)
	var resolve func(string) (model.ResourceTemplate, error)
	resolve = func(current string) (model.ResourceTemplate, error) {
		if visiting[current] {
			return model.ResourceTemplate{}, fmt.Errorf("template inheritance cycle at %q", current)
		}
		spec, ok := c.Templates[current]
		if !ok {
			return model.ResourceTemplate{}, fmt.Errorf("template %q not found", current)
		}
		visiting[current] = true
		defer delete(visiting, current)

		resolved := model.ResourceTemplate{Name: current}
		if parent := strings.TrimSpace(spec.Extends); parent != "" {
			var err error
			resolved, err = resolve(parent)
			if err != nil {
				return model.ResourceTemplate{}, err
			}
			resolved.Name = current
			resolved.Extends = parent
		}
		if spec.Type != "" {
			resolved.Type = spec.Type
		}
		if spec.Provider != "" {
			resolved.Provider = spec.Provider
		}
		if spec.Agent != "" {
			resolved.Agent = spec.Agent
		}
		if spec.Source != "" {
			resolved.Source = spec.Source
		}
		if len(spec.Config) != 0 {
			if resolved.Config == nil {
				resolved.Config = make(map[string]any)
			}
			for key, value := range spec.Config {
				resolved.Config[key] = value
			}
		}
		resolved.Packages = append(resolved.Packages, spec.Packages...)
		return resolved, nil
	}
	resolved, err := resolve(name)
	if err != nil {
		return model.ResourceTemplate{}, err
	}
	if strings.TrimSpace(resolved.Type) == "" {
		return model.ResourceTemplate{}, fmt.Errorf("template %q must define type or extend a template with type", name)
	}
	return resolved, nil
}

// ResolvePoolSpec applies a pool's template and retains the legacy inline
// fields as pool-level overrides.
func (c Config) ResolvePoolSpec(spec PoolSpec) (PoolSpec, error) {
	if strings.TrimSpace(spec.Template) == "" {
		return spec, nil
	}
	template, err := c.ResolveTemplate(spec.Template)
	if err != nil {
		return PoolSpec{}, err
	}
	resolved := spec
	if resolved.Type == "" {
		resolved.Type = template.Type
	}
	if resolved.Provider == "" {
		resolved.Provider = template.Provider
	}
	if resolved.Agent == "" {
		resolved.Agent = template.Agent
	}
	if resolved.Source == "" {
		resolved.Source = template.Source
	}
	if len(template.Config) != 0 {
		merged := make(map[string]any, len(template.Config)+len(spec.Config))
		for key, value := range template.Config {
			merged[key] = value
		}
		for key, value := range spec.Config {
			merged[key] = value
		}
		resolved.Config = merged
	}
	resolved.Packages = append(append([]string(nil), template.Packages...), spec.Packages...)
	return resolved, nil
}

// ResolvePoolSpecs returns the effective pool specs in their configuration
// order. This is the boundary used by daemon wiring so all downstream code can
// continue to work with the established PoolSpec shape.
func (c Config) ResolvePoolSpecs() ([]PoolSpec, error) {
	resolved := make([]PoolSpec, 0, len(c.Pools))
	for _, spec := range c.Pools {
		pool, err := c.ResolvePoolSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("pool %q template: %w", spec.Name, err)
		}
		resolved = append(resolved, pool)
	}
	return resolved, nil
}

// TemplateParents returns the configured single-parent template edges for
// consumers that need to reason about promotion lineage.
func (c Config) TemplateParents() map[string]string {
	parents := make(map[string]string, len(c.Templates))
	for name, spec := range c.Templates {
		if parent := strings.TrimSpace(spec.Extends); parent != "" {
			parents[name] = parent
		}
	}
	return parents
}

// PackageRegistry builds the in-config package registry used by the embedded
// server. Published-store adapters can replace this registry later without
// changing resourcepack's planning contract.
func (c Config) PackageRegistry(ctx context.Context) (*artifact.MemoryRegistry, error) {
	registry := artifact.NewMemoryRegistry()
	for key, manifest := range c.Packages {
		if manifest.Name == "" {
			manifest.Name = key
		}
		if err := manifest.Validate(); err != nil {
			return nil, fmt.Errorf("package %q: %w", key, err)
		}
		payload, err := yaml.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("encode package %q: %w", key, err)
		}
		if err := registry.Publish(ctx, artifact.Artifact{
			Kind:     artifact.KindPackage,
			Ref:      artifact.Ref{Kind: artifact.KindPackage, Name: manifest.Name, Version: manifest.Version},
			Manifest: payload,
		}); err != nil {
			return nil, err
		}
	}
	for name, source := range c.Sources {
		if err := registry.PutSource(ctx, artifact.Source{
			Name: name, Store: source.Store, Path: source.Path, Digest: source.Digest,
			Format: source.Format, OS: source.OS, Provider: source.Provider, Metadata: source.Metadata,
		}); err != nil {
			return nil, fmt.Errorf("source %q: %w", name, err)
		}
	}
	return registry, nil
}

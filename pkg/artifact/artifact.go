// Package artifact defines the common identity and registry seam for Boxy's
// typed immutable artifacts. Sources and resource packages use the same
// registry facade while retaining different metadata models.
package artifact

import (
	"context"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// Kind identifies the typed artifact manifest stored in the registry.
type Kind string

const (
	KindSource  Kind = "source"
	KindPackage Kind = "package"
)

// Ref is an immutable artifact identity. Version is required for published
// package references so a configuration cannot silently resolve to mutable
// content.
type Ref struct {
	Kind    Kind   `json:"kind" yaml:"kind"`
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (r Ref) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

// ParseRef parses the user-facing name@version form.
func ParseRef(raw string) (Ref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Ref{}, fmt.Errorf("artifact reference is required")
	}
	name, version, ok := strings.Cut(raw, "@")
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	if !ok || name == "" || version == "" || strings.Contains(version, "@") {
		return Ref{}, fmt.Errorf("artifact reference %q must use name@version", raw)
	}
	return Ref{Name: name, Version: version}, nil
}

// Artifact is the immutable package payload returned by Registry.Resolve.
// Manifest is the typed artifact's YAML or JSON manifest; Blobs contains
// content-addressed supporting files keyed by their digest or manifest name.
type Artifact struct {
	Kind     Kind              `json:"kind" yaml:"kind"`
	Ref      Ref               `json:"ref" yaml:"ref"`
	Manifest []byte            `json:"manifest" yaml:"manifest"`
	Blobs    map[string][]byte `json:"blobs,omitempty" yaml:"blobs,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Source is a catalog entry for externally owned immutable bytes.
type Source struct {
	Name     string            `json:"name" yaml:"name"`
	Store    string            `json:"store" yaml:"store"`
	Path     string            `json:"path" yaml:"path"`
	Digest   string            `json:"digest" yaml:"digest"`
	Format   string            `json:"format,omitempty" yaml:"format,omitempty"`
	OS       string            `json:"os,omitempty" yaml:"os,omitempty"`
	Provider string            `json:"provider,omitempty" yaml:"provider,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Registry is the one logical artifact facade used by Boxy. Implementations
// may use any number of physical stores behind it.
type Registry interface {
	Resolve(ctx context.Context, ref Ref) (Artifact, error)
	ResolveSource(ctx context.Context, name string) (Source, error)
	Publish(ctx context.Context, artifact Artifact) error
	PutSource(ctx context.Context, source Source) error
}

// MemoryRegistry is a concurrency-safe registry useful for tests and the
// devfactory reference path.
type MemoryRegistry struct {
	mu        sync.RWMutex
	artifacts map[string]Artifact
	sources   map[string]Source
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		artifacts: make(map[string]Artifact),
		sources:   make(map[string]Source),
	}
}

func (r *MemoryRegistry) Publish(_ context.Context, value Artifact) error {
	if r == nil {
		return fmt.Errorf("artifact registry is nil")
	}
	if value.Kind == "" {
		return fmt.Errorf("artifact kind is required")
	}
	if value.Ref.Name == "" || value.Ref.Version == "" {
		return fmt.Errorf("artifact name and version are required")
	}
	value.Ref.Kind = value.Kind
	key := value.Ref.String()
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.artifacts[keyFor(value.Kind, key)]; ok {
		if string(existing.Manifest) != string(value.Manifest) || !sameBlobs(existing.Blobs, value.Blobs) {
			return fmt.Errorf("artifact %q is immutable and already published with different content", key)
		}
		return nil
	}
	r.artifacts[keyFor(value.Kind, key)] = cloneArtifact(value)
	return nil
}

func (r *MemoryRegistry) Resolve(_ context.Context, ref Ref) (Artifact, error) {
	if r == nil {
		return Artifact{}, fmt.Errorf("artifact registry is nil")
	}
	if ref.Kind == "" {
		return Artifact{}, fmt.Errorf("artifact kind is required for %q", ref.String())
	}
	r.mu.RLock()
	value, ok := r.artifacts[keyFor(ref.Kind, ref.String())]
	r.mu.RUnlock()
	if !ok {
		return Artifact{}, fmt.Errorf("artifact %q %s not found", ref.Kind, ref.String())
	}
	return cloneArtifact(value), nil
}

func (r *MemoryRegistry) PutSource(_ context.Context, source Source) error {
	if r == nil {
		return fmt.Errorf("artifact registry is nil")
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Store) == "" || strings.TrimSpace(source.Path) == "" {
		return fmt.Errorf("source name, store, and path are required")
	}
	if err := ValidateDigest(source.Digest); err != nil {
		return fmt.Errorf("source %q: %w", source.Name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.sources[source.Name]; ok && !reflect.DeepEqual(existing, source) {
		return fmt.Errorf("source %q is immutable and already registered with different metadata", source.Name)
	}
	r.sources[source.Name] = cloneSource(source)
	return nil
}

// ValidateDigest accepts the digest notation used by source catalog records.
// Keeping this in the artifact package lets every registry enforce the same
// integrity contract before a source is referenced by a provider.
func ValidateDigest(digest string) error {
	digest = strings.TrimSpace(digest)
	algorithm, value, ok := strings.Cut(digest, ":")
	if !ok || strings.ToLower(algorithm) != "sha256" || len(value) != 64 {
		return fmt.Errorf("digest must use sha256:<64 hex characters>")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("digest must use sha256:<64 hex characters>: %w", err)
	}
	return nil
}

func (r *MemoryRegistry) ResolveSource(_ context.Context, name string) (Source, error) {
	if r == nil {
		return Source{}, fmt.Errorf("artifact registry is nil")
	}
	r.mu.RLock()
	source, ok := r.sources[strings.TrimSpace(name)]
	r.mu.RUnlock()
	if !ok {
		return Source{}, fmt.Errorf("source %q not found", name)
	}
	return cloneSource(source), nil
}

func keyFor(kind Kind, ref string) string { return string(kind) + ":" + ref }

func cloneArtifact(value Artifact) Artifact {
	value.Manifest = append([]byte(nil), value.Manifest...)
	if value.Blobs != nil {
		blobs := value.Blobs
		value.Blobs = make(map[string][]byte, len(blobs))
		for key, blob := range blobs {
			value.Blobs[key] = append([]byte(nil), blob...)
		}
	}
	if value.Metadata != nil {
		value.Metadata = cloneStrings(value.Metadata)
	}
	return value
}

func cloneSource(value Source) Source {
	if value.Metadata != nil {
		value.Metadata = cloneStrings(value.Metadata)
	}
	return value
}

func cloneStrings(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func sameBlobs(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for key, left := range a {
		right, ok := b[key]
		if !ok || string(left) != string(right) {
			return false
		}
	}
	return true
}

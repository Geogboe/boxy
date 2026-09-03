package artifact

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CompositeRegistry presents inline and published stores through one logical
// artifact facade. Resolution follows declaration order; publication goes to
// the first registry, which keeps the existing inline/local flow unchanged.
// A store error is returned immediately, while ordinary not-found results
// allow the next registry to satisfy a remote package reference.
type CompositeRegistry struct {
	Registries []Registry
}

func NewCompositeRegistry(registries ...Registry) *CompositeRegistry {
	filtered := make([]Registry, 0, len(registries))
	for _, registry := range registries {
		if registry != nil {
			filtered = append(filtered, registry)
		}
	}
	return &CompositeRegistry{Registries: filtered}
}

func (r *CompositeRegistry) Resolve(ctx context.Context, ref Ref) (Artifact, error) {
	if r == nil || len(r.Registries) == 0 {
		return Artifact{}, fmt.Errorf("artifact registry is empty")
	}
	var last error
	var found *Artifact
	for _, registry := range r.Registries {
		value, err := registry.Resolve(ctx, ref)
		if err == nil {
			if found == nil {
				copy := cloneArtifact(value)
				found = &copy
				continue
			}
			if !sameArtifact(*found, value) {
				return Artifact{}, fmt.Errorf("conflicting content for immutable artifact %q", ref.String())
			}
			continue
		}
		if !isNotFound(err) {
			return Artifact{}, err
		}
		last = err
	}
	if found != nil {
		return *found, nil
	}
	return Artifact{}, last
}

func (r *CompositeRegistry) ResolveSource(ctx context.Context, name string) (Source, error) {
	if r == nil || len(r.Registries) == 0 {
		return Source{}, fmt.Errorf("artifact registry is empty")
	}
	var last error
	var found *Source
	for _, registry := range r.Registries {
		value, err := registry.ResolveSource(ctx, name)
		if err == nil {
			if found == nil {
				copy := cloneSource(value)
				found = &copy
				continue
			}
			if !sameSource(*found, value) {
				return Source{}, fmt.Errorf("conflicting content for immutable source %q", name)
			}
			continue
		}
		if !isNotFound(err) {
			return Source{}, err
		}
		last = err
	}
	if found != nil {
		return *found, nil
	}
	return Source{}, last
}

func (r *CompositeRegistry) Publish(ctx context.Context, value Artifact) error {
	registry, err := r.first()
	if err != nil {
		return err
	}
	return registry.Publish(ctx, value)
}

func (r *CompositeRegistry) PutSource(ctx context.Context, source Source) error {
	registry, err := r.first()
	if err != nil {
		return err
	}
	return registry.PutSource(ctx, source)
}

func (r *CompositeRegistry) first() (Registry, error) {
	if r == nil || len(r.Registries) == 0 || r.Registries[0] == nil {
		return nil, fmt.Errorf("artifact registry is empty")
	}
	return r.Registries[0], nil
}

// ResolveSourceDescriptor delegates to the registry that owns the named
// source, if it supports direct source delivery. A caller can still resolve
// metadata through the composite and materialize a local path when no signer
// is available.
func (r *CompositeRegistry) ResolveSourceDescriptor(ctx context.Context, name string, ttl time.Duration) (SourceDescriptor, error) {
	var last error
	for _, registry := range r.Registries {
		if signer, ok := registry.(SourceDescriptorer); ok {
			descriptor, err := signer.ResolveSourceDescriptor(ctx, name, ttl)
			if err == nil {
				return descriptor, nil
			}
			if !isNotFound(err) {
				return SourceDescriptor{}, err
			}
			last = err
		}
	}
	if last != nil {
		return SourceDescriptor{}, last
	}
	return SourceDescriptor{}, fmt.Errorf("no registry can sign source %q", name)
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

package model

import "fmt"

// ResourceProfile is a Boxy-defined variant identifier for a ResourceType.
//
// Think of it as the "pooling key" beyond the coarse ResourceType:
// - VM: "win-2022", "ubuntu-2204", "ubuntu-2204-devbox"
// - Container: "ubuntu-2204", "ubuntu-2204-lamp"
// - Share: "default", "smb-basic"
//
// Profiles are type-dependent by design; "win-2022" only makes sense for vm.
type ResourceProfile string

const (
	// ResourceProfileDefault is the conventional "no special customization" profile.
	//
	// Using an explicit default keeps pools deterministic (no implicit meaning for "").
	ResourceProfileDefault ResourceProfile = "default"
)

// Profile is a catalog entry defining an allowed (Type, Profile) pair.
//
// This is intentionally minimal scaffolding. It exists to prevent "random strings
// at call sites" from becoming the de facto API.
type Profile struct {
	Type ResourceType    `json:"type" yaml:"type"`
	Name ResourceProfile `json:"name" yaml:"name"`
}

func (p Profile) Validate() error {
	if p.Type == ResourceTypeUnknown || p.Type == "" {
		return fmt.Errorf("profile type is required")
	}
	if p.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	return nil
}

// ProfileRegistry is a simple in-memory catalog of supported profiles.
//
// It is Boxy-owned (not provider-owned): a profile name is a stable identifier
// that Boxy components can match on across pools and requests.
type ProfileRegistry struct {
	byType map[ResourceType]map[ResourceProfile]struct{}
}

func NewProfileRegistry(profiles []Profile) (*ProfileRegistry, error) {
	r := &ProfileRegistry{
		byType: make(map[ResourceType]map[ResourceProfile]struct{}),
	}
	for i := range profiles {
		p := profiles[i]
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("profiles[%d] invalid: %w", i, err)
		}
		m, ok := r.byType[p.Type]
		if !ok {
			m = make(map[ResourceProfile]struct{})
			r.byType[p.Type] = m
		}
		if _, exists := m[p.Name]; exists {
			return nil, fmt.Errorf("duplicate profile: type=%q name=%q", p.Type, p.Name)
		}
		m[p.Name] = struct{}{}
	}
	return r, nil
}

func (r *ProfileRegistry) Has(t ResourceType, name ResourceProfile) bool {
	if r == nil {
		return false
	}
	m, ok := r.byType[t]
	if !ok {
		return false
	}
	_, ok = m[name]
	return ok
}

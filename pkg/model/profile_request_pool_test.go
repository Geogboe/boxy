package model

import (
	"strings"
	"testing"
)

func TestProfileRegistryValidatesAndFindsProfiles(t *testing.T) {
	registry, err := NewProfileRegistry([]Profile{
		{Type: ResourceTypeContainer, Name: "alpine"},
		{Type: ResourceTypeVM, Name: "win2025"},
	})
	if err != nil {
		t.Fatalf("NewProfileRegistry: %v", err)
	}
	if !registry.Has(ResourceTypeContainer, "alpine") {
		t.Fatal("registry missing container/alpine")
	}
	if registry.Has(ResourceTypeContainer, "win2025") {
		t.Fatal("registry matched profile on wrong type")
	}
	var nilRegistry *ProfileRegistry
	if nilRegistry.Has(ResourceTypeContainer, "alpine") {
		t.Fatal("nil registry reported profile present")
	}
}

func TestProfileRegistryRejectsInvalidAndDuplicateProfiles(t *testing.T) {
	tests := []struct {
		name     string
		profiles []Profile
		want     string
	}{
		{name: "missing type", profiles: []Profile{{Name: "alpine"}}, want: "profile type is required"},
		{name: "missing name", profiles: []Profile{{Type: ResourceTypeContainer}}, want: "profile name is required"},
		{
			name: "duplicate",
			profiles: []Profile{
				{Type: ResourceTypeContainer, Name: "alpine"},
				{Type: ResourceTypeContainer, Name: "alpine"},
			},
			want: "duplicate profile",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProfileRegistry(tt.profiles)
			if err == nil {
				t.Fatalf("NewProfileRegistry error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewProfileRegistry error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestResourceRequestValidate(t *testing.T) {
	valid := ResourceRequest{Type: ResourceTypeContainer, Profile: "alpine", Count: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate valid request: %v", err)
	}

	tests := []struct {
		name string
		req  ResourceRequest
		want string
	}{
		{name: "missing type", req: ResourceRequest{Profile: "alpine", Count: 1}, want: "request type is required"},
		{name: "unknown type", req: ResourceRequest{Type: ResourceTypeUnknown, Profile: "alpine", Count: 1}, want: "request type is required"},
		{name: "missing profile", req: ResourceRequest{Type: ResourceTypeContainer, Count: 1}, want: "request profile is required"},
		{name: "zero count", req: ResourceRequest{Type: ResourceTypeContainer, Profile: "alpine"}, want: "request count must be > 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if err == nil {
				t.Fatalf("Validate error = nil, want %q", tt.want)
			}
			if err.Error() != tt.want {
				t.Fatalf("Validate error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestPoolDrainEffective(t *testing.T) {
	tests := []struct {
		name  string
		drain PoolDrainState
		want  bool
	}{
		{name: "none"},
		{name: "config", drain: PoolDrainState{ConfigDeclared: true}, want: true},
		{name: "operator", drain: PoolDrainState{Operator: true}, want: true},
		{name: "both", drain: PoolDrainState{ConfigDeclared: true, Operator: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.drain.Effective(); got != tt.want {
				t.Fatalf("Effective() = %t, want %t", got, tt.want)
			}
			if got := (Pool{Drain: tt.drain}).EffectivelyDrained(); got != tt.want {
				t.Fatalf("EffectivelyDrained() = %t, want %t", got, tt.want)
			}
		})
	}
}

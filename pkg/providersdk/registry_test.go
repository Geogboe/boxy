package providersdk

import (
	"context"
	"strings"
	"testing"
)

type registryDriver struct{}

func (registryDriver) Type() Type { return "fake" }
func (registryDriver) Create(context.Context, any) (*Resource, error) {
	return &Resource{ID: "res-1"}, nil
}
func (registryDriver) Read(context.Context, string) (*ResourceStatus, error) {
	return &ResourceStatus{}, nil
}
func (registryDriver) Update(context.Context, string, Operation) (*Result, error) {
	return &Result{}, nil
}
func (registryDriver) Delete(context.Context, string) error { return nil }
func (registryDriver) Allocate(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func testRegistration() Registration {
	return Registration{
		Type:        "fake",
		ConfigProto: func() any { return &struct{}{} },
		NewDriver:   func(any) (Driver, error) { return registryDriver{}, nil },
	}
}

func TestRegistryRegisterGetAndTypes(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testRegistration()); err != nil {
		t.Fatalf("Register fake: %v", err)
	}
	if err := registry.Register(Registration{
		Type:        "alpha",
		ConfigProto: func() any { return &struct{}{} },
		NewDriver:   func(any) (Driver, error) { return registryDriver{}, nil },
	}); err != nil {
		t.Fatalf("Register alpha: %v", err)
	}

	if _, ok := registry.Get("fake"); !ok {
		t.Fatal("Get fake returned ok=false")
	}
	got := registry.Types()
	want := []Type{"alpha", "fake"}
	if len(got) != len(want) {
		t.Fatalf("Types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Types = %v, want %v", got, want)
		}
	}
}

func TestRegistryRegisterRejectsInvalidRegistrations(t *testing.T) {
	tests := []struct {
		name string
		reg  Registration
		want string
	}{
		{name: "missing type", reg: Registration{ConfigProto: func() any { return nil }, NewDriver: func(any) (Driver, error) { return registryDriver{}, nil }}, want: "registration type is empty"},
		{name: "missing config", reg: Registration{Type: "fake", NewDriver: func(any) (Driver, error) { return registryDriver{}, nil }}, want: "ConfigProto is nil"},
		{name: "missing factory", reg: Registration{Type: "fake", ConfigProto: func() any { return nil }}, want: "NewDriver is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewRegistry().Register(tt.reg)
			if err == nil {
				t.Fatalf("Register error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Register error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRegistryRegisterRejectsDuplicateType(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testRegistration()); err != nil {
		t.Fatalf("Register first: %v", err)
	}
	err := registry.Register(testRegistration())
	if err == nil {
		t.Fatal("Register duplicate error = nil")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("Register duplicate error = %q, want duplicate message", err.Error())
	}
}

func TestRegistryValidateInstances(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testRegistration()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.ValidateInstances(context.Background(), []Instance{{Name: "local", Type: "fake"}}); err != nil {
		t.Fatalf("ValidateInstances known provider: %v", err)
	}

	err := registry.ValidateInstances(context.Background(), []Instance{{Name: "bad", Type: "missing"}})
	if err == nil {
		t.Fatal("ValidateInstances unknown error = nil")
	}
	if !strings.Contains(err.Error(), `provider "bad": unknown type "missing"`) {
		t.Fatalf("ValidateInstances error = %q, want unknown provider message", err.Error())
	}
}

func TestNilRegistryBehaviors(t *testing.T) {
	var registry *Registry
	if err := registry.Register(testRegistration()); err == nil {
		t.Fatal("nil Register error = nil")
	}
	if _, ok := registry.Get("fake"); ok {
		t.Fatal("nil Get ok = true")
	}
	if types := registry.Types(); types != nil {
		t.Fatalf("nil Types = %v, want nil", types)
	}
}

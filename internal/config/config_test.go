package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func TestLoadFile_YAML_HappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers:
  - name: local
    type: docker
    config:
      host: unix:///var/run/docker.sock
pools: []
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "local" {
		t.Fatalf("provider name = %q, want %q", cfg.Providers[0].Name, "local")
	}
	if cfg.Providers[0].Type != "docker" {
		t.Fatalf("provider type = %q, want %q", cfg.Providers[0].Type, "docker")
	}
}

func TestLoadFile_YAML_AcceptsPoolsBlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers: []
pools:
  - name: kali-attackers
    type: container
    provider: docker-local
    config:
      image: kalilinux/kali-rolling
    policy:
      preheat:
        min_ready: 3
        max_total: 8
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0", len(cfg.Providers))
	}
	if len(cfg.Pools) != 1 {
		t.Fatalf("pools len = %d, want 1", len(cfg.Pools))
	}
}

func TestLoadFile_JSON_HappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{
  "providers": [
    {
      "name": "local",
      "type": "docker",
      "config": {
        "host": "unix:///var/run/docker.sock"
      }
    }
  ],
  "pools": []
}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.Providers))
	}
}

func TestLoadFile_YAML_UnknownTopLevelFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
extra: 1
providers: []
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestLoadFile_JSON_UnknownTopLevelFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{"extra":1,"providers":[]}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestLoadFile_YAML_UnknownProviderFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers:
  - name: local
    type: docker
    bogus: 1
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestServerSpec_UIEnabled(t *testing.T) {
	t.Parallel()

	t.Run("nil_defaults_true", func(t *testing.T) {
		s := ServerSpec{}
		if !s.UIEnabled() {
			t.Fatal("UIEnabled() = false, want true (nil default)")
		}
	})

	t.Run("explicit_true", func(t *testing.T) {
		v := true
		s := ServerSpec{UI: &v}
		if !s.UIEnabled() {
			t.Fatal("UIEnabled() = false, want true")
		}
	})

	t.Run("explicit_false", func(t *testing.T) {
		v := false
		s := ServerSpec{UI: &v}
		if s.UIEnabled() {
			t.Fatal("UIEnabled() = true, want false")
		}
	})
}

func TestLoadFile_JSON_UnknownProviderFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{
  "providers": [
    {
      "name": "local",
      "type": "docker",
      "bogus": 1
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestResolvePoolExpectedType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    model.ResourceType
		wantErr string
	}{
		{name: "empty defaults to container", input: "", want: model.ResourceTypeContainer},
		{name: "container", input: "container", want: model.ResourceTypeContainer},
		{name: "docker", input: "docker", want: model.ResourceTypeContainer},
		{name: "vm", input: "vm", want: model.ResourceTypeVM},
		{name: "share", input: "share", want: model.ResourceTypeShare},
		{name: "invalid", input: "badtype", want: model.ResourceTypeUnknown, wantErr: `unsupported pool type "badtype"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolvePoolExpectedType(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolvePoolExpectedType(%q) error = nil, want %q", tt.input, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("ResolvePoolExpectedType(%q) error = %q, want %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePoolExpectedType(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ResolvePoolExpectedType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigValidate_invalid_pool_type(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Pools: []PoolSpec{
			{Name: "test", Type: "badtype"},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid pool type")
	}
	if got, want := err.Error(), `pool "test" type invalid: unsupported pool type "badtype"`; got != want {
		t.Fatalf("Validate() error = %q, want %q", got, want)
	}
}

func TestConfigValidate_valid_pool_type_aliases(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Pools: []PoolSpec{
			{Name: "default-empty"},
			{Name: "container", Type: "container"},
			{Name: "docker", Type: "docker"},
			{Name: "vm", Type: "vm"},
			{Name: "share", Type: "share"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
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

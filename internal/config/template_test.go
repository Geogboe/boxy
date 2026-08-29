package config

import (
	"testing"
)

func TestConfigLoadsTemplatesAndSandboxPackages(t *testing.T) {
	cfg, err := decodeYAML([]byte(`
templates:
  windows-base:
    type: vm
    provider: hyperv-local
    source: windows-2022
  windows-apps:
    extends: windows-base
    packages: [app1@1.0.0]
pools:
  - name: apps
    template: windows-apps
    policy:
      preheat:
        min_ready: 2
`))
	if err != nil {
		t.Fatalf("decodeYAML: %v", err)
	}
	if cfg.Pools[0].Template != "windows-apps" {
		t.Fatalf("pool template = %q, want windows-apps", cfg.Pools[0].Template)
	}
	resolved, err := cfg.ResolveTemplate("windows-apps")
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	if resolved.Type != "vm" || resolved.Source != "windows-2022" || len(resolved.Packages) != 1 {
		t.Fatalf("resolved template = %#v, want inherited shape and package", resolved)
	}
}

func TestConfigRejectsTemplateCycles(t *testing.T) {
	cfg := Config{Templates: map[string]TemplateSpec{
		"a": {Extends: "b"},
		"b": {Extends: "a"},
	}}
	if _, err := cfg.ResolveTemplate("a"); err == nil {
		t.Fatal("cyclic templates resolved successfully")
	}
}

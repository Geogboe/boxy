package config

import (
	"context"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/artifact"
	"github.com/Geogboe/boxy/pkg/resourcepack"
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

func TestPackageRegistryCompilesBuiltinPackageManager(t *testing.T) {
	t.Parallel()

	cfg := Config{Packages: map[string]resourcepack.Manifest{
		"developer-tools": {
			Version: "1.0.0",
			Builtin: resourcepack.BuiltinPackageManager,
			Scopes:  []resourcepack.Scope{resourcepack.ScopeResource},
			Events:  []resourcepack.Event{resourcepack.EventProvision},
			Inputs: map[string]any{"parameters": map[string]any{
				"manager": "apk", "packages": []any{"git", "curl"},
			}},
		},
	}}
	registry, err := cfg.PackageRegistry(context.Background())
	if err != nil {
		t.Fatalf("PackageRegistry: %v", err)
	}
	value, err := registry.Resolve(context.Background(), artifact.Ref{
		Type: artifact.ArtifactTypePackage, Name: "developer-tools", Version: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(value.Manifest)
	if !strings.Contains(manifest, "method: shell") || !strings.Contains(manifest, "apk add") {
		t.Fatalf("registry manifest was not compiled:\n%s", manifest)
	}
	if strings.Contains(manifest, "builtin:") || strings.Contains(manifest, "manager:") {
		t.Fatalf("registry manifest retained recipe fields:\n%s", manifest)
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

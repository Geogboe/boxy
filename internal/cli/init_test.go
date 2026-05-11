package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
}

func TestRunInit_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := runInit(false); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "boxy.yaml"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("boxy.yaml is empty")
	}
}

func TestRunInit_WritesComprehensiveStarterTemplate(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := runInit(false); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "boxy.yaml"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"providers:",
		"agents: []",
		"type: docker",
		"type: vm",
		"guest_password_ref:",
		"--log-level debug --log-file ./boxy.log",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("starter config missing %q\n%s", want, got)
		}
	}
}

func TestRunInit_GeneratedConfigLoadsAndValidates(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := runInit(false); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	cfg, err := boxyconfig.LoadFile(filepath.Join(dir, "boxy.yaml"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		t.Fatalf("RegisterBuiltins() error: %v", err)
	}
	if err := reg.ValidateInstances(context.Background(), cfg.Providers); err != nil {
		t.Fatalf("ValidateInstances() error: %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("len(cfg.Providers) = %d, want 1", len(cfg.Providers))
	}
	if len(cfg.Pools) != 1 {
		t.Fatalf("len(cfg.Pools) = %d, want 1", len(cfg.Pools))
	}
	if got := cfg.Pools[0].Name; got != "alpine-dev" {
		t.Fatalf("cfg.Pools[0].Name = %q, want alpine-dev", got)
	}
}

func TestRunInit_ErrorIfExists(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "boxy.yaml"), []byte("existing"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := runInit(false)
	if err == nil {
		t.Fatal("expected error when boxy.yaml exists")
	}
}

func TestRunInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "boxy.yaml"), []byte("old"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := runInit(true); err != nil {
		t.Fatalf("runInit(force=true) error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "boxy.yaml"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) == "old" {
		t.Fatal("file was not overwritten")
	}
}

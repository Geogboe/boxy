package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidate_valid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(cfgPath, []byte("pools: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"config", "validate", "--config", cfgPath})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestConfigValidate_bad_config(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(cfgPath, []byte("not_a_valid_field: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"config", "validate", "--config", cfgPath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for bad config")
	}
}

func TestConfigValidate_missing_file(t *testing.T) {
	t.Parallel()
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"config", "validate", "--config", "/nonexistent/boxy.yaml"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

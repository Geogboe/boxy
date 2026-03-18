package cli

import (
	"os"
	"path/filepath"
	"testing"
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

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunInit_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

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
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "boxy.yaml"), []byte("existing"), 0644)

	err := runInit(false)
	if err == nil {
		t.Fatal("expected error when boxy.yaml exists")
	}
}

func TestRunInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	os.WriteFile(filepath.Join(dir, "boxy.yaml"), []byte("old"), 0644)

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

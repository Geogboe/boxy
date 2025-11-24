package workspacefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProvisionCreatesLayout(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res1")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}

	expectExists(t, paths.RootDir)
	expectExists(t, paths.WorkspaceDir)
}

func TestAllocateWritesArtifacts(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res2")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	connectPath := paths.ConnectScript
	if err := os.WriteFile(connectPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write connect failed: %v", err)
	}
	expectExists(t, connectPath)
}

func TestHealthCheck(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res3")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if err := HealthCheck(paths, nil, 0); err != nil {
		t.Fatalf("healthcheck failed: %v", err)
	}
}

func TestHealthCheckMissingRequiredFile(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res-missing")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	missing := filepath.Join(paths.RootDir, "missing.txt")
	err = HealthCheck(paths, []string{missing}, 0)
	if err == nil {
		t.Fatalf("expected healthcheck to fail for missing required file")
	}
}

func TestHealthCheckFreeSpaceThreshold(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res-free")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	s := newStatfs()
	if err := s.Statfs(paths.RootDir); err != nil {
		t.Fatalf("statfs failed: %v", err)
	}
	limit := s.FreeBytes() + 1
	err = HealthCheck(paths, nil, limit)
	if err == nil {
		t.Fatalf("expected healthcheck to fail when free space below threshold")
	}
}

func TestCleanup(t *testing.T) {
	base := t.TempDir()
	paths, err := Provision(base, "res4")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if err := Cleanup(paths); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if _, err := os.Stat(paths.RootDir); err == nil {
		t.Fatalf("expected root dir removed")
	}
}

func expectExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

// Ensure statfs works on all platforms in a minimal way.
func TestStatfsAvailable(t *testing.T) {
	base := t.TempDir()
	if runtime.GOOS == "windows" {
		// Create a child to ensure the path exists.
		_ = os.MkdirAll(filepath.Join(base, "child"), 0o755)
	}
	s := newStatfs()
	if err := s.Statfs(base); err != nil {
		t.Fatalf("statfs failed: %v", err)
	}
	if s.FreeBytes() == 0 {
		t.Fatalf("expected FreeBytes > 0")
	}
}

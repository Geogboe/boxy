package devfactory

import (
	"path/filepath"
	"testing"
)

func TestConfig_ResolveRelativePaths_JoinsRelativeDataDirOntoBaseDir(t *testing.T) {
	c := &Config{DataDir: filepath.Join(".boxy", "devfactory")}
	c.ResolveRelativePaths(filepath.Join("some", "config", "dir"))

	want := filepath.Join("some", "config", "dir", ".boxy", "devfactory")
	if c.DataDir != want {
		t.Fatalf("DataDir = %q, want %q", c.DataDir, want)
	}
}

func TestConfig_ResolveRelativePaths_LeavesAbsoluteDataDirUntouched(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("abs", "data"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	c := &Config{DataDir: abs}
	c.ResolveRelativePaths(filepath.Join("some", "config", "dir"))

	if c.DataDir != abs {
		t.Fatalf("DataDir = %q, want unchanged %q", c.DataDir, abs)
	}
}

func TestConfig_ResolveRelativePaths_LeavesEmptyDataDirUntouched(t *testing.T) {
	c := &Config{}
	c.ResolveRelativePaths(filepath.Join("some", "config", "dir"))

	if c.DataDir != "" {
		t.Fatalf("DataDir = %q, want empty", c.DataDir)
	}
}

func TestConfig_ResolveRelativePaths_LeavesDataDirUntouchedWhenBaseDirEmpty(t *testing.T) {
	c := &Config{DataDir: filepath.Join(".boxy", "devfactory")}
	c.ResolveRelativePaths("")

	want := filepath.Join(".boxy", "devfactory")
	if c.DataDir != want {
		t.Fatalf("DataDir = %q, want unchanged %q", c.DataDir, want)
	}
}

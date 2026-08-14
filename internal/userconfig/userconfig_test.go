package userconfig

import (
	"path/filepath"
	"testing"
)

func TestDirUsesXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(xdg, "boxy"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

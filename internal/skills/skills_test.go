package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetFSContainsBundledSkill(t *testing.T) {
	data, err := fs.ReadFile(AssetFS(), "assets/boxy-cli/SKILL.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "boxy help all") {
		t.Fatalf("SKILL.md missing expected help guidance")
	}
}

func TestInstallCanonicalCopiesSkillAndVersion(t *testing.T) {
	home := setTestHome(t)
	xdg := filepath.Join(home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	canonicalPath, err := InstallCanonical(false, "v1.2.3")
	if err != nil {
		t.Fatalf("InstallCanonical: %v", err)
	}
	if want := filepath.Join(xdg, "boxy", "skills", SkillName); canonicalPath != want {
		t.Fatalf("canonical path = %q, want %q", canonicalPath, want)
	}
	if _, err := os.Stat(filepath.Join(canonicalPath, "SKILL.md")); err != nil {
		t.Fatalf("Stat SKILL.md: %v", err)
	}
	gotVersion, err := os.ReadFile(filepath.Join(canonicalPath, VersionFileName))
	if err != nil {
		t.Fatalf("ReadFile version: %v", err)
	}
	if strings.TrimSpace(string(gotVersion)) != "v1.2.3" {
		t.Fatalf("version file = %q, want v1.2.3", strings.TrimSpace(string(gotVersion)))
	}
}

func TestLinkAtAndRemoveLinkAt(t *testing.T) {
	home := setTestHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	canonicalPath, err := InstallCanonical(false, "v1.2.3")
	if err != nil {
		t.Fatalf("InstallCanonical: %v", err)
	}
	targetParent := filepath.Join(t.TempDir(), "targets")

	targetPath, copyFallback, err := LinkAt(canonicalPath, targetParent, false)
	if err != nil {
		t.Fatalf("LinkAt: %v", err)
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("Lstat target: %v", err)
	}
	if runtime.GOOS != "windows" && copyFallback {
		t.Fatal("unexpected copy fallback on non-Windows platform")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(targetPath)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if !samePath(resolved, canonicalPath) {
			t.Fatalf("resolved path = %q, want %q", resolved, canonicalPath)
		}
	} else {
		marker, err := os.ReadFile(filepath.Join(targetPath, SourceFileName))
		if err != nil {
			t.Fatalf("ReadFile source marker: %v", err)
		}
		if !samePath(strings.TrimSpace(string(marker)), canonicalPath) {
			t.Fatalf("source marker = %q, want %q", strings.TrimSpace(string(marker)), canonicalPath)
		}
	}

	removed, err := RemoveLinkAt(canonicalPath, targetParent)
	if err != nil {
		t.Fatalf("RemoveLinkAt: %v", err)
	}
	if !removed {
		t.Fatal("expected managed target to be removed")
	}
	if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("target still exists after removal: %v", err)
	}
}

func TestRemoveLinkAtLeavesUnmanagedDirectory(t *testing.T) {
	home := setTestHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	canonicalPath, err := InstallCanonical(false, "v1.2.3")
	if err != nil {
		t.Fatalf("InstallCanonical: %v", err)
	}
	targetParent := t.TempDir()
	unmanagedPath := filepath.Join(targetParent, SkillName)
	if err := os.MkdirAll(unmanagedPath, 0o755); err != nil {
		t.Fatalf("MkdirAll unmanaged: %v", err)
	}

	removed, err := RemoveLinkAt(canonicalPath, targetParent)
	if err != nil {
		t.Fatalf("RemoveLinkAt: %v", err)
	}
	if removed {
		t.Fatal("expected unmanaged directory to be left in place")
	}
	if _, err := os.Stat(unmanagedPath); err != nil {
		t.Fatalf("Stat unmanaged path: %v", err)
	}
}

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	boxyskills "github.com/Geogboe/boxy/internal/skills"
)

func TestSkillsInstall_DefaultsToUserTarget(t *testing.T) {
	home, cwd := prepareSkillsEnv(t)
	_ = home
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"skills", "install"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	canonical := filepath.Join(home, ".config-home", "boxy", "skills", boxyskills.SkillName)
	if _, err := os.Stat(filepath.Join(canonical, "SKILL.md")); err != nil {
		t.Fatalf("canonical skill missing: %v", err)
	}
	userTarget := filepath.Join(home, ".agents", "skills", boxyskills.SkillName)
	assertManagedSkillTarget(t, canonical, userTarget)
	if !strings.Contains(stdout.String(), "Canonical:") {
		t.Fatalf("expected canonical output, got %q", stdout.String())
	}
	_ = cwd
	_ = stderr
}

func TestSkillsInstall_ProjectAndPathTargetsAreAdditive(t *testing.T) {
	home, cwd := prepareSkillsEnv(t)
	_ = home
	extraA := filepath.Join(t.TempDir(), "extraA")
	extraB := filepath.Join(t.TempDir(), "extraB")

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "install", "--project", "--path", extraA, "--path", extraB})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	canonical := filepath.Join(home, ".config-home", "boxy", "skills", boxyskills.SkillName)
	assertManagedSkillTarget(t, canonical, filepath.Join(cwd, ".agents", "skills", boxyskills.SkillName))
	assertManagedSkillTarget(t, canonical, filepath.Join(extraA, boxyskills.SkillName))
	assertManagedSkillTarget(t, canonical, filepath.Join(extraB, boxyskills.SkillName))
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", boxyskills.SkillName)); !os.IsNotExist(err) {
		t.Fatalf("user target should not be installed when explicit targets are provided")
	}
}

func TestSkillsInstall_IsNoopWhenManagedTargetAlreadyExists(t *testing.T) {
	prepareSkillsEnv(t)
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "install"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	cmd = NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "install"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
}

func TestSkillsInstall_ErrorsForConflictingExistingTargetWithoutForce(t *testing.T) {
	_, cwd := prepareSkillsEnv(t)
	conflictParent := filepath.Join(cwd, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(conflictParent, boxyskills.SkillName), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "install", "--project"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSkillsUninstall_RemovesManagedTargetAndPurgesCanonical(t *testing.T) {
	home, _ := prepareSkillsEnv(t)
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "install"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install Execute: %v", err)
	}

	canonical := filepath.Join(home, ".config-home", "boxy", "skills", boxyskills.SkillName)
	target := filepath.Join(home, ".agents", "skills", boxyskills.SkillName)

	cmd = NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "uninstall", "--purge"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall Execute: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists: %v", err)
	}
	if _, err := os.Stat(canonical); !os.IsNotExist(err) {
		t.Fatalf("canonical still exists: %v", err)
	}
}

func TestSkillsUninstall_LeavesUnmanagedDirectory(t *testing.T) {
	_, cwd := prepareSkillsEnv(t)
	unmanagedParent := filepath.Join(cwd, "custom")
	unmanagedTarget := filepath.Join(unmanagedParent, boxyskills.SkillName)
	if err := os.MkdirAll(unmanagedTarget, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"skills", "uninstall", "--path", unmanagedParent})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(unmanagedTarget); err != nil {
		t.Fatalf("unmanaged target should remain: %v", err)
	}
}

func TestHelpAllPrintsNewCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stdout)
	cmd.SetArgs([]string{"help", "all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"boxy skills install", "boxy skills uninstall", "boxy help all"} {
		if !strings.Contains(output, want) {
			t.Fatalf("help all output missing %q", want)
		}
	}
}

func prepareSkillsEnv(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config-home"))
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})
	return home, cwd
}

func assertManagedSkillTarget(t *testing.T, canonical, target string) {
	t.Helper()
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat target %q: %v", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("EvalSymlinks %q: %v", target, err)
		}
		if filepath.Clean(resolved) != filepath.Clean(canonical) {
			t.Fatalf("resolved target = %q, want %q", resolved, canonical)
		}
		return
	}
	marker, err := os.ReadFile(filepath.Join(target, boxyskills.SourceFileName))
	if err != nil {
		t.Fatalf("ReadFile source marker: %v", err)
	}
	if strings.TrimSpace(string(marker)) != canonical {
		t.Fatalf("source marker = %q, want %q", strings.TrimSpace(string(marker)), canonical)
	}
	if runtime.GOOS != "windows" {
		t.Fatal("managed copy fallback should only happen on Windows")
	}
}

// internal/cli/service_protected_dir_test.go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// withProtectedServiceRoot redirects protectedServiceRootFn to a fresh
// t.TempDir() for the duration of one test, restoring the previous value
// (whatever TestMain installed) afterward.
func withProtectedServiceRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := protectedServiceRootFn
	protectedServiceRootFn = func() string { return dir }
	t.Cleanup(func() { protectedServiceRootFn = orig })
	return dir
}

// withProtectedServiceGOOS overrides protectedServiceInstallGOOS so
// usesProtectedServiceDir's windows-only branch is testable regardless of
// the host actually running the test.
func withProtectedServiceGOOS(t *testing.T, goos string) {
	t.Helper()
	orig := protectedServiceInstallGOOS
	protectedServiceInstallGOOS = goos
	t.Cleanup(func() { protectedServiceInstallGOOS = orig })
}

func TestUsesProtectedServiceDir(t *testing.T) {
	cases := []struct {
		name     string
		goos     string
		userMode bool
		want     bool
	}{
		{"windows real install", "windows", false, true},
		{"windows --user install", "windows", true, false},
		{"linux real install", "linux", false, false},
		{"darwin real install", "darwin", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withProtectedServiceGOOS(t, c.goos)
			if got := usesProtectedServiceDir(c.userMode); got != c.want {
				t.Errorf("usesProtectedServiceDir(%v) on %s = %v, want %v", c.userMode, c.goos, got, c.want)
			}
		})
	}
}

func TestProtectedServiceDir_IsScopedPerServiceName(t *testing.T) {
	root := withProtectedServiceRoot(t)

	agentDir := protectedServiceDir("boxy-agent")
	serveDir := protectedServiceDir("boxy-serve")
	namedDir := protectedServiceDir("boxy-agent-test1")

	if agentDir == serveDir || agentDir == namedDir || serveDir == namedDir {
		t.Fatalf("expected distinct directories per service name, got agent=%q serve=%q named=%q", agentDir, serveDir, namedDir)
	}
	if filepath.Dir(agentDir) != root {
		t.Fatalf("protectedServiceDir(%q) = %q, want a child of root %q", "boxy-agent", agentDir, root)
	}
}

func TestStageProtectedServiceBinary_CopiesExecutable(t *testing.T) {
	withProtectedServiceRoot(t)
	src := filepath.Join(t.TempDir(), "boxy.exe")
	if err := os.WriteFile(src, []byte("exe-bytes"), 0o755); err != nil {
		t.Fatalf("write fixture exe: %v", err)
	}

	dst, err := stageProtectedServiceBinary("boxy-agent", src)
	if err != nil {
		t.Fatalf("stageProtectedServiceBinary: %v", err)
	}
	if filepath.Base(dst) != "boxy.exe" {
		t.Fatalf("staged path = %q, want a boxy.exe basename", dst)
	}
	if filepath.Dir(dst) != protectedServiceDir("boxy-agent") {
		t.Fatalf("staged path %q is not inside protectedServiceDir(%q) = %q", dst, "boxy-agent", protectedServiceDir("boxy-agent"))
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read staged binary: %v", err)
	}
	if string(content) != "exe-bytes" {
		t.Fatalf("staged content = %q, want %q", content, "exe-bytes")
	}
}

func TestStageProtectedServiceBinary_CopiesWintunWhenPresentBesideSource(t *testing.T) {
	withProtectedServiceRoot(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "boxy.exe")
	if err := os.WriteFile(src, []byte("exe-bytes"), 0o755); err != nil {
		t.Fatalf("write fixture exe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, wintunDLLName), []byte("dll-bytes"), 0o644); err != nil {
		t.Fatalf("write fixture wintun.dll: %v", err)
	}

	dst, err := stageProtectedServiceBinary("boxy-agent", src)
	if err != nil {
		t.Fatalf("stageProtectedServiceBinary: %v", err)
	}
	stagedWintun := filepath.Join(filepath.Dir(dst), wintunDLLName)
	content, err := os.ReadFile(stagedWintun)
	if err != nil {
		t.Fatalf("expected wintun.dll staged at %s: %v", stagedWintun, err)
	}
	if string(content) != "dll-bytes" {
		t.Fatalf("staged wintun.dll content = %q, want %q", content, "dll-bytes")
	}
}

func TestStageProtectedServiceBinary_SkipsWintunWhenAbsentBesideSource(t *testing.T) {
	withProtectedServiceRoot(t)
	src := filepath.Join(t.TempDir(), "boxy.exe")
	if err := os.WriteFile(src, []byte("exe-bytes"), 0o755); err != nil {
		t.Fatalf("write fixture exe: %v", err)
	}

	dst, err := stageProtectedServiceBinary("boxy-agent", src)
	if err != nil {
		t.Fatalf("stageProtectedServiceBinary: %v", err)
	}
	stagedWintun := filepath.Join(filepath.Dir(dst), wintunDLLName)
	if _, err := os.Stat(stagedWintun); !os.IsNotExist(err) {
		t.Fatalf("expected no wintun.dll staged when the source had none, stat err = %v", err)
	}
}

func TestStageProtectedServiceBinary_RefreshOverwritesAnExistingCopy(t *testing.T) {
	withProtectedServiceRoot(t)
	src := filepath.Join(t.TempDir(), "boxy.exe")
	if err := os.WriteFile(src, []byte("v1"), 0o755); err != nil {
		t.Fatalf("write fixture exe v1: %v", err)
	}
	if _, err := stageProtectedServiceBinary("boxy-agent", src); err != nil {
		t.Fatalf("first stage: %v", err)
	}

	// Simulate `boxy update` replacing the source binary in place.
	if err := os.WriteFile(src, []byte("v2-updated"), 0o755); err != nil {
		t.Fatalf("write fixture exe v2: %v", err)
	}
	dst, err := stageProtectedServiceBinary("boxy-agent", src)
	if err != nil {
		t.Fatalf("refresh stage: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read refreshed binary: %v", err)
	}
	if string(content) != "v2-updated" {
		t.Fatalf("staged content after refresh = %q, want %q", content, "v2-updated")
	}
}

func TestStageProtectedServiceBinary_NoopWhenSourceAlreadyAtDestination(t *testing.T) {
	dir := withProtectedServiceRoot(t)
	// Pre-place a file exactly at the path stageProtectedServiceBinary
	// would compute as the destination, and pass that same path in as the
	// source -- an operator who already runs boxy.exe from inside a
	// previously-staged protected directory (e.g. a repeat install using
	// that copy directly) must not have it truncated by copying it onto
	// itself.
	svcDir := filepath.Join(dir, "boxy-agent")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	samePath := filepath.Join(svcDir, "boxy.exe")
	if err := os.WriteFile(samePath, []byte("already-here"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	dst, err := stageProtectedServiceBinary("boxy-agent", samePath)
	if err != nil {
		t.Fatalf("stageProtectedServiceBinary: %v", err)
	}
	if dst != samePath {
		t.Fatalf("dst = %q, want %q unchanged", dst, samePath)
	}
	content, err := os.ReadFile(samePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "already-here" {
		t.Fatalf("content = %q, want it untouched (\"already-here\")", content)
	}
}

func TestStageProtectedServiceBinary_MissingSourceFailsClearly(t *testing.T) {
	withProtectedServiceRoot(t)
	_, err := stageProtectedServiceBinary("boxy-agent", filepath.Join(t.TempDir(), "does-not-exist.exe"))
	if err == nil {
		t.Fatal("expected an error for a missing source binary")
	}
}

func TestRemoveProtectedServiceDir_RemovesStagedFiles(t *testing.T) {
	withProtectedServiceRoot(t)
	src := filepath.Join(t.TempDir(), "boxy.exe")
	if err := os.WriteFile(src, []byte("exe-bytes"), 0o755); err != nil {
		t.Fatalf("write fixture exe: %v", err)
	}
	dst, err := stageProtectedServiceBinary("boxy-agent", src)
	if err != nil {
		t.Fatalf("stageProtectedServiceBinary: %v", err)
	}

	if err := removeProtectedServiceDir("boxy-agent"); err != nil {
		t.Fatalf("removeProtectedServiceDir: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected staged binary removed, stat err = %v", err)
	}
	if _, err := os.Stat(protectedServiceDir("boxy-agent")); !os.IsNotExist(err) {
		t.Fatalf("expected the service directory itself removed, stat err = %v", err)
	}
}

func TestRemoveProtectedServiceDir_NoopWhenNeverStaged(t *testing.T) {
	withProtectedServiceRoot(t)
	if err := removeProtectedServiceDir("boxy-agent-never-installed"); err != nil {
		t.Fatalf("removeProtectedServiceDir on a directory that was never created: %v", err)
	}
}

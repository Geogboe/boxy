package resourcepack

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func packageManagerManifest(manager string, packages ...any) Manifest {
	return Manifest{
		Name:    "developer-tools",
		Version: "1.0.0",
		Builtin: BuiltinPackageManager,
		Scopes:  []Scope{ScopeResource},
		Events:  []Event{EventProvision},
		Inputs: map[string]any{
			"parameters": map[string]any{
				"manager":  manager,
				"packages": packages,
			},
		},
	}
}

func TestCompilePackageManagerPackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		manager  string
		packages []any
		method   Method
		want     []string
	}{
		{manager: "apt", packages: []any{"git", "curl"}, method: MethodShell, want: []string{"apt-get", "curl", "git"}},
		{manager: "apk", packages: []any{"git", "curl"}, method: MethodShell, want: []string{"apk", "curl", "git"}},
		{manager: "winget", packages: []any{"Microsoft.VisualStudioCode", "Git.Git"}, method: MethodPowerShell, want: []string{"winget install --id $package --exact --silent --accept-source-agreements --accept-package-agreements", "Git.Git", "Microsoft.VisualStudioCode"}},
		{manager: "CHOCOLATEY", packages: []any{"git", "curl"}, method: MethodPowerShell, want: []string{"choco install $package --yes --no-progress", "curl", "git"}},
	}
	for _, tt := range tests {
		t.Run(tt.manager, func(t *testing.T) {
			compiled, err := Compile(packageManagerManifest(tt.manager, tt.packages...))
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if compiled.Builtin != "" {
				t.Fatalf("compiled builtin = %q, want empty", compiled.Builtin)
			}
			if compiled.Method != tt.method {
				t.Fatalf("compiled method = %q, want %q", compiled.Method, tt.method)
			}
			inline, ok := compiled.Inputs["inline"].(string)
			if !ok || inline == "" {
				t.Fatalf("compiled inline input = %#v, want non-empty string", compiled.Inputs["inline"])
			}
			for _, want := range tt.want {
				if !strings.Contains(inline, want) {
					t.Errorf("compiled %s script does not contain %q:\n%s", tt.manager, want, inline)
				}
			}
			if err := compiled.Validate(); err != nil {
				t.Fatalf("compiled manifest Validate: %v", err)
			}
		})
	}
}

func TestCompilePackageManagerPackagesAreDeterministic(t *testing.T) {
	t.Parallel()

	first, err := Compile(packageManagerManifest("apk", "git", "curl"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(packageManagerManifest("apk", "curl", "git"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent package declarations compiled differently:\nfirst: %#v\nsecond: %#v", first, second)
	}
}

func TestCompilePackageManagerAcceptsYAMLInputShape(t *testing.T) {
	t.Parallel()

	var manifest Manifest
	if err := yaml.Unmarshal([]byte(`
name: developer-tools
version: 1.0.0
builtin: package-manager
scopes: [resource]
events: [provision]
inputs:
  parameters:
    manager: apk
    packages: [curl, git]
`), &manifest); err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(manifest)
	if err != nil {
		t.Fatalf("Compile YAML manifest: %v", err)
	}
	if compiled.Method != MethodShell {
		t.Fatalf("compiled method = %q, want shell", compiled.Method)
	}
}

func TestCompileChocolateyRefreshesPowerShellPathBeforeLookup(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(packageManagerManifest("chocolatey", "git"))
	if err != nil {
		t.Fatal(err)
	}
	inline := compiled.Inputs["inline"].(string)
	for _, want := range []string{
		"[Environment]::GetEnvironmentVariable('Path', 'Machine')",
		"[Environment]::GetEnvironmentVariable('Path', 'User')",
		"$env:Path =",
	} {
		if !strings.Contains(inline, want) {
			t.Errorf("Chocolatey script missing PATH refresh %q:\n%s", want, inline)
		}
	}
	if pathRefresh, lookup := strings.Index(inline, "$env:Path ="), strings.Index(inline, "Get-Command -Name 'choco'"); pathRefresh < 0 || lookup < 0 || pathRefresh > lookup {
		t.Fatalf("Chocolatey PATH refresh must precede choco lookup:\n%s", inline)
	}
}

func TestCompilePackageManagerRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		modify func(*Manifest)
	}{
		{name: "unsupported builtin", modify: func(m *Manifest) { m.Builtin = "package-manager-v2" }},
		{name: "unsupported manager", modify: func(m *Manifest) { m.Inputs["parameters"].(map[string]any)["manager"] = "brew" }},
		{name: "missing manager", modify: func(m *Manifest) { delete(m.Inputs["parameters"].(map[string]any), "manager") }},
		{name: "missing packages", modify: func(m *Manifest) { delete(m.Inputs["parameters"].(map[string]any), "packages") }},
		{name: "empty package", modify: func(m *Manifest) { m.Inputs["parameters"].(map[string]any)["packages"] = []any{""} }},
		{name: "duplicate package", modify: func(m *Manifest) { m.Inputs["parameters"].(map[string]any)["packages"] = []any{"git", "git"} }},
		{name: "unsafe package", modify: func(m *Manifest) {
			m.Inputs["parameters"].(map[string]any)["packages"] = []any{"git; touch /tmp/pwned"}
		}},
		{name: "unsupported option", modify: func(m *Manifest) { m.Inputs["parameters"].(map[string]any)["upgrade"] = true }},
		{name: "inline input", modify: func(m *Manifest) { m.Inputs["inline"] = "echo unsafe" }},
		{name: "explicit method", modify: func(m *Manifest) { m.Method = MethodShell }},
		{name: "defaults", modify: func(m *Manifest) { m.Defaults = map[string]any{"channel": "stable"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := packageManagerManifest("apk", "git")
			tt.modify(&manifest)
			if _, err := Compile(manifest); err == nil {
				t.Fatal("Compile succeeded, want validation error")
			}
		})
	}
}

func TestCompilePackageManagerRejectsUnsafeIdentifiers(t *testing.T) {
	t.Parallel()

	unsafe := []string{"git\nwhoami", "git && whoami", "git|whoami", "$(whoami)", "$(Get-Process)", "git'", "git`whoami`", "git*", "-o"}
	for _, packageID := range unsafe {
		t.Run(packageID, func(t *testing.T) {
			if _, err := Compile(packageManagerManifest("apt", packageID)); err == nil {
				t.Fatalf("Compile accepted unsafe package ID %q", packageID)
			}
		})
	}
}

func TestPackageManagerShellScriptFailsWhenManagerIsMissing(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(packageManagerManifest("apk", "curl"))
	if err != nil {
		t.Fatal(err)
	}
	inline := compiled.Inputs["inline"].(string)
	pathDir := t.TempDir()
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("shell unavailable: %v", err)
	}
	path := filepath.Join(pathDir, "empty-path")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shPath, "-c", inline)
	cmd.Env = []string{"PATH=" + pathDir}
	if err := cmd.Run(); err == nil {
		t.Fatal("generated script succeeded without its package manager")
	}
}

func TestPackageManagerShellScriptPropagatesManagerFailure(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(packageManagerManifest("apk", "curl"))
	if err != nil {
		t.Fatal(err)
	}
	managerPath := filepath.Join(t.TempDir(), "apk")
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("shell unavailable: %v", err)
	}
	if err := os.WriteFile(managerPath, []byte("#!/bin/sh\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shPath, "-c", compiled.Inputs["inline"].(string))
	cmd.Env = []string{"PATH=" + filepath.Dir(managerPath)}
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("generated script error = %v, want exit code 23", err)
	}
}

func TestPackageManagerPowerShellScriptFailsWhenManagerIsMissing(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(packageManagerManifest("winget", "Git.Git"))
	if err != nil {
		t.Fatal(err)
	}
	powershell, err := exec.LookPath("pwsh")
	if err != nil {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skipf("PowerShell unavailable: %v", err)
	}
	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", compiled.Inputs["inline"].(string))
	cmd.Env = testEnvironmentWithPath(t.TempDir())
	if err := cmd.Run(); err == nil {
		t.Fatal("generated PowerShell script succeeded without its package manager")
	}
}

func TestPackageManagerPowerShellScriptPropagatesManagerFailure(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(packageManagerManifest("winget", "Git.Git"))
	if err != nil {
		t.Fatal(err)
	}
	powershell, err := exec.LookPath("pwsh")
	if err != nil {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skipf("PowerShell unavailable: %v", err)
	}
	pathDir := t.TempDir()
	managerPath := filepath.Join(pathDir, "winget")
	contents := []byte("#!/bin/sh\nexit 23\n")
	if runtime.GOOS == "windows" {
		managerPath += ".cmd"
		contents = []byte("@echo off\r\nexit /b 23\r\n")
	}
	if err := os.WriteFile(managerPath, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", compiled.Inputs["inline"].(string))
	cmd.Env = testEnvironmentWithPath(pathDir)
	if err := cmd.Run(); err == nil {
		t.Fatal("generated PowerShell script succeeded after package manager failure")
	}
}

func testEnvironmentWithPath(path string) []string {
	current := os.Environ()
	env := make([]string, 0, len(current)+1)
	for _, value := range current {
		if strings.HasPrefix(strings.ToUpper(value), "PATH=") {
			continue
		}
		env = append(env, value)
	}
	return append(env, "PATH="+path)
}

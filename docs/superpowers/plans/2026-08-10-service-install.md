# Service Install (`boxy agent service` / `boxy serve service`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `boxy agent serve` and `boxy serve` be installed as OS-managed background services (real Windows Service via SCM, Windows Task Scheduler fallback, Linux systemd system or user unit) that start automatically and survive logout/reboot, via new `boxy agent service` / `boxy serve service` command trees.

**Architecture:** A new `internal/svcmgr` package provides a platform-agnostic `Manager` interface (`Install`/`Uninstall`/`Start`/`Stop`/`Status`) with per-OS implementations behind Go build tags (Windows: SCM via `golang.org/x/sys/windows/svc/mgr`, and Task Scheduler via `schtasks.exe`; Linux: `systemd` unit files via `systemctl`/`loginctl`; Darwin: explicit unsupported error). `internal/cli` gets a small YAML "service config" file format (one per process type) that `install` writes with fully-resolved absolute paths, and that a new `--service-config` flag on `agent serve`/`serve` loads from at boot instead of requiring flags on the command line. A `RunAsWindowsService` helper detects SCM-launched execution and bridges it to the existing `runAgentServe`/`runServe` functions via context cancellation.

**Tech Stack:** Go 1.25, `golang.org/x/sys/windows` (`svc`, `svc/mgr` — promote from indirect to direct dependency), `gopkg.in/yaml.v3` (already a direct dependency), `os/exec` (`systemctl`, `loginctl`, `schtasks.exe`), `spf13/cobra`.

## Global Constraints

- Spec source of truth: `docs/superpowers/specs/2026-08-10-service-install-design.md`. Every task below implements one or more of its sections — cite the section in review.
- Hand-rolled per-OS only — no third-party service-manager abstraction library (e.g. `kardianos/service`). Only official `golang.org/x/sys` packages and shelling out to OS-native tools (`systemctl`, `loginctl`, `schtasks.exe`).
- macOS/Darwin: `service install` must fail with a clear, explicit error — never silently no-op.
- Single instance per host for v1: fixed names `boxy-agent` / `boxy-serve`; `install` errors if already installed.
- Default `install` behavior requires elevation (Administrator / root) for the real service; `--user` opts into the unprivileged fallback (Task Scheduler at-logon task / systemd user unit + `loginctl enable-linger`).
- All relative paths (`--data-dir`, `--config`, `--ca-cert`, log file) must be resolved to absolute paths at `install` time — a running service has no predictable working directory.
- The agent's bootstrap `token` is the only sensitive field in a service config file: DPAPI machine-scope encrypted on Windows, `0600` file permissions on Linux, and scrubbed to empty once the agent successfully registers. `serve`'s service config has no secret field.
- `uninstall` never deletes `--data-dir` unless `--purge` is passed.
- Match existing code conventions: package-level factory `var`s for test injection (see `updateNewUpdater` in `internal/cli/update.go`), sentinel errors, `fmt.Errorf("...: %w", err)` wrapping, `t.Helper()` in test helpers, table-driven tests where the codebase already uses them.
- Run `task test` (not raw `go test`) per project convention; `task lint` before considering any task done, matching `.golangci.yml`.

---

### Task 1: `internal/svcmgr` core types and sentinel errors

**Files:**
- Create: `internal/svcmgr/svcmgr.go`
- Test: `internal/svcmgr/svcmgr_test.go`

**Interfaces:**
- Produces (used by every later task in this plan):
  ```go
  package svcmgr

  type Spec struct {
      Name        string   // short identifier, e.g. "boxy-agent" — used as service/unit/task name
      DisplayName string   // human-readable name shown in service managers
      Description string
      ExecPath    string   // absolute path to the boxy executable
      Args        []string // arguments passed to ExecPath when the service starts
  }

  type Status struct {
      Installed bool
      Running   bool
      Mode      string // "system-service", "user-task", "system-unit", "user-unit", or "" when not installed
  }

  type ManagerOptions struct {
      UserMode bool // true = unprivileged fallback, false = privileged/system-level (default)
  }

  type Manager interface {
      Install(spec Spec) error
      Uninstall(name string) error
      Start(name string) error
      Stop(name string) error
      Status(name string) (Status, error)
  }

  var ErrAlreadyInstalled = errors.New("svcmgr: service already installed")
  var ErrNotInstalled = errors.New("svcmgr: service not installed")
  ```
  `NewManager(opts ManagerOptions) (Manager, error)` is declared here only as a doc comment — the actual implementation is provided per-platform in Tasks 2, 3, and 6 (Go requires exactly one definition per build; each platform file supplies its own `NewManager`).

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/svcmgr_test.go
package svcmgr

import (
	"errors"
	"testing"
)

func TestSentinelErrors_AreDistinct(t *testing.T) {
	if errors.Is(ErrAlreadyInstalled, ErrNotInstalled) {
		t.Fatal("ErrAlreadyInstalled and ErrNotInstalled must be distinct sentinel errors")
	}
}

func TestStatus_ZeroValueIsNotInstalled(t *testing.T) {
	var st Status
	if st.Installed || st.Running || st.Mode != "" {
		t.Fatalf("zero-value Status should report not-installed, got %+v", st)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/svcmgr/...`
Expected: FAIL — package `svcmgr` doesn't exist yet (no non-test file).

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/svcmgr.go

// Package svcmgr installs, uninstalls, starts, stops, and queries boxy
// processes (agent or server) as OS-managed background services: a real
// Windows Service via the Service Control Manager, a Windows Task
// Scheduler at-logon task as an unprivileged fallback, or a systemd
// system/user unit on Linux. macOS is not supported — NewManager returns
// an error rather than silently failing to install anything.
//
// See docs/superpowers/specs/2026-08-10-service-install-design.md.
package svcmgr

import "errors"

// Spec describes a service to install: the identifying name, display
// metadata, and the exact executable + arguments to run.
type Spec struct {
	// Name is the short identifier used as the service/unit/task name,
	// e.g. "boxy-agent" or "boxy-serve". Must be stable across
	// install/uninstall/start/stop/status calls for the same process.
	Name        string
	DisplayName string
	Description string
	// ExecPath must be an absolute path — the service has no predictable
	// working directory to resolve a relative path against.
	ExecPath string
	Args     []string
}

// Status reports whether a named service is installed, currently running,
// and which install mode it's using.
type Status struct {
	Installed bool
	Running   bool
	// Mode is one of "system-service", "user-task" (Windows),
	// "system-unit", "user-unit" (Linux), or "" when Installed is false.
	Mode string
}

// ManagerOptions selects which install mode a Manager targets.
type ManagerOptions struct {
	// UserMode selects the unprivileged fallback (Windows: Task
	// Scheduler at-logon task; Linux: systemd --user unit). false (the
	// default) selects the privileged, real service (Windows: SCM;
	// Linux: systemd system unit) and requires the caller already be
	// elevated.
	UserMode bool
}

// Manager installs, removes, starts, stops, and reports the status of a
// single OS-managed service. Implementations are platform-specific — see
// NewManager in the platform-specific files in this package.
type Manager interface {
	Install(spec Spec) error
	Uninstall(name string) error
	Start(name string) error
	Stop(name string) error
	Status(name string) (Status, error)
}

// ErrAlreadyInstalled is returned by Install when a service with the same
// name is already registered.
var ErrAlreadyInstalled = errors.New("svcmgr: service already installed")

// ErrNotInstalled is returned by Uninstall/Start/Stop when no service with
// the given name is registered.
var ErrNotInstalled = errors.New("svcmgr: service not installed")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/svcmgr/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/svcmgr.go internal/svcmgr/svcmgr_test.go
git commit -m "feat(svcmgr): add core Manager types and sentinel errors" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: `internal/svcmgr` Darwin stub (explicit unsupported error)

**Files:**
- Create: `internal/svcmgr/manager_darwin.go`
- Test: `internal/svcmgr/manager_darwin_test.go`

**Interfaces:**
- Consumes: `svcmgr.ManagerOptions`, `svcmgr.Manager` (Task 1).
- Produces: `NewManager(opts ManagerOptions) (Manager, error)` for `GOOS=darwin` builds — always returns a non-nil, descriptive error.

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/manager_darwin_test.go
//go:build darwin

package svcmgr

import "testing"

func TestNewManager_DarwinIsUnsupported(t *testing.T) {
	_, err := NewManager(ManagerOptions{})
	if err == nil {
		t.Fatal("expected an error on darwin, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run (only meaningful on a darwin host or with `GOOS=darwin go vet ./internal/svcmgr/...`; on this Windows dev machine, verify via `GOOS=darwin go build ./internal/svcmgr/...` that it fails to compile until Step 3 lands):
Expected: FAIL — `NewManager` undefined for darwin build.

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/manager_darwin.go
//go:build darwin

package svcmgr

import "fmt"

// NewManager always fails on darwin: service install is not implemented
// for macOS yet. See docs/superpowers/specs/2026-08-10-service-install-design.md
// "Non-goals" — this is a deliberate, documented gap, not an oversight, and
// tracked as a follow-up issue rather than a silent no-op.
func NewManager(ManagerOptions) (Manager, error) {
	return nil, fmt.Errorf("svcmgr: service install is not supported on macOS yet (tracked as a follow-up issue); run boxy agent/serve directly, or install a launchd plist by hand")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOOS=darwin go build ./internal/svcmgr/...` (compiles) and, if a darwin runner is available, `go test ./internal/svcmgr/...`.
Expected: builds cleanly; test passes where it can run.

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/manager_darwin.go internal/svcmgr/manager_darwin_test.go
git commit -m "feat(svcmgr): explicit unsupported error on darwin" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: `internal/svcmgr` Linux systemd Manager

**Files:**
- Create: `internal/svcmgr/manager_linux.go`
- Test: `internal/svcmgr/manager_linux_test.go`

**Interfaces:**
- Consumes: `svcmgr.Spec`, `svcmgr.Status`, `svcmgr.Manager`, `svcmgr.ManagerOptions`, `svcmgr.ErrAlreadyInstalled`, `svcmgr.ErrNotInstalled` (Task 1).
- Produces: `NewManager(opts ManagerOptions) (Manager, error)` for `GOOS=linux`; package-level `var runCommand = func(name string, args ...string) ([]byte, error) { ... }`, overridable by tests in this package and reused by no other task (Windows Task Scheduler in Task 4 declares its own).

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/manager_linux_test.go
//go:build linux

package svcmgr

import (
	"fmt"
	"strings"
	"testing"
)

// fakeRunner records every invocation and returns canned output/errors
// keyed by the joined command line, so tests can assert exactly which
// systemctl/loginctl calls Install/Uninstall/Start/Stop/Status make.
type fakeRunner struct {
	calls   [][]string
	outputs map[string][]byte
	errs    map[string]error
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	return f.outputs[key], f.errs[key]
}

func withFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{outputs: map[string][]byte{}, errs: map[string]error{}}
	orig := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = orig })
	return f
}

func TestRenderUnit_ContainsExecStartAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    "/usr/local/bin/boxy",
		Args:        []string{"agent", "serve", "--service-config", "/home/u/.boxy-agent/service.yaml"},
	}
	unit := renderUnit(spec)
	want := []string{
		"Description=Boxy remote agent",
		`ExecStart=/usr/local/bin/boxy agent serve --service-config /home/u/.boxy-agent/service.yaml`,
		"Restart=on-failure",
	}
	for _, w := range want {
		if !strings.Contains(unit, w) {
			t.Errorf("rendered unit missing %q; got:\n%s", w, unit)
		}
	}
}

func TestSystemdManager_Install_SystemMode_RunsExpectedCommands(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}

	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy", Args: []string{"agent", "serve"}}
	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}

	wantCalls := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", "boxy-agent"},
	}
	if len(f.calls) != len(wantCalls) {
		t.Fatalf("got %d systemctl calls, want %d: %v", len(f.calls), len(wantCalls), f.calls)
	}
	for i, want := range wantCalls {
		got := strings.Join(f.calls[i], " ")
		if got != strings.Join(want, " ") {
			t.Errorf("call %d = %q, want %q", i, got, strings.Join(want, " "))
		}
	}
}

func TestSystemdManager_Install_UserMode_EnablesLinger(t *testing.T) {
	f := withFakeRunner(t)
	t.Setenv("USER", "geo")
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}

	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Join(c, " ") == "loginctl enable-linger geo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a loginctl enable-linger call, got: %v", f.calls)
	}
}

func TestSystemdManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	spec := Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}

	if err := m.Install(spec); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	err := m.Install(spec)
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("second Install error = %v, want ErrAlreadyInstalled", err)
	}
	_ = f
}

func TestSystemdManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: false, unitDir: dir}
	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestSystemdManager_Status_ReportsMode(t *testing.T) {
	f := withFakeRunner(t)
	dir := t.TempDir()
	m := &systemdManager{userMode: true, unitDir: dir}
	if err := m.Install(Spec{Name: "boxy-agent", ExecPath: "/usr/local/bin/boxy"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	f.outputs["systemctl --user is-active boxy-agent"] = []byte("active\n")

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running || st.Mode != "user-unit" {
		t.Fatalf("Status = %+v, want Installed=true Running=true Mode=user-unit", st)
	}
}
```

(This test file needs `"errors"` in its imports for `errors.Is`; add it alongside `"fmt"`, `"strings"`, `"testing"`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/svcmgr/...` (on this Windows dev machine, use `GOOS=linux go vet ./internal/svcmgr/...` to confirm it fails to compile/typecheck; full execution happens on a Linux CI runner per Task 17)
Expected: FAIL — `systemdManager`, `renderUnit`, `runCommand` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/manager_linux.go
//go:build linux

package svcmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runCommand is overridable in tests so systemctl/loginctl calls can be
// faked without a real systemd session.
var runCommand = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}

type systemdManager struct {
	userMode bool
	// unitDir overrides the unit file's parent directory; empty means the
	// real default (computed by unitDir()). Tests set this to a t.TempDir().
	unitDir string
}

// NewManager returns a Linux systemd-backed Manager. UserMode selects a
// per-user unit under ~/.config/systemd/user (no root required) over a
// system unit under /etc/systemd/system (requires root).
func NewManager(opts ManagerOptions) (Manager, error) {
	return &systemdManager{userMode: opts.UserMode}, nil
}

func (m *systemdManager) resolvedUnitDir() (string, error) {
	if m.unitDir != "" {
		return m.unitDir, nil
	}
	if !m.userMode {
		return "/etc/systemd/system", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for user unit: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func (m *systemdManager) unitPath(name string) (string, error) {
	dir, err := m.resolvedUnitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".service"), nil
}

func (m *systemdManager) systemctlArgs(args ...string) []string {
	if m.userMode {
		return append([]string{"--user"}, args...)
	}
	return args
}

func (m *systemdManager) mode() string {
	if m.userMode {
		return "user-unit"
	}
	return "system-unit"
}

// renderUnit builds the systemd unit file content for spec. Restart=
// on-failure with a 5s backoff is the boot-time-resilience story called
// for by the spec; the actual command line (spec.Args, already carrying
// --service-config) is what makes the unit reproduce the exact invocation
// captured at install time.
func renderUnit(spec Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", spec.Description)
	fmt.Fprintf(&b, "After=network-online.target\n")
	fmt.Fprintf(&b, "Wants=network-online.target\n\n")
	fmt.Fprintf(&b, "[Service]\n")
	fmt.Fprintf(&b, "Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(append([]string{spec.ExecPath}, spec.Args...), " "))
	fmt.Fprintf(&b, "Restart=on-failure\n")
	fmt.Fprintf(&b, "RestartSec=5\n\n")
	fmt.Fprintf(&b, "[Install]\n")
	return b.String()
}

// renderUnitFor renders the unit with the correct [Install] target for the
// manager's mode (system units target multi-user.target; user units target
// the systemd --user default.target).
func (m *systemdManager) renderUnitFor(spec Spec) string {
	base := renderUnit(spec)
	target := "multi-user.target"
	if m.userMode {
		target = "default.target"
	}
	return base + fmt.Sprintf("WantedBy=%s\n", target)
}

func (m *systemdManager) Install(spec Spec) error {
	st, err := m.Status(spec.Name)
	if err != nil {
		return err
	}
	if st.Installed {
		return ErrAlreadyInstalled
	}

	path, err := m.unitPath(spec.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create unit directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(m.renderUnitFor(spec)), 0o644); err != nil {
		return fmt.Errorf("write unit file %q: %w", path, err)
	}

	if out, err := runCommand("systemctl", m.systemctlArgs("daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := runCommand("systemctl", m.systemctlArgs("enable", "--now", spec.Name)...); err != nil {
		return fmt.Errorf("systemctl enable --now %s: %w: %s", spec.Name, err, out)
	}

	if m.userMode {
		user := os.Getenv("USER")
		if out, err := runCommand("loginctl", "enable-linger", user); err != nil {
			return fmt.Errorf("loginctl enable-linger %s failed (unit installed, but it won't start at boot without an active login until this succeeds — some managed hosts restrict linger via polkit; retry manually with `loginctl enable-linger %s`): %w: %s", user, user, err, out)
		}
	}
	return nil
}

func (m *systemdManager) Uninstall(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}

	if out, err := runCommand("systemctl", m.systemctlArgs("disable", "--now", name)...); err != nil {
		return fmt.Errorf("systemctl disable --now %s: %w: %s", name, err, out)
	}
	path, err := m.unitPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove unit file %q: %w", path, err)
	}
	_, _ = runCommand("systemctl", m.systemctlArgs("daemon-reload")...)
	return nil
}

func (m *systemdManager) Start(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	if out, err := runCommand("systemctl", m.systemctlArgs("start", name)...); err != nil {
		return fmt.Errorf("systemctl start %s: %w: %s", name, err, out)
	}
	return nil
}

func (m *systemdManager) Stop(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	if out, err := runCommand("systemctl", m.systemctlArgs("stop", name)...); err != nil {
		return fmt.Errorf("systemctl stop %s: %w: %s", name, err, out)
	}
	return nil
}

func (m *systemdManager) Status(name string) (Status, error) {
	path, err := m.unitPath(name)
	if err != nil {
		return Status{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	} else if err != nil {
		return Status{}, fmt.Errorf("stat unit file %q: %w", path, err)
	}

	out, _ := runCommand("systemctl", m.systemctlArgs("is-active", name)...)
	running := strings.TrimSpace(string(out)) == "active"
	return Status{Installed: true, Running: running, Mode: m.mode()}, nil
}
```

Add `"os/exec"` to the import block (used by the default `runCommand`).

- [ ] **Step 4: Run test to verify it passes**

Run (on a Linux host, or trust CI from Task 17 for full execution; use `GOOS=linux go vet ./internal/svcmgr/...` here to confirm it type-checks): `go test ./internal/svcmgr/... -run TestSystemdManager -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/manager_linux.go internal/svcmgr/manager_linux_test.go
git commit -m "feat(svcmgr): Linux systemd system/user unit manager" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: `internal/svcmgr` Windows Task Scheduler Manager (unprivileged fallback)

**Files:**
- Create: `internal/svcmgr/manager_windows_schtasks.go`
- Test: `internal/svcmgr/manager_windows_schtasks_test.go`

**Interfaces:**
- Consumes: `svcmgr.Spec`, `svcmgr.Status`, `svcmgr.Manager`, `svcmgr.ErrAlreadyInstalled`, `svcmgr.ErrNotInstalled` (Task 1).
- Produces: `taskSchedulerManager` type with `Install/Uninstall/Start/Stop/Status`, and package-level `var runCommand = func(name string, args ...string) ([]byte, error) { ... }` (declared once for the whole `windows` build in this file; Task 5's SCM manager and Task 8 must not redeclare it).

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/manager_windows_schtasks_test.go
//go:build windows

package svcmgr

import (
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	outputs map[string][]byte
	errs    map[string]error
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	return f.outputs[key], f.errs[key]
}

func withFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{outputs: map[string][]byte{}, errs: map[string]error{}}
	orig := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = orig })
	return f
}

func TestRenderTaskXML_ContainsLogonTriggerHiddenAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    `C:\Users\geo\.local\bin\boxy.exe`,
		Args:        []string{"agent", "serve", "--service-config", `C:\Users\geo\.boxy-agent\service.yaml`},
	}
	xml := renderTaskXML(spec)
	for _, want := range []string{
		"<LogonTrigger>",
		"<Hidden>true</Hidden>",
		"<RestartOnFailure>",
		`<Command>C:\Users\geo\.local\bin\boxy.exe</Command>`,
		`<Arguments>agent serve --service-config C:\Users\geo\.boxy-agent\service.yaml</Arguments>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("rendered task XML missing %q; got:\n%s", want, xml)
		}
	}
}

func TestTaskSchedulerManager_Install_RunsSchtasksCreate(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	spec := Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`, Args: []string{"agent", "serve"}}

	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0][0] != "schtasks" {
		t.Fatalf("expected exactly one schtasks call, got: %v", f.calls)
	}
	joined := strings.Join(f.calls[0], " ")
	for _, want := range []string{"/create", "/tn", "boxy-agent", "/xml", "/f"} {
		if !strings.Contains(joined, want) {
			t.Errorf("schtasks call missing %q: %s", want, joined)
		}
	}
}

func TestTaskSchedulerManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("boxy-agent  Ready")

	err := m.Install(Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("Install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestTaskSchedulerManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	f.errs["schtasks /query /tn boxy-agent"] = errors.New("ERROR: The system cannot find the file specified.")
	m := &taskSchedulerManager{}

	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestTaskSchedulerManager_Status_ReportsUserTaskMode(t *testing.T) {
	f := withFakeRunner(t)
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("TaskName  Status\r\nboxy-agent  Running\r\n")
	m := &taskSchedulerManager{}

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running || st.Mode != "user-task" {
		t.Fatalf("Status = %+v, want Installed=true Running=true Mode=user-task", st)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/svcmgr/... -run 'TestRenderTaskXML|TestTaskSchedulerManager' -v`
Expected: FAIL — `taskSchedulerManager`, `renderTaskXML`, `runCommand` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/manager_windows_schtasks.go
//go:build windows

package svcmgr

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runCommand is overridable in tests so schtasks.exe calls can be faked
// without actually touching the Task Scheduler. It is the single
// definition shared by every exec-based Windows manager in this package.
var runCommand = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}

// taskSchedulerManager installs boxy as a per-user Task Scheduler task
// triggered at logon — the unprivileged fallback to a real Windows
// Service. This is a real behavioral difference from the SCM-backed
// manager: it starts at user logon, not at machine boot before any login.
type taskSchedulerManager struct{}

func (m *taskSchedulerManager) Install(spec Spec) error {
	st, err := m.Status(spec.Name)
	if err != nil {
		return err
	}
	if st.Installed {
		return ErrAlreadyInstalled
	}

	xmlPath, err := os.CreateTemp("", "boxy-task-*.xml")
	if err != nil {
		return fmt.Errorf("create temp task definition: %w", err)
	}
	defer os.Remove(xmlPath.Name())

	// Task Scheduler's XML importer expects UTF-16LE with a byte-order
	// mark, matching what the Task Scheduler UI itself exports.
	encoded, err := utf16LEWithBOM(renderTaskXML(spec))
	if err != nil {
		_ = xmlPath.Close()
		return fmt.Errorf("encode task definition: %w", err)
	}
	if _, err := xmlPath.Write(encoded); err != nil {
		_ = xmlPath.Close()
		return fmt.Errorf("write task definition: %w", err)
	}
	if err := xmlPath.Close(); err != nil {
		return fmt.Errorf("close task definition: %w", err)
	}

	out, err := runCommand("schtasks", "/create", "/tn", spec.Name, "/xml", xmlPath.Name(), "/f")
	if err != nil {
		return fmt.Errorf("schtasks /create: %w: %s", err, out)
	}
	return nil
}

func (m *taskSchedulerManager) Uninstall(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	out, err := runCommand("schtasks", "/delete", "/tn", name, "/f")
	if err != nil {
		return fmt.Errorf("schtasks /delete: %w: %s", err, out)
	}
	return nil
}

func (m *taskSchedulerManager) Start(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	out, err := runCommand("schtasks", "/run", "/tn", name)
	if err != nil {
		return fmt.Errorf("schtasks /run: %w: %s", err, out)
	}
	return nil
}

func (m *taskSchedulerManager) Stop(name string) error {
	st, err := m.Status(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	out, err := runCommand("schtasks", "/end", "/tn", name)
	if err != nil {
		return fmt.Errorf("schtasks /end: %w: %s", err, out)
	}
	return nil
}

func (m *taskSchedulerManager) Status(name string) (Status, error) {
	out, err := runCommand("schtasks", "/query", "/tn", name)
	if err != nil {
		// schtasks exits non-zero with "cannot find the file specified"
		// (or similar) when the task doesn't exist — treated as
		// not-installed rather than a hard error.
		return Status{}, nil
	}
	running := strings.Contains(string(out), "Running")
	return Status{Installed: true, Running: running, Mode: "user-task"}, nil
}

// renderTaskXML builds a Task Scheduler v2 task definition: a logon
// trigger (any user logon triggers it, matching how the installing user's
// own session starts the agent), a hidden, least-privilege action, and a
// restart-on-failure policy (3 attempts, 1 minute apart) as the
// unprivileged-mode substitute for a real service's SCM recovery actions.
func renderTaskXML(spec Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n")
	fmt.Fprintf(&b, "    <Description>%s</Description>\n", spec.Description)
	b.WriteString("  </RegistrationInfo>\n")
	b.WriteString("  <Triggers>\n")
	b.WriteString("    <LogonTrigger>\n")
	b.WriteString("      <Enabled>true</Enabled>\n")
	b.WriteString("    </LogonTrigger>\n")
	b.WriteString("  </Triggers>\n")
	b.WriteString("  <Principals>\n")
	b.WriteString(`    <Principal id="Author">` + "\n")
	b.WriteString("      <LogonType>InteractiveToken</LogonType>\n")
	b.WriteString("      <RunLevel>LeastPrivilege</RunLevel>\n")
	b.WriteString("    </Principal>\n")
	b.WriteString("  </Principals>\n")
	b.WriteString("  <Settings>\n")
	b.WriteString("    <Hidden>true</Hidden>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	b.WriteString("    <RestartOnFailure>\n")
	b.WriteString("      <Interval>PT1M</Interval>\n")
	b.WriteString("      <Count>3</Count>\n")
	b.WriteString("    </RestartOnFailure>\n")
	b.WriteString("  </Settings>\n")
	b.WriteString("  <Actions>\n")
	b.WriteString("    <Exec>\n")
	fmt.Fprintf(&b, "      <Command>%s</Command>\n", spec.ExecPath)
	fmt.Fprintf(&b, "      <Arguments>%s</Arguments>\n", strings.Join(spec.Args, " "))
	b.WriteString("    </Exec>\n")
	b.WriteString("  </Actions>\n")
	b.WriteString("</Task>\n")
	return b.String()
}

// utf16LEWithBOM encodes s as UTF-16LE with a leading byte-order mark, the
// encoding Task Scheduler's XML importer expects.
func utf16LEWithBOM(s string) ([]byte, error) {
	runes := []rune(s)
	units := make([]uint16, 0, len(runes)+1)
	units = append(units, 0xFEFF) // BOM
	for _, r := range runes {
		if r > 0xFFFF {
			return nil, fmt.Errorf("unsupported rune %U in task XML (outside BMP)", r)
		}
		units = append(units, uint16(r))
	}
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u)
		out[i*2+1] = byte(u >> 8)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/svcmgr/... -run 'TestRenderTaskXML|TestTaskSchedulerManager' -v`
Expected: PASS (this dev machine is Windows, so this runs for real, unlike the Linux manager in Task 3).

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/manager_windows_schtasks.go internal/svcmgr/manager_windows_schtasks_test.go
git commit -m "feat(svcmgr): Windows Task Scheduler manager (unprivileged fallback)" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: `internal/svcmgr` Windows SCM Manager (real service, default/privileged)

**Files:**
- Create: `internal/svcmgr/manager_windows_scm.go`
- Test: `internal/svcmgr/manager_windows_scm_test.go`

**Interfaces:**
- Consumes: `svcmgr.Spec`, `svcmgr.Status`, `svcmgr.Manager`, `svcmgr.ErrAlreadyInstalled`, `svcmgr.ErrNotInstalled` (Task 1).
- Produces: `scmManager` type implementing `Manager`; `scmAPI`/`scmService` interfaces and `var connectSCM = func() (scmAPI, error) { ... }` (overridable in tests — this is the seam that lets Install/Uninstall/Start/Stop/Status be unit-tested without real Administrator rights).

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/manager_windows_scm_test.go
//go:build windows

package svcmgr

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// fakeSCM and fakeSCMService fake just enough of the SCM surface for
// scmManager's tests, keyed by service name.
type fakeSCM struct {
	services map[string]*fakeSCMService
}

type fakeSCMService struct {
	status  svc.Status
	deleted bool
	started bool
}

func (f *fakeSCM) OpenService(name string) (scmService, error) {
	s, ok := f.services[name]
	if !ok || s.deleted {
		return nil, errors.New("service does not exist")
	}
	return s, nil
}

func (f *fakeSCM) CreateService(name, _ string, _ mgr.Config, _ ...string) (scmService, error) {
	if f.services == nil {
		f.services = map[string]*fakeSCMService{}
	}
	s := &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	f.services[name] = s
	return s, nil
}

func (f *fakeSCM) Disconnect() error { return nil }

func (s *fakeSCMService) Close() error { return nil }
func (s *fakeSCMService) Delete() error {
	s.deleted = true
	return nil
}
func (s *fakeSCMService) Start(...string) error {
	s.started = true
	s.status.State = svc.Running
	return nil
}
func (s *fakeSCMService) Control(c svc.Cmd) (svc.Status, error) {
	if c == svc.Stop {
		s.status.State = svc.Stopped
	}
	return s.status, nil
}
func (s *fakeSCMService) Query() (svc.Status, error) { return s.status, nil }

func withFakeSCM(t *testing.T) *fakeSCM {
	t.Helper()
	f := &fakeSCM{services: map[string]*fakeSCMService{}}
	orig := connectSCM
	connectSCM = func() (scmAPI, error) { return f, nil }
	t.Cleanup(func() { connectSCM = orig })
	return f
}

func TestSCMManager_Install_CreatesService(t *testing.T) {
	f := withFakeSCM(t)
	m := &scmManager{}

	if err := m.Install(Spec{Name: "boxy-agent", DisplayName: "Boxy Agent", ExecPath: `C:\boxy.exe`, Args: []string{"agent", "serve"}}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := f.services["boxy-agent"]; !ok {
		t.Fatal("expected boxy-agent to be created in the fake SCM")
	}
}

func TestSCMManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	err := m.Install(Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("Install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestSCMManager_Uninstall_DeletesService(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	if err := m.Uninstall("boxy-agent"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !f.services["boxy-agent"].deleted {
		t.Fatal("expected service to be marked deleted")
	}
}

func TestSCMManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	withFakeSCM(t)
	m := &scmManager{}
	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestSCMManager_StartStop_ChangeStatus(t *testing.T) {
	f := withFakeSCM(t)
	f.services["boxy-agent"] = &fakeSCMService{status: svc.Status{State: svc.Stopped}}
	m := &scmManager{}

	if err := m.Start("boxy-agent"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Running || st.Mode != "system-service" {
		t.Fatalf("Status after Start = %+v, want Running=true Mode=system-service", st)
	}

	if err := m.Stop("boxy-agent"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st, err = m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Fatalf("Status after Stop = %+v, want Running=false", st)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/svcmgr/... -run TestSCMManager -v`
Expected: FAIL — `scmManager`, `scmAPI`, `scmService`, `connectSCM` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/manager_windows_scm.go
//go:build windows

package svcmgr

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// scmAPI and scmService narrow the mgr.Mgr/mgr.Service surface this
// package needs, so tests can fake the Windows Service Control Manager
// without requiring Administrator rights or a real Windows host. *mgr.Mgr
// and *mgr.Service satisfy these structurally (see realSCM below) — no
// wrapper methods needed beyond the OpenService/CreateService return-type
// adaptation.
type scmAPI interface {
	OpenService(name string) (scmService, error)
	CreateService(name, exepath string, c mgr.Config, args ...string) (scmService, error)
	Disconnect() error
}

type scmService interface {
	Close() error
	Delete() error
	Start(args ...string) error
	Control(c svc.Cmd) (svc.Status, error)
	Query() (svc.Status, error)
}

type realSCM struct{ m *mgr.Mgr }

func (r realSCM) OpenService(name string) (scmService, error) { return r.m.OpenService(name) }

func (r realSCM) CreateService(name, exepath string, c mgr.Config, args ...string) (scmService, error) {
	return r.m.CreateService(name, exepath, c, args...)
}

func (r realSCM) Disconnect() error { return r.m.Disconnect() }

// connectSCM is overridable in tests so Install/Uninstall/Start/Stop/Status
// can be unit-tested without a real, elevated SCM connection.
var connectSCM = func() (scmAPI, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	return realSCM{m}, nil
}

// scmManager installs boxy as a real Windows Service via the Service
// Control Manager. Requires an elevated (Administrator) process — callers
// must check privilege before constructing/using this (see
// internal/cli's isElevated).
type scmManager struct{}

func (m *scmManager) Install(spec Spec) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	if existing, err := scm.OpenService(spec.Name); err == nil {
		_ = existing.Close()
		return ErrAlreadyInstalled
	}

	svcCfg := mgr.Config{
		DisplayName: spec.DisplayName,
		Description: spec.Description,
		StartType:   mgr.StartAutomatic,
	}
	s, err := scm.CreateService(spec.Name, spec.ExecPath, svcCfg, spec.Args...)
	if err != nil {
		return fmt.Errorf("create service %q: %w", spec.Name, err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q after install: %w", spec.Name, err)
	}
	return nil
}

func (m *scmManager) Uninstall(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Start(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Stop(name string) error {
	scm, err := connectSCM()
	if err != nil {
		return fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return ErrNotInstalled
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service %q: %w", name, err)
	}
	return nil
}

func (m *scmManager) Status(name string) (Status, error) {
	scm, err := connectSCM()
	if err != nil {
		return Status{}, fmt.Errorf("connect to service control manager: %w", err)
	}
	defer func() { _ = scm.Disconnect() }()

	s, err := scm.OpenService(name)
	if err != nil {
		return Status{}, nil
	}
	defer func() { _ = s.Close() }()

	st, err := s.Query()
	if err != nil {
		return Status{}, fmt.Errorf("query service %q: %w", name, err)
	}
	return Status{Installed: true, Running: st.State == svc.Running, Mode: "system-service"}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/svcmgr/... -run TestSCMManager -v`
Expected: PASS

- [ ] **Step 5: Promote `golang.org/x/sys` to a direct dependency**

```bash
go mod tidy
```

Verify `go.mod`'s `golang.org/x/sys` line no longer has the `// indirect` suffix.

- [ ] **Step 6: Commit**

```bash
git add internal/svcmgr/manager_windows_scm.go internal/svcmgr/manager_windows_scm_test.go go.mod go.sum
git commit -m "feat(svcmgr): Windows SCM manager (real service, default/privileged)" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 6: `internal/svcmgr` platform `NewManager` dispatchers (Windows + Linux)

**Files:**
- Create: `internal/svcmgr/manager_windows.go`
- Modify: `internal/svcmgr/manager_linux.go` (already has `NewManager` from Task 3 — no change needed, listed here only for cross-reference)
- Test: `internal/svcmgr/manager_windows_test.go`

**Interfaces:**
- Consumes: `scmManager` (Task 5), `taskSchedulerManager` (Task 4), `ManagerOptions`, `Manager` (Task 1).
- Produces: `NewManager(opts ManagerOptions) (Manager, error)` for `GOOS=windows` — the one exported entry point `internal/cli` calls; it must not know about `scmManager`/`taskSchedulerManager` directly.

- [ ] **Step 1: Write the failing test**

```go
// internal/svcmgr/manager_windows_test.go
//go:build windows

package svcmgr

import "testing"

func TestNewManager_Windows_DispatchesOnUserMode(t *testing.T) {
	priv, err := NewManager(ManagerOptions{UserMode: false})
	if err != nil {
		t.Fatalf("NewManager(UserMode: false): %v", err)
	}
	if _, ok := priv.(*scmManager); !ok {
		t.Fatalf("NewManager(UserMode: false) = %T, want *scmManager", priv)
	}

	user, err := NewManager(ManagerOptions{UserMode: true})
	if err != nil {
		t.Fatalf("NewManager(UserMode: true): %v", err)
	}
	if _, ok := user.(*taskSchedulerManager); !ok {
		t.Fatalf("NewManager(UserMode: true) = %T, want *taskSchedulerManager", user)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/svcmgr/... -run TestNewManager_Windows -v`
Expected: FAIL — `NewManager` undefined for the `windows` build (Tasks 4/5 deliberately didn't define it).

- [ ] **Step 3: Write the implementation**

```go
// internal/svcmgr/manager_windows.go
//go:build windows

package svcmgr

// NewManager returns a Windows-backed Manager: the real SCM-registered
// service by default (requires an elevated/Administrator caller — see
// internal/cli's isElevated, checked before this is ever called), or the
// Task Scheduler at-logon fallback when opts.UserMode is true.
func NewManager(opts ManagerOptions) (Manager, error) {
	if opts.UserMode {
		return &taskSchedulerManager{}, nil
	}
	return &scmManager{}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/svcmgr/... -run TestNewManager_Windows -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/manager_windows.go internal/svcmgr/manager_windows_test.go
git commit -m "feat(svcmgr): Windows NewManager dispatch (SCM vs Task Scheduler)" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 7: `internal/svcmgr` token encrypt/decrypt (DPAPI on Windows, identity elsewhere)

**Files:**
- Create: `internal/svcmgr/token_windows.go`
- Create: `internal/svcmgr/token_other.go`
- Test: `internal/svcmgr/token_windows_test.go`
- Test: `internal/svcmgr/token_other_test.go`

**Interfaces:**
- Produces (same exported names on every platform, used by `internal/cli`'s service-config persistence in Task 10):
  ```go
  func EncryptToken(plaintext []byte) ([]byte, error)
  func DecryptToken(ciphertext []byte) ([]byte, error)
  ```
  On Windows: DPAPI machine-scope round-trip. On every other platform: identity (Linux protects the token via file permissions instead, per spec).

- [ ] **Step 1: Write the failing tests**

```go
// internal/svcmgr/token_windows_test.go
//go:build windows

package svcmgr

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptToken_RoundTrips(t *testing.T) {
	plain := []byte("single-use-registration-token")

	enc, err := EncryptToken(plain)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("EncryptToken returned plaintext unchanged — expected DPAPI-encrypted bytes")
	}

	dec, err := DecryptToken(enc)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("DecryptToken = %q, want %q", dec, plain)
	}
}

func TestEncryptToken_EmptyInput_ReturnsEmptyOutput(t *testing.T) {
	enc, err := EncryptToken(nil)
	if err != nil {
		t.Fatalf("EncryptToken(nil): %v", err)
	}
	if len(enc) != 0 {
		t.Fatalf("EncryptToken(nil) = %v, want empty", enc)
	}
}
```

```go
// internal/svcmgr/token_other_test.go
//go:build !windows

package svcmgr

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptToken_IdentityOnNonWindows(t *testing.T) {
	plain := []byte("single-use-registration-token")

	enc, err := EncryptToken(plain)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}
	if !bytes.Equal(enc, plain) {
		t.Fatalf("EncryptToken = %q, want unchanged %q (Linux protects via file perms, not DPAPI)", enc, plain)
	}

	dec, err := DecryptToken(enc)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("DecryptToken = %q, want %q", dec, plain)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/svcmgr/... -run TestEncryptDecryptToken -v`
Expected: FAIL — `EncryptToken`/`DecryptToken` undefined.

- [ ] **Step 3: Write the implementations**

```go
// internal/svcmgr/token_windows.go
//go:build windows

package svcmgr

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// cryptProtectLocalMachine (CRYPTPROTECT_LOCAL_MACHINE) makes the
// encrypted blob decryptable by any sufficiently-privileged process on
// this machine, rather than tied to one interactive user's DPAPI master
// key — the right scope for a Windows Service, which commonly runs as
// LocalSystem or a dedicated service account rather than an interactive
// profile.
const cryptProtectLocalMachine = 0x4

// EncryptToken protects plaintext (the agent's single-use bootstrap
// token) at rest using DPAPI machine-scope, so the persisted service
// config file isn't a plaintext secret between install and first
// successful registration.
func EncryptToken(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(plaintext)), Data: &plaintext[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, cryptProtectLocalMachine, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }()

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return result, nil
}

// DecryptToken reverses EncryptToken.
func DecryptToken(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, cryptProtectLocalMachine, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }()

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return result, nil
}
```

```go
// internal/svcmgr/token_other.go
//go:build !windows

package svcmgr

// EncryptToken is the identity function on non-Windows platforms — Linux
// protects the persisted service config file via 0600 file permissions
// instead of at-rest encryption (see docs/superpowers/specs/2026-08-10-service-install-design.md).
func EncryptToken(plaintext []byte) ([]byte, error) { return plaintext, nil }

// DecryptToken is the identity function on non-Windows platforms.
func DecryptToken(ciphertext []byte) ([]byte, error) { return ciphertext, nil }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/svcmgr/... -run TestEncryptDecryptToken -v`
Expected: PASS (this dev machine is Windows, so `token_windows_test.go` runs for real; `token_other_test.go` is excluded by its build tag here and exercised by the Linux CI job from Task 17).

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/token_windows.go internal/svcmgr/token_other.go internal/svcmgr/token_windows_test.go internal/svcmgr/token_other_test.go
git commit -m "feat(svcmgr): DPAPI token encryption on Windows, identity elsewhere" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 8: `internal/svcmgr` `RunAsWindowsService` helper

**Files:**
- Create: `internal/svcmgr/winservice_windows.go`
- Create: `internal/svcmgr/winservice_other.go`
- Test: `internal/svcmgr/winservice_windows_test.go`
- Test: `internal/svcmgr/winservice_other_test.go`

**Interfaces:**
- Produces (same signature every platform, used by `internal/cli`'s `agent_serve.go`/`serve.go` in Tasks 11–12):
  ```go
  func RunAsWindowsService(name string, run func(ctx context.Context) error) (handled bool, err error)
  ```
  `handled == false` means "not launched by the SCM — caller should run normally"; `handled == true` means this function fully drove the service lifecycle and `err` is its outcome.

- [ ] **Step 1: Write the failing tests**

```go
// internal/svcmgr/winservice_windows_test.go
//go:build windows

package svcmgr

import (
	"context"
	"testing"
)

func TestRunAsWindowsService_NotAService_ReturnsUnhandled(t *testing.T) {
	// go test itself is not launched by the SCM, so svc.IsWindowsService()
	// is false here — this exercises the real detection path, not a fake.
	called := false
	handled, err := RunAsWindowsService("boxy-agent-test", func(ctx context.Context) error {
		called = true
		return nil
	})
	if handled {
		t.Fatal("expected handled=false when not running as a Windows service")
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if called {
		t.Fatal("run must not be invoked when handled=false — the caller runs it itself")
	}
}
```

```go
// internal/svcmgr/winservice_other_test.go
//go:build !windows

package svcmgr

import (
	"context"
	"testing"
)

func TestRunAsWindowsService_AlwaysUnhandledOnNonWindows(t *testing.T) {
	called := false
	handled, err := RunAsWindowsService("boxy-agent-test", func(ctx context.Context) error {
		called = true
		return nil
	})
	if handled || err != nil || called {
		t.Fatalf("handled=%v err=%v called=%v, want false/nil/false", handled, err, called)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/svcmgr/... -run TestRunAsWindowsService -v`
Expected: FAIL — `RunAsWindowsService` undefined.

- [ ] **Step 3: Write the implementations**

```go
// internal/svcmgr/winservice_windows.go
//go:build windows

package svcmgr

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// RunAsWindowsService checks whether the current process was launched by
// the Service Control Manager. If not, it returns (false, nil) so the
// caller proceeds with a normal foreground run. If so, it drives the
// svc.Handler protocol itself: run(ctx) executes in a goroutine, and an
// SCM stop/shutdown request cancels ctx and waits for run to return before
// reporting Stopped.
func RunAsWindowsService(name string, run func(ctx context.Context) error) (bool, error) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("determine windows service session: %w", err)
	}
	if !isSvc {
		return false, nil
	}

	h := &winServiceHandler{run: run}
	if err := svc.Run(name, h); err != nil {
		return true, fmt.Errorf("run windows service %q: %w", name, err)
	}
	return true, h.runErr
}

type winServiceHandler struct {
	run    func(ctx context.Context) error
	runErr error
}

func (h *winServiceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.runErr = h.run(ctx)
		close(done)
	}()

	s <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case <-done:
			s <- svc.Status{State: svc.Stopped}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				s <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				s <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
```

```go
// internal/svcmgr/winservice_other.go
//go:build !windows

package svcmgr

import "context"

// RunAsWindowsService always reports unhandled on non-Windows platforms —
// there is no SCM to detect. Kept with the same signature as the Windows
// implementation so internal/cli's agent_serve.go/serve.go can call it
// unconditionally without a build tag of their own.
func RunAsWindowsService(_ string, _ func(ctx context.Context) error) (bool, error) {
	return false, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/svcmgr/... -run TestRunAsWindowsService -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/svcmgr/winservice_windows.go internal/svcmgr/winservice_other.go internal/svcmgr/winservice_windows_test.go internal/svcmgr/winservice_other_test.go
git commit -m "feat(svcmgr): RunAsWindowsService SCM detection/bridging helper" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 9: `internal/cli` privilege detection (`isElevated`)

**Files:**
- Create: `internal/cli/privilege_windows.go`
- Create: `internal/cli/privilege_unix.go`
- Test: `internal/cli/privilege_unix_test.go`

**Interfaces:**
- Produces: `func isElevated() (bool, error)` — same signature both platforms, consumed by `agent_service.go`/`serve_service.go` in Tasks 13–14.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/privilege_unix_test.go
//go:build !windows

package cli

import (
	"os"
	"testing"
)

func TestIsElevated_MatchesEUID(t *testing.T) {
	elevated, err := isElevated()
	if err != nil {
		t.Fatalf("isElevated: %v", err)
	}
	want := os.Geteuid() == 0
	if elevated != want {
		t.Fatalf("isElevated() = %v, want %v (euid=%d)", elevated, want, os.Geteuid())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/... -run TestIsElevated -v` (only meaningful on non-Windows; on this Windows dev machine confirm via `GOOS=linux go vet ./internal/cli/...` that `isElevated` is undefined for the `!windows` build)
Expected: FAIL

- [ ] **Step 3: Write the implementations**

```go
// internal/cli/privilege_unix.go
//go:build !windows

package cli

import "os"

// isElevated reports whether the current process is running as root —
// the Unix precondition for installing a system-level systemd unit.
func isElevated() (bool, error) {
	return os.Geteuid() == 0, nil
}
```

```go
// internal/cli/privilege_windows.go
//go:build windows

package cli

import "golang.org/x/sys/windows"

// isElevated reports whether the current process token is elevated
// (running as Administrator) — the precondition for registering a real
// Windows Service via SCM.
func isElevated() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/... -run TestIsElevated -v`
Expected: PASS on a non-Windows runner (Task 17's CI matrix); on this Windows dev machine, confirm `internal/cli/privilege_windows.go` at least compiles via `go build ./internal/cli/...`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/privilege_windows.go internal/cli/privilege_unix.go internal/cli/privilege_unix_test.go
git commit -m "feat(cli): isElevated privilege detection for service install" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 10: `internal/cli` service config YAML persistence + path resolution

**Files:**
- Create: `internal/cli/service_config.go`
- Test: `internal/cli/service_config_test.go`

**Interfaces:**
- Consumes: `svcmgr.EncryptToken`, `svcmgr.DecryptToken` (Task 7); `agentServeOpts` (existing, `internal/cli/agent_serve.go`); `serveOpts` (existing, `internal/cli/serve.go`).
- Produces (consumed by Tasks 11–14):
  ```go
  type agentServiceConfig struct {
      Server    string   `yaml:"server"`
      Providers []string `yaml:"providers"`
      Token     string   `yaml:"token,omitempty"` // base64 of svcmgr.EncryptToken output; empty once registered
      Name      string   `yaml:"name,omitempty"`
      CACert    string   `yaml:"ca_cert,omitempty"`
      DataDir   string   `yaml:"data_dir"`
      Insecure  bool     `yaml:"insecure,omitempty"`
      LogFile   string   `yaml:"log_file"`
  }
  func saveAgentServiceConfig(path string, cfg agentServiceConfig) error
  func loadAgentServiceConfig(path string) (agentServiceConfig, error)
  func scrubAgentServiceConfigToken(path string) error

  type serveServiceConfig struct {
      ConfigPath   string   `yaml:"config_path,omitempty"`
      Listen       string   `yaml:"listen,omitempty"`
      UI           bool     `yaml:"ui"`
      GRPCListen   string   `yaml:"grpc_listen,omitempty"`
      GRPCCertSANs []string `yaml:"grpc_cert_sans,omitempty"`
      Insecure     bool     `yaml:"insecure,omitempty"`
      LogFile      string   `yaml:"log_file"`
  }
  func saveServeServiceConfig(path string, cfg serveServiceConfig) error
  func loadServeServiceConfig(path string) (serveServiceConfig, error)

  func resolveAbs(path string) (string, error) // wraps filepath.Abs with a clearer error
  ```

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/service_config_test.go
package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgentServiceConfig_SaveLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := agentServiceConfig{
		Server:    "boxy-server:9091",
		Providers: []string{"docker", "hyperv"},
		Token:     "raw-bootstrap-token",
		Name:      "agent-1",
		DataDir:   filepath.Join(dir, ".boxy-agent"),
		LogFile:   filepath.Join(dir, ".boxy-agent", "service.log"),
	}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	got, err := loadAgentServiceConfig(path)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if got.Server != cfg.Server || got.Token != cfg.Token || got.DataDir != cfg.DataDir {
		t.Fatalf("round-tripped config = %+v, want %+v", got, cfg)
	}
}

func TestAgentServiceConfig_TokenIsNotStoredAsPlaintextOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := agentServiceConfig{Server: "s:9091", Providers: []string{"docker"}, Token: "super-secret-token", DataDir: dir, LogFile: filepath.Join(dir, "service.log")}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-token") {
		t.Fatal("service config file must not contain the raw token — it must be base64(EncryptToken(...))-encoded on disk")
	}
}

func TestScrubAgentServiceConfigToken_ClearsTokenButKeepsRestOfConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")
	cfg := agentServiceConfig{Server: "s:9091", Providers: []string{"docker"}, Token: "burn-me", DataDir: dir, LogFile: filepath.Join(dir, "service.log")}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	if err := scrubAgentServiceConfigToken(path); err != nil {
		t.Fatalf("scrubAgentServiceConfigToken: %v", err)
	}

	got, err := loadAgentServiceConfig(path)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if got.Token != "" {
		t.Fatalf("Token = %q after scrub, want empty", got.Token)
	}
	if got.Server != cfg.Server || got.DataDir != cfg.DataDir {
		t.Fatalf("scrub must not touch other fields: got %+v", got)
	}
}

func TestServeServiceConfig_SaveLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := serveServiceConfig{
		ConfigPath: filepath.Join(dir, "boxy.yaml"),
		Listen:     ":9090",
		UI:         true,
		GRPCListen: ":9091",
		LogFile:    filepath.Join(dir, "service.log"),
	}
	if err := saveServeServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	got, err := loadServeServiceConfig(path)
	if err != nil {
		t.Fatalf("loadServeServiceConfig: %v", err)
	}
	// serveServiceConfig has a []string field (GRPCCertSANs), so it isn't
	// comparable with == / != — use reflect.DeepEqual instead (add
	// "reflect" to this file's imports).
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round-tripped config = %+v, want %+v", got, cfg)
	}
}

func TestResolveAbs_RelativePathBecomesAbsolute(t *testing.T) {
	got, err := resolveAbs("relative/path")
	if err != nil {
		t.Fatalf("resolveAbs: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("resolveAbs(%q) = %q, want an absolute path", "relative/path", got)
	}
}

func TestResolveAbs_EmptyStringStaysEmpty(t *testing.T) {
	got, err := resolveAbs("")
	if err != nil {
		t.Fatalf("resolveAbs: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveAbs(\"\") = %q, want empty (optional fields like --ca-cert may be unset)", got)
	}
}
```


- [ ] **Step 2: Run test to verify it fails**

Run: `task test:cli`
Expected: FAIL — `agentServiceConfig`, `saveAgentServiceConfig`, etc. undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/cli/service_config.go
package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"gopkg.in/yaml.v3"
)

// agentServiceConfig is the on-disk shape of an installed agent service's
// resolved configuration (written by `boxy agent service install`, read by
// `boxy agent serve --service-config <path>`). All paths are stored
// absolute — a service has no predictable working directory to resolve a
// relative path against. Token is the agent's single-use bootstrap token,
// base64(svcmgr.EncryptToken(...))-encoded at rest and scrubbed to empty
// once the agent successfully registers (see scrubAgentServiceConfigToken
// and its call site in agent_serve.go's OnRegistered callback).
type agentServiceConfig struct {
	Server    string   `yaml:"server"`
	Providers []string `yaml:"providers"`
	Token     string   `yaml:"token,omitempty"`
	Name      string   `yaml:"name,omitempty"`
	CACert    string   `yaml:"ca_cert,omitempty"`
	DataDir   string   `yaml:"data_dir"`
	Insecure  bool     `yaml:"insecure,omitempty"`
	LogFile   string   `yaml:"log_file"`
}

func saveAgentServiceConfig(path string, cfg agentServiceConfig) error {
	if cfg.Token != "" {
		enc, err := svcmgr.EncryptToken([]byte(cfg.Token))
		if err != nil {
			return fmt.Errorf("encrypt token: %w", err)
		}
		cfg.Token = base64.StdEncoding.EncodeToString(enc)
	}
	return writeYAMLFile(path, cfg)
}

func loadAgentServiceConfig(path string) (agentServiceConfig, error) {
	var cfg agentServiceConfig
	if err := readYAMLFile(path, &cfg); err != nil {
		return agentServiceConfig{}, err
	}
	if cfg.Token != "" {
		raw, err := base64.StdEncoding.DecodeString(cfg.Token)
		if err != nil {
			return agentServiceConfig{}, fmt.Errorf("decode stored token: %w", err)
		}
		dec, err := svcmgr.DecryptToken(raw)
		if err != nil {
			return agentServiceConfig{}, fmt.Errorf("decrypt stored token: %w", err)
		}
		cfg.Token = string(dec)
	}
	return cfg, nil
}

// scrubAgentServiceConfigToken clears the token field of an already-saved
// agent service config in place, leaving every other field untouched.
// Called once the agent successfully registers — the token is single-use
// and worthless after that point, so nothing sensitive should linger in
// the file past bootstrap.
func scrubAgentServiceConfigToken(path string) error {
	cfg, err := loadAgentServiceConfig(path)
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		return nil
	}
	cfg.Token = ""
	return saveAgentServiceConfig(path, cfg)
}

// serveServiceConfig is the on-disk shape of an installed serve service's
// resolved configuration. It has no secret field — serve has no bootstrap
// token equivalent.
type serveServiceConfig struct {
	ConfigPath   string   `yaml:"config_path,omitempty"`
	Listen       string   `yaml:"listen,omitempty"`
	UI           bool     `yaml:"ui"`
	GRPCListen   string   `yaml:"grpc_listen,omitempty"`
	GRPCCertSANs []string `yaml:"grpc_cert_sans,omitempty"`
	Insecure     bool     `yaml:"insecure,omitempty"`
	LogFile      string   `yaml:"log_file"`
}

func saveServeServiceConfig(path string, cfg serveServiceConfig) error {
	return writeYAMLFile(path, cfg)
}

func loadServeServiceConfig(path string) (serveServiceConfig, error) {
	var cfg serveServiceConfig
	err := readYAMLFile(path, &cfg)
	return cfg, err
}

func writeYAMLFile(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

func readYAMLFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}

// resolveAbs resolves path to an absolute path. Empty input stays empty —
// optional fields (e.g. --ca-cert, --config) that were never set must not
// be turned into a spurious cwd-relative path.
func resolveAbs(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", path, err)
	}
	return abs, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `task test:cli`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/service_config.go internal/cli/service_config_test.go
git commit -m "feat(cli): service config YAML persistence with token encryption" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 11: Wire `--service-config` into `agent serve` + Windows-service detection + token scrub

**Files:**
- Modify: `internal/cli/agent_serve.go`
- Test: `internal/cli/agent_serve_test.go` (add new tests; existing tests in this file must keep passing unchanged)

**Interfaces:**
- Consumes: `agentServiceConfig`, `loadAgentServiceConfig`, `scrubAgentServiceConfigToken` (Task 10); `svcmgr.RunAsWindowsService` (Task 8).
- Produces: `agentServeOpts.serviceConfigPath` field; `runAgentServe` now loads from file when set. No new exported symbols outside the package — `newAgentServiceCommand` (Task 13) calls `runAgentServe` the same way the existing `agent serve` command's `RunE` does.

- [ ] **Step 1: Write the failing tests**

```go
// Add to internal/cli/agent_serve_test.go

func TestRunAgentServe_ServiceConfig_LoadsOptsFromFile(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".boxy-agent")
	cfgPath := filepath.Join(dir, "service.yaml")

	if err := saveAgentServiceConfig(cfgPath, agentServiceConfig{
		Server:    "127.0.0.1:1", // deliberately unreachable — this test only checks opts resolution, not a real connection
		Providers: []string{"docker"},
		DataDir:   agentDir,
		Insecure:  true,
	}); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	opts, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if opts.server != "127.0.0.1:1" || len(opts.providers) != 1 || opts.providers[0] != "docker" || opts.dataDir != agentDir || !opts.insecure {
		t.Fatalf("resolved opts = %+v, unexpected", opts)
	}
}

func TestRunAgentServe_NoServiceConfigAndNoServer_ErrorsClearly(t *testing.T) {
	_, err := resolveAgentServeOpts(agentServeOpts{})
	if err == nil {
		t.Fatal("expected an error when neither --server nor --service-config is given")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `task test:cli`
Expected: FAIL — `resolveAgentServeOpts` and `agentServeOpts.serviceConfigPath` undefined.

- [ ] **Step 3: Modify `agent_serve.go`**

Add the flag and a `serviceConfigPath` field:

```go
type agentServeOpts struct {
	server            string
	providers         []string
	token             string
	name              string
	caCert            string
	dataDir           string
	insecure          bool
	serviceConfigPath string
}
```

```go
	cmd.Flags().StringVar(&opts.insecure /* unchanged line above stays */, "insecure", false, "connect without TLS (local development only)")
	cmd.Flags().StringVar(&opts.serviceConfigPath, "service-config", "", "load flags from a service config file written by `boxy agent service install` instead of the flags above")
```

(Insert the new `StringVar` call immediately after the existing `--insecure` flag registration in `newAgentServeCommand`.) Remove the two `_ = cmd.MarkFlagRequired(...)` calls for `server` and `providers` — required-ness is now conditional on `--service-config` being unset, enforced in `resolveAgentServeOpts` instead of by cobra.

Add the new resolution function and call it from `runAgentServe`:

```go
// resolveAgentServeOpts returns the effective opts to run with: loaded
// entirely from --service-config's file when set (a service invocation
// carries no other flags), otherwise opts as given directly, validated
// for the flags that used to be cobra-required (--server, --providers).
func resolveAgentServeOpts(opts agentServeOpts) (agentServeOpts, error) {
	if opts.serviceConfigPath == "" {
		if opts.server == "" {
			return agentServeOpts{}, fmt.Errorf("--server is required (or pass --service-config)")
		}
		if len(opts.providers) == 0 {
			return agentServeOpts{}, fmt.Errorf("--providers is required (or pass --service-config)")
		}
		return opts, nil
	}

	cfg, err := loadAgentServiceConfig(opts.serviceConfigPath)
	if err != nil {
		return agentServeOpts{}, fmt.Errorf("load --service-config %q: %w", opts.serviceConfigPath, err)
	}
	return agentServeOpts{
		server:            cfg.Server,
		providers:         cfg.Providers,
		token:             cfg.Token,
		name:              cfg.Name,
		caCert:            cfg.CACert,
		dataDir:           cfg.DataDir,
		insecure:          cfg.Insecure,
		serviceConfigPath: opts.serviceConfigPath,
	}, nil
}
```

At the top of `runAgentServe`, resolve opts first:

```go
func runAgentServe(ctx context.Context, opts agentServeOpts) error {
	opts, err := resolveAgentServeOpts(opts)
	if err != nil {
		return err
	}

	dataDir := opts.dataDir
	// ... rest of the existing function body is unchanged from here
```

Wire the Windows-service bridge and token-scrub-on-registration in `newAgentServeCommand`'s `RunE` and `runAgentServe`'s `OnRegistered` callback:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, err := svcmgr.RunAsWindowsService("boxy-agent", func(ctx context.Context) error {
				return runAgentServe(ctx, opts)
			})
			if handled {
				return err
			}
			return runAgentServe(cmd.Context(), opts)
		},
```

And in `runAgentServe`'s existing `OnRegistered` callback (inside the `agentsdk.Run(...)` call), scrub the token once credentials are persisted, only when running from a service config:

```go
		OnRegistered: func(resp *boxyagentv1.RegisterResponse) {
			slog.Info("registered with server", "agent_id", resp.GetAgentId())
			if len(resp.GetClientCertificatePem()) > 0 {
				if err := persistAgentCredentials(dataDir, resp); err != nil {
					slog.Error("failed to persist issued credentials; reconnects after restart will need a new token", "error", err, "data_dir", dataDir)
				} else if opts.serviceConfigPath != "" {
					if err := scrubAgentServiceConfigToken(opts.serviceConfigPath); err != nil {
						slog.Warn("failed to scrub bootstrap token from service config after registration", "error", err, "path", opts.serviceConfigPath)
					}
				}
			}
		},
```

Add `"github.com/Geogboe/boxy/internal/svcmgr"` to the file's import block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `task test:cli`
Expected: PASS, including every pre-existing test in `agent_serve_test.go` (they call `runAgentServe` directly with `server`/`providers` already set, so `resolveAgentServeOpts`'s early-return path is a no-op for them).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/agent_serve.go internal/cli/agent_serve_test.go
git commit -m "feat(cli): --service-config for agent serve, Windows SCM bridge, token scrub" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 12: Wire `--service-config` into `serve` + Windows-service detection

**Files:**
- Modify: `internal/cli/serve.go`
- Test: `internal/cli/serve_test.go` (add new tests; existing tests must keep passing unchanged)

**Interfaces:**
- Consumes: `serveServiceConfig`, `loadServeServiceConfig` (Task 10); `svcmgr.RunAsWindowsService` (Task 8).
- Produces: `serveOpts.serviceConfigPath` field; a `resolveServeOpts` function mirroring Task 11's `resolveAgentServeOpts` (serve has no required-flag relaxation to do — `serve`'s existing flags are all optional with defaults — so this is purely "load from file when set, else use flags/config as today").

- [ ] **Step 1: Write the failing test**

```go
// Add to internal/cli/serve_test.go

func TestResolveServeOpts_ServiceConfig_LoadsFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "service.yaml")

	if err := saveServeServiceConfig(cfgPath, serveServiceConfig{
		Listen:     ":19090",
		UI:         false,
		GRPCListen: ":19091",
		LogFile:    filepath.Join(dir, "service.log"),
	}); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	opts, err := resolveServeOpts(serveOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}
	if opts.listen != ":19090" || opts.ui != false || opts.grpcListen != ":19091" {
		t.Fatalf("resolved opts = %+v, unexpected", opts)
	}
}

func TestResolveServeOpts_NoServiceConfig_ReturnsOptsUnchanged(t *testing.T) {
	given := serveOpts{listen: ":9090", ui: true}
	got, err := resolveServeOpts(given)
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}
	// serveOpts has a []string field (grpcCertSANs), so it isn't
	// comparable with == / != — use reflect.DeepEqual instead (add
	// "reflect" to this file's imports if serve_test.go doesn't already
	// have it).
	if !reflect.DeepEqual(got, given) {
		t.Fatalf("resolveServeOpts(%+v) = %+v, want unchanged", given, got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `task test:cli`
Expected: FAIL — `serveOpts.serviceConfigPath`, `resolveServeOpts` undefined.

- [ ] **Step 3: Modify `serve.go`**

```go
type serveOpts struct {
	configPath        string
	listen            string
	ui                bool
	grpcListen        string
	grpcCertSANs      []string
	insecure          bool
	serviceConfigPath string
}
```

```go
	cmd.Flags().StringVar(&opts.serviceConfigPath, "service-config", "", "load flags from a service config file written by `boxy serve service install` instead of the flags above")
```

(Insert immediately after the existing `--insecure` flag registration in `newServeCommand`.)

```go
// resolveServeOpts returns the effective opts to run with: loaded
// entirely from --service-config's file when set, otherwise opts
// unchanged.
func resolveServeOpts(opts serveOpts) (serveOpts, error) {
	if opts.serviceConfigPath == "" {
		return opts, nil
	}
	cfg, err := loadServeServiceConfig(opts.serviceConfigPath)
	if err != nil {
		return serveOpts{}, fmt.Errorf("load --service-config %q: %w", opts.serviceConfigPath, err)
	}
	return serveOpts{
		configPath:        cfg.ConfigPath,
		listen:            cfg.Listen,
		ui:                cfg.UI,
		grpcListen:        cfg.GRPCListen,
		grpcCertSANs:      cfg.GRPCCertSANs,
		insecure:          cfg.Insecure,
		serviceConfigPath: opts.serviceConfigPath,
	}, nil
}
```

At the top of `runServe`:

```go
func runServe(ctx context.Context, opts serveOpts, cmd *cobra.Command) error {
	opts, err := resolveServeOpts(opts)
	if err != nil {
		return err
	}

	logFile, _ := cmd.Root().PersistentFlags().GetString("log-file")
	// ... rest of the existing function body is unchanged from here
```

(Rename the existing `logFile, _ := ...` line's surrounding code untouched — only the new `opts, err := resolveServeOpts(opts)` block is inserted above it. Note the pre-existing `err` variable further down in the function — if `runServe` already declares `err` again with `:=` later at line `cfg, cfgPath, err := loadConfig(...)`, that's a **new** `:=` in a nested scope so it doesn't collide, but double-check by reading the current function body before inserting, since a top-level `err :=` here means the later `cfg, cfgPath, err := loadConfig(opts.configPath)` must become `cfg, cfgPath, err = loadConfig(opts.configPath)` if it's in the same scope — verify and adjust so it still compiles.)

Wire the Windows-service bridge in `newServeCommand`'s `RunE`:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, err := svcmgr.RunAsWindowsService("boxy-serve", func(ctx context.Context) error {
				return runServe(ctx, opts, cmd)
			})
			if handled {
				return err
			}
			return runServe(cmd.Context(), opts, cmd)
		},
```

Add `"github.com/Geogboe/boxy/internal/svcmgr"` to the file's import block.

- [ ] **Step 4: Run tests to verify they pass**

Run: `task test:cli`
Expected: PASS, including every pre-existing test in `serve_test.go`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/serve.go internal/cli/serve_test.go
git commit -m "feat(cli): --service-config for serve, Windows SCM bridge" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 13: `boxy agent service` command tree

**Files:**
- Create: `internal/cli/agent_service.go`
- Modify: `internal/cli/agent.go` (register the new subcommand)
- Test: `internal/cli/agent_service_test.go`

**Interfaces:**
- Consumes: `svcmgr.NewManager`, `svcmgr.Spec`, `svcmgr.Manager`, `svcmgr.ErrAlreadyInstalled`, `svcmgr.ErrNotInstalled` (Tasks 1, 6); `isElevated` (Task 9); `agentServiceConfig`, `saveAgentServiceConfig`, `resolveAbs` (Task 10); `agentServeOpts` field names (Task 11, for the mirrored flag set).
- Produces: `newAgentServiceCommand() *cobra.Command`, called once from `newAgentCommand` in `agent.go`. Package-level `var svcmgrNewManager = svcmgr.NewManager` (test injection seam, following the `updateNewUpdater` pattern).

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/agent_service_test.go
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

// fakeManager stubs svcmgr.Manager for command-layer tests.
type fakeManager struct {
	installedSpecs []svcmgr.Spec
	installErr     error
	uninstallErr   error
	startErr       error
	stopErr        error
	status         svcmgr.Status
	statusErr      error
}

func (f *fakeManager) Install(spec svcmgr.Spec) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installedSpecs = append(f.installedSpecs, spec)
	return nil
}
func (f *fakeManager) Uninstall(string) error         { return f.uninstallErr }
func (f *fakeManager) Start(string) error              { return f.startErr }
func (f *fakeManager) Stop(string) error                { return f.stopErr }
func (f *fakeManager) Status(string) (svcmgr.Status, error) { return f.status, f.statusErr }

func withFakeSvcManager(t *testing.T, m *fakeManager) {
	t.Helper()
	orig := svcmgrNewManager
	svcmgrNewManager = func(svcmgr.ManagerOptions) (svcmgr.Manager, error) { return m, nil }
	t.Cleanup(func() { svcmgrNewManager = orig })
}

func withElevated(t *testing.T, elevated bool) {
	t.Helper()
	orig := isElevatedFn
	isElevatedFn = func() (bool, error) { return elevated, nil }
	t.Cleanup(func() { isElevatedFn = orig })
}

func newTestCmd(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}

func TestAgentServiceInstall_Elevated_WritesConfigAndInstalls(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".boxy-agent")

	var out bytes.Buffer
	err := runAgentServiceInstall(newTestCmd(&out), agentServiceInstallOpts{
		userMode: false,
		agentOpts: agentServeOpts{
			server:    "boxy-server:9091",
			providers: []string{"docker"},
			dataDir:   dataDir,
		},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 {
		t.Fatalf("expected exactly one Install call, got %d", len(m.installedSpecs))
	}
	spec := m.installedSpecs[0]
	if spec.Name != "boxy-agent" {
		t.Fatalf("Spec.Name = %q, want boxy-agent", spec.Name)
	}

	cfgPath := filepath.Join(dataDir, "service.yaml")
	cfg, err := loadAgentServiceConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if cfg.Server != "boxy-server:9091" || !filepath.IsAbs(cfg.DataDir) {
		t.Fatalf("saved config = %+v, unexpected", cfg)
	}
}

func TestAgentServiceInstall_NotElevated_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		userMode:  false,
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected an error when not elevated and --user was not passed")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when the elevation check fails")
	}
}

func TestAgentServiceInstall_UserMode_SkipsElevationCheck(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		userMode:  true,
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("runAgentServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 {
		t.Fatal("expected Install to be called in --user mode without elevation")
	}
}

func TestAgentServiceInstall_AlreadyInstalled_SurfacesClearError(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{installErr: svcmgr.ErrAlreadyInstalled}
	withFakeSvcManager(t, m)

	err := runAgentServiceInstall(newTestCmd(&bytes.Buffer{}), agentServiceInstallOpts{
		agentOpts: agentServeOpts{server: "s:9091", providers: []string{"docker"}, dataDir: t.TempDir()},
	})
	if !errors.Is(err, svcmgr.ErrAlreadyInstalled) {
		t.Fatalf("error = %v, want to wrap svcmgr.ErrAlreadyInstalled", err)
	}
}

func TestAgentServiceUninstall_NotPurge_KeepsDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	if err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), false, dataDir); err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir should still exist: %v", err)
	}
}

func TestAgentServiceUninstall_Purge_RemovesDataDir(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true}}
	withFakeSvcManager(t, m)

	dataDir := t.TempDir()
	if err := runAgentServiceUninstall(newTestCmd(&bytes.Buffer{}), true, dataDir); err != nil {
		t.Fatalf("runAgentServiceUninstall: %v", err)
	}
	if _, err := os.Stat(dataDir); err == nil {
		t.Fatal("data dir should have been removed by --purge")
	}
}

func TestAgentServiceStatus_PrintsInstalledState(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: true, Running: true, Mode: "system-service"}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runAgentServiceStatus(newTestCmd(&out)); err != nil {
		t.Fatalf("runAgentServiceStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "running") || !strings.Contains(got, "system-service") {
		t.Fatalf("status output = %q, expected to mention running and system-service", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `task test:cli`
Expected: FAIL — `runAgentServiceInstall`, `agentServiceInstallOpts`, `svcmgrNewManager`, `isElevatedFn`, `runAgentServiceUninstall`, `runAgentServiceStatus` undefined.

- [ ] **Step 3: Write the implementation**

First, expose `isElevated` as an overridable var (Task 9 defined it as a plain function — add this thin indirection here rather than modifying Task 9's files, since only the command layer needs test injection):

```go
// internal/cli/agent_service.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

// isElevatedFn is isElevated (Task 9) behind a package-level var so tests
// can fake the current process's privilege level.
var isElevatedFn = isElevated

// svcmgrNewManager is svcmgr.NewManager behind a package-level var so
// tests can fake the underlying OS service manager, following the same
// injectable-factory pattern as updateNewUpdater in update.go.
var svcmgrNewManager = svcmgr.NewManager

const agentServiceName = "boxy-agent"

type agentServiceInstallOpts struct {
	userMode  bool
	agentOpts agentServeOpts
}

func newAgentServiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install, uninstall, start, stop, or check the status of boxy agent as an OS-managed background service",
	}
	cmd.AddCommand(newAgentServiceInstallCommand())
	cmd.AddCommand(newAgentServiceUninstallCommand())
	cmd.AddCommand(newAgentServiceStartCommand())
	cmd.AddCommand(newAgentServiceStopCommand())
	cmd.AddCommand(newAgentServiceStatusCommand())
	return cmd
}

func newAgentServiceInstallCommand() *cobra.Command {
	var opts agentServiceInstallOpts

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install boxy agent as a background service (real service by default; --user for an unprivileged fallback)",
		Long: `Installs boxy agent as an OS-managed background service so it starts
automatically and survives logout/reboot.

By default this registers a real service (Windows Service via SCM, Linux
systemd system unit) and requires an elevated process (Administrator /
root). Pass --user to install the unprivileged fallback instead (Windows
Task Scheduler at-logon task, Linux systemd user unit) — note this starts
at user logon, not at machine boot before any login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceInstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "install the unprivileged fallback (no admin/root required) instead of a real service")
	cmd.Flags().StringVar(&opts.agentOpts.server, "server", "", "boxy server gRPC address (host:port), required")
	cmd.Flags().StringSliceVar(&opts.agentOpts.providers, "providers", nil, "provider types this agent hosts (e.g. docker,hyperv), required")
	cmd.Flags().StringVar(&opts.agentOpts.token, "token", "", "single-use registration token (first connection only)")
	cmd.Flags().StringVar(&opts.agentOpts.name, "name", "", "human-readable agent name (default: hostname)")
	cmd.Flags().StringVar(&opts.agentOpts.caCert, "ca-cert", "", "path to the server's CA certificate, required for the first (token) connection unless --insecure")
	cmd.Flags().StringVar(&opts.agentOpts.dataDir, "data-dir", "", "directory for the agent's issued credentials (default .boxy-agent in cwd)")
	cmd.Flags().BoolVar(&opts.agentOpts.insecure, "insecure", false, "connect without TLS (local development only)")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("providers")
	return cmd
}

func runAgentServiceInstall(cmd *cobra.Command, opts agentServiceInstallOpts) error {
	if !opts.userMode {
		elevated, err := isElevatedFn()
		if err != nil {
			return fmt.Errorf("check process privilege: %w", err)
		}
		if !elevated {
			return fmt.Errorf("installing a real boxy-agent service requires an elevated process (run as Administrator, or as root/sudo) — pass --user to install the unprivileged fallback instead")
		}
	}

	dataDir := opts.agentOpts.dataDir
	if dataDir == "" {
		wd, err := effectiveWD()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		dataDir = filepath.Join(wd, ".boxy-agent")
	}
	absDataDir, err := resolveAbs(dataDir)
	if err != nil {
		return err
	}
	absCACert, err := resolveAbs(opts.agentOpts.caCert)
	if err != nil {
		return err
	}
	logFile := filepath.Join(absDataDir, "service.log")

	cfg := agentServiceConfig{
		Server:    opts.agentOpts.server,
		Providers: stringsOf(opts.agentOpts.providers),
		Token:     opts.agentOpts.token,
		Name:      opts.agentOpts.name,
		CACert:    absCACert,
		DataDir:   absDataDir,
		Insecure:  opts.agentOpts.insecure,
		LogFile:   logFile,
	}
	cfgPath := filepath.Join(absDataDir, "service.yaml")
	if err := saveAgentServiceConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("write service config: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate boxy executable: %w", err)
	}

	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: opts.userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	spec := svcmgr.Spec{
		Name:        agentServiceName,
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent — dials a boxy server and executes provider operations",
		ExecPath:    exePath,
		Args:        []string{"agent", "serve", "--service-config", cfgPath},
	}
	if err := mgr.Install(spec); err != nil {
		return fmt.Errorf("install %s service: %w", agentServiceName, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-agent installed and started (config: %s, log: %s)\n", cfgPath, logFile)
	return nil
}

// stringsOf normalizes a cobra StringSlice flag value into a plain
// []string with no nil-vs-empty ambiguity for YAML round-tripping.
func stringsOf(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := make([]string, len(ss))
	copy(out, ss)
	return out
}

func newAgentServiceUninstallCommand() *cobra.Command {
	var purge bool
	var dataDir string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedDataDir := dataDir
			if resolvedDataDir == "" {
				wd, err := effectiveWD()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
				resolvedDataDir = filepath.Join(wd, ".boxy-agent")
			}
			return runAgentServiceUninstall(cmd, purge, resolvedDataDir)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the agent's data directory (credentials, state)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "the agent's data directory, to locate it for --purge (default .boxy-agent in cwd)")
	return cmd
}

func runAgentServiceUninstall(cmd *cobra.Command, purge bool, dataDir string) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	if err := mgr.Uninstall(agentServiceName); err != nil {
		return fmt.Errorf("uninstall %s service: %w", agentServiceName, err)
	}
	if purge {
		if err := os.RemoveAll(dataDir); err != nil {
			return fmt.Errorf("remove data directory %q: %w", dataDir, err)
		}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-agent service uninstalled\n")
	return nil
}

func newAgentServiceStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Start(agentServiceName); err != nil {
				return fmt.Errorf("start %s service: %w", agentServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-agent service started")
			return nil
		},
	}
}

func newAgentServiceStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed boxy agent service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Stop(agentServiceName); err != nil {
				return fmt.Errorf("stop %s service: %w", agentServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-agent service stopped")
			return nil
		},
	}
}

func newAgentServiceStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the installed boxy agent service's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServiceStatus(cmd)
		},
	}
}

func runAgentServiceStatus(cmd *cobra.Command) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	st, err := mgr.Status(agentServiceName)
	if err != nil {
		return fmt.Errorf("get %s service status: %w", agentServiceName, err)
	}
	if !st.Installed {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "boxy-agent: not installed")
		return nil
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "boxy-agent: %s (%s)\n", state, st.Mode)
	return nil
}
```

Now wire it into `agent.go`:

```go
	cmd.AddCommand(newAgentServeCommand())
	cmd.AddCommand(newAgentServiceCommand())
```

(Add immediately after the existing `newAgentServeCommand()` registration in `newAgentCommand`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `task test:cli`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/agent_service.go internal/cli/agent.go internal/cli/agent_service_test.go
git commit -m "feat(cli): boxy agent service install/uninstall/start/stop/status" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 14: `boxy serve service` command tree

**Files:**
- Create: `internal/cli/serve_service.go`
- Modify: `internal/cli/root.go` (register the new subcommand)
- Test: `internal/cli/serve_service_test.go`

**Interfaces:**
- Consumes: same `svcmgrNewManager`, `isElevatedFn` vars declared in Task 13 (this task must not redeclare them — `internal/cli` is one package, so `agent_service.go`'s package-level vars are visible here directly).
- Produces: `newServeServiceCommand() *cobra.Command`, registered once from `root.go`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/serve_service_test.go
package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/svcmgr"
)

func TestServeServiceInstall_Elevated_WritesConfigAndInstalls(t *testing.T) {
	withElevated(t, true)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	dir := t.TempDir()
	boxyDir := filepath.Join(dir, ".boxy")

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{
		userMode: false,
		serveOpts: serveOpts{
			configPath: filepath.Join(dir, "boxy.yaml"),
			listen:     ":9090",
			ui:         true,
		},
		boxyDir: boxyDir,
	})
	if err != nil {
		t.Fatalf("runServeServiceInstall: %v", err)
	}
	if len(m.installedSpecs) != 1 || m.installedSpecs[0].Name != "boxy-serve" {
		t.Fatalf("installedSpecs = %+v, want one Spec named boxy-serve", m.installedSpecs)
	}

	cfg, err := loadServeServiceConfig(filepath.Join(boxyDir, "service.yaml"))
	if err != nil {
		t.Fatalf("loadServeServiceConfig: %v", err)
	}
	if cfg.Listen != ":9090" || !cfg.UI {
		t.Fatalf("saved config = %+v, unexpected", cfg)
	}
}

func TestServeServiceInstall_NotElevated_ErrorsWithoutInstalling(t *testing.T) {
	withElevated(t, false)
	m := &fakeManager{}
	withFakeSvcManager(t, m)

	err := runServeServiceInstall(newTestCmd(&bytes.Buffer{}), serveServiceInstallOpts{boxyDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when not elevated and --user was not passed")
	}
	if len(m.installedSpecs) != 0 {
		t.Fatal("must not call Install when the elevation check fails")
	}
}

func TestServeServiceStatus_ReportsNotInstalled(t *testing.T) {
	m := &fakeManager{status: svcmgr.Status{Installed: false}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	if err := runServeServiceStatus(newTestCmd(&out)); err != nil {
		t.Fatalf("runServeServiceStatus: %v", err)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Fatalf("status output = %q, expected to mention not installed", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `task test:cli`
Expected: FAIL — `runServeServiceInstall`, `serveServiceInstallOpts`, `runServeServiceStatus` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/cli/serve_service.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/spf13/cobra"
)

const serveServiceName = "boxy-serve"

type serveServiceInstallOpts struct {
	userMode  bool
	serveOpts serveOpts
	// boxyDir overrides where service.yaml/service.log are written;
	// empty means the real default (resolved the same way serveStatePath
	// resolves .boxy/, so the service config sits next to state.json).
	boxyDir string
}

func newServeServiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install, uninstall, start, stop, or check the status of boxy serve as an OS-managed background service",
	}
	cmd.AddCommand(newServeServiceInstallCommand())
	cmd.AddCommand(newServeServiceUninstallCommand())
	cmd.AddCommand(newServeServiceStartCommand())
	cmd.AddCommand(newServeServiceStopCommand())
	cmd.AddCommand(newServeServiceStatusCommand())
	return cmd
}

func newServeServiceInstallCommand() *cobra.Command {
	var opts serveServiceInstallOpts

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install boxy serve as a background service (real service by default; --user for an unprivileged fallback)",
		Long: `Installs boxy serve as an OS-managed background service so it starts
automatically and survives logout/reboot.

By default this registers a real service (Windows Service via SCM, Linux
systemd system unit) and requires an elevated process (Administrator /
root). Pass --user to install the unprivileged fallback instead (Windows
Task Scheduler at-logon task, Linux systemd user unit) — note this starts
at user logon, not at machine boot before any login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeServiceInstall(cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.userMode, "user", false, "install the unprivileged fallback (no admin/root required) instead of a real service")
	cmd.Flags().StringVar(&opts.serveOpts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.serveOpts.listen, "listen", "", "HTTP listen address (default :9090)")
	cmd.Flags().BoolVar(&opts.serveOpts.ui, "ui", true, "enable web dashboard UI")
	cmd.Flags().StringVar(&opts.serveOpts.grpcListen, "grpc-listen", "", "agent gRPC listen address (default :9091)")
	cmd.Flags().StringArrayVar(&opts.serveOpts.grpcCertSANs, "grpc-cert-san", nil, "extra DNS name or IP to include in the agent gRPC server certificate SANs (repeatable)")
	cmd.Flags().BoolVar(&opts.serveOpts.insecure, "insecure", false, "serve agent gRPC without TLS/mTLS (local development only)")
	return cmd
}

func runServeServiceInstall(cmd *cobra.Command, opts serveServiceInstallOpts) error {
	if !opts.userMode {
		elevated, err := isElevatedFn()
		if err != nil {
			return fmt.Errorf("check process privilege: %w", err)
		}
		if !elevated {
			return fmt.Errorf("installing a real boxy-serve service requires an elevated process (run as Administrator, or as root/sudo) — pass --user to install the unprivileged fallback instead")
		}
	}

	boxyDir := opts.boxyDir
	if boxyDir == "" {
		statePath, err := serveStatePath(opts.serveOpts.configPath)
		if err != nil {
			return err
		}
		boxyDir = filepath.Dir(statePath)
	}
	absConfigPath, err := resolveAbs(opts.serveOpts.configPath)
	if err != nil {
		return err
	}
	logFile := filepath.Join(boxyDir, "service.log")

	cfg := serveServiceConfig{
		ConfigPath:   absConfigPath,
		Listen:       opts.serveOpts.listen,
		UI:           opts.serveOpts.ui,
		GRPCListen:   opts.serveOpts.grpcListen,
		GRPCCertSANs: stringsOf(opts.serveOpts.grpcCertSANs),
		Insecure:     opts.serveOpts.insecure,
		LogFile:      logFile,
	}
	cfgPath := filepath.Join(boxyDir, "service.yaml")
	if err := saveServeServiceConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("write service config: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate boxy executable: %w", err)
	}

	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: opts.userMode})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	spec := svcmgr.Spec{
		Name:        serveServiceName,
		DisplayName: "Boxy Server",
		Description: "Boxy daemon — API server, reconcile loop, and embedded agent",
		ExecPath:    exePath,
		Args:        []string{"serve", "--service-config", cfgPath},
	}
	if err := mgr.Install(spec); err != nil {
		return fmt.Errorf("install %s service: %w", serveServiceName, err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy-serve installed and started (config: %s, log: %s)\n", cfgPath, logFile)
	return nil
}

func newServeServiceUninstallCommand() *cobra.Command {
	var purge bool
	var boxyDir string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedBoxyDir := boxyDir
			if resolvedBoxyDir == "" {
				statePath, err := serveStatePath("")
				if err != nil {
					return err
				}
				resolvedBoxyDir = filepath.Dir(statePath)
			}
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Uninstall(serveServiceName); err != nil {
				return fmt.Errorf("uninstall %s service: %w", serveServiceName, err)
			}
			if purge {
				if err := os.RemoveAll(resolvedBoxyDir); err != nil {
					return fmt.Errorf("remove state directory %q: %w", resolvedBoxyDir, err)
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service uninstalled")
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove boxy serve's state directory (.boxy/)")
	cmd.Flags().StringVar(&boxyDir, "boxy-dir", "", "the .boxy/ state directory, to locate it for --purge (default resolved the same way `boxy serve` resolves it)")
	return cmd
}

func newServeServiceStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Start(serveServiceName); err != nil {
				return fmt.Errorf("start %s service: %w", serveServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service started")
			return nil
		},
	}
}

func newServeServiceStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the installed boxy serve service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
			if err != nil {
				return fmt.Errorf("create service manager: %w", err)
			}
			if err := mgr.Stop(serveServiceName); err != nil {
				return fmt.Errorf("stop %s service: %w", serveServiceName, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "✓ boxy-serve service stopped")
			return nil
		},
	}
}

func newServeServiceStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the installed boxy serve service's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeServiceStatus(cmd)
		},
	}
}

func runServeServiceStatus(cmd *cobra.Command) error {
	mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{})
	if err != nil {
		return fmt.Errorf("create service manager: %w", err)
	}
	st, err := mgr.Status(serveServiceName)
	if err != nil {
		return fmt.Errorf("get %s service status: %w", serveServiceName, err)
	}
	if !st.Installed {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "boxy-serve: not installed")
		return nil
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "boxy-serve: %s (%s)\n", state, st.Mode)
	return nil
}
```

Now wire it into `root.go`:

```go
	root.AddCommand(newServeCommand())
	root.AddCommand(newServeServiceCommand())
```

Wait — `newServeServiceCommand` is a top-level `service` subcommand of `serve`, i.e. it must be registered on the command returned by `newServeCommand()`, not on `root` directly (so the CLI reads `boxy serve service install`, matching the spec, not `boxy serve-service install`). Register it inside `newServeCommand` in `serve.go` instead:

```go
	cmd.AddCommand(newServeServiceCommand())
```

(Add this line at the end of `newServeCommand`, just before `return cmd`, in `internal/cli/serve.go` — **not** in `root.go`. Do not add a `root.AddCommand(newServeServiceCommand())` line.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `task test:cli`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/serve_service.go internal/cli/serve.go internal/cli/serve_service_test.go
git commit -m "feat(cli): boxy serve service install/uninstall/start/stop/status" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 15: Full-tree cobra wiring check + `task lint` clean pass

**Files:**
- No new files. This task verifies Tasks 1–14 compose correctly as a whole and satisfies `.golangci.yml`.

- [ ] **Step 1: Verify the command tree end-to-end**

Run:
```bash
go run ./cmd/boxy agent service --help
go run ./cmd/boxy agent service install --help
go run ./cmd/boxy serve service --help
go run ./cmd/boxy serve service install --help
```
Expected: each prints the expected `Use`/`Short`/flags with no cobra wiring errors (e.g. no duplicate command names, no missing `RunE`).

- [ ] **Step 2: Run the full test suite**

Run: `task test`
Expected: PASS (Linux-only files like `manager_linux.go`/`manager_linux_test.go` are simply not compiled on this Windows dev machine — that's expected here; Task 17 adds the CI coverage for them).

- [ ] **Step 3: Run lint**

Run: `task lint` (or `golangci-lint run ./...` if no such Taskfile task exists — check `Taskfile.yml` first and use whichever is present)
Expected: no new findings. Common issues to pre-empt given the code above: unused imports/vars left over from any placeholder cleanup missed in earlier steps (re-check every file edited in Tasks 1–14 for a stray `var _ = ...` placeholder line — none should remain), and `errcheck`-flagged unchecked errors on `fmt.Fprintln`/`fmt.Fprintf` to `cmd.OutOrStdout()` (already `_ = `-prefixed above, matching the rest of `internal/cli`'s convention).

- [ ] **Step 4: Fix anything lint or the manual `--help` check surfaces, then commit**

```bash
git add -A
git commit -m "chore: fix lint findings from service-install command wiring" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

(Skip the commit if Steps 1–3 found nothing to fix.)

---

### Task 16: Documentation — `docs/service-install.md`, `docs/install.md` pointer, bundled skill drift

**Files:**
- Create: `docs/service-install.md`
- Modify: `docs/install.md`
- Modify: `internal/skills/assets/boxy-cli/SKILL.md`

**Interfaces:** None — documentation only. This task exists because `internal/skills/drift_test.go`'s `TestBundledSkillMentionsAllCommands` (run via `task skills:check`) fails once new top-level command tokens (`service`) appear in the cobra tree without a corresponding mention in the bundled skill docs.

- [ ] **Step 1: Confirm the drift test currently fails after Tasks 13–14**

Run: `task skills:check`
Expected: FAIL — `TestBundledSkillMentionsAllCommands` reports the bundled skill content is missing the `service` token.

- [ ] **Step 2: Write `docs/service-install.md`**

```markdown
# Install boxy agent / boxy serve as a background service

`boxy agent service` and `boxy serve service` install boxy as an
OS-managed background process that starts automatically and survives
logout/reboot — no terminal needs to stay open.

## Platforms

- **Windows**: a real Windows Service (via the Service Control Manager)
  by default, or a Task Scheduler at-logon task with `--user`.
- **Linux**: a systemd system unit by default, or a systemd `--user` unit
  with `--user`.
- **macOS**: not supported yet. `service install` fails with a clear
  error rather than silently doing nothing. Tracked as a follow-up issue.

## Privileged vs. `--user`

By default, `install` registers the real service and requires an elevated
process:

- Windows: run from an Administrator PowerShell/terminal.
- Linux: run with `sudo`.

Pass `--user` to install the unprivileged fallback instead — no admin/root
needed:

- Windows: a Task Scheduler task triggered at your next logon. This is a
  real behavioral difference from a true service: it starts at **user
  logon**, not at machine boot before any login.
- Linux: a systemd `--user` unit, plus `loginctl enable-linger` so it
  keeps running (and starts at boot) without an active login session.
  Some hardened/managed hosts restrict `enable-linger` via polkit — if
  that happens, `install` still succeeds but prints a note; retry
  `loginctl enable-linger <user>` manually once permitted.

## Usage

```bash
# Agent — real service, requires elevation
boxy agent service install --server boxy-server:9091 --providers docker --token <token> --ca-cert ca.crt

# Agent — unprivileged fallback
boxy agent service install --user --server boxy-server:9091 --providers docker --token <token> --ca-cert ca.crt

boxy agent service status
boxy agent service stop
boxy agent service start
boxy agent service uninstall           # keeps credentials/state
boxy agent service uninstall --purge   # also removes the data directory

# Server — same shape
boxy serve service install --listen :9090
boxy serve service status
boxy serve service uninstall
```

## What gets written

`install` resolves every path it's given (`--data-dir`, `--config`,
`--ca-cert`) to an absolute path and writes a small `service.yaml` file
next to the process's existing state directory (`.boxy-agent/` for the
agent, `.boxy/` for `serve`) — a running service has no predictable
working directory, so nothing in the installed invocation depends on cwd.

The agent's `service.yaml` briefly holds the single-use bootstrap
`--token`: encrypted at rest with DPAPI (machine-scope) on Windows, and
protected by `0600` file permissions on Linux. Once the agent registers
successfully, the token is scrubbed from the file — it's single-use and
worthless after that point regardless.

## Single instance per host

v1 supports exactly one installed agent and one installed server per
host, under the fixed names `boxy-agent` and `boxy-serve`. `install`
errors if one is already registered rather than creating a second one.
Multi-instance support is tracked as a follow-up issue.
```

- [ ] **Step 3: Add a pointer from `docs/install.md`**

```markdown
## Run as a background service

To have `boxy agent serve` or `boxy serve` start automatically and
survive reboot/logout instead of running in a foreground terminal, see
[Install boxy agent / boxy serve as a background service](service-install.md).
```

(Insert this section immediately before the `## Verify` section, near the end of `docs/install.md`.)

- [ ] **Step 4: Update `internal/skills/assets/boxy-cli/SKILL.md`**

Add new bullet points in the "Command Discovery" list (near the existing `boxy agent serve` / `boxy update` bullets around lines 26–41):

```markdown
- `boxy agent service install`
- `boxy agent service uninstall`
- `boxy agent service start`
- `boxy agent service stop`
- `boxy agent service status`
- `boxy serve service install`
- `boxy serve service uninstall`
- `boxy serve service start`
- `boxy serve service stop`
- `boxy serve service status`
```

And a short explanatory line near the existing `boxy agent serve ...` line (around line 54):

```markdown
- `boxy agent service install --server <host:port> --providers <list> --token <token> --ca-cert <path>` installs the agent as a background service instead of a foreground process — a real service by default (requires an elevated process), or `--user` for an unprivileged Task Scheduler/systemd-user fallback. `boxy serve service install` does the same for the daemon. See docs/service-install.md.
```

- [ ] **Step 5: Run the drift check to verify it passes**

Run: `task skills:check`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add docs/service-install.md docs/install.md internal/skills/assets/boxy-cli/SKILL.md
git commit -m "docs: document boxy agent/serve service install" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 17: CI — add `windows-latest` to the `test` job matrix

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:** None — CI config only. Without this, every `_windows.go` file added in Tasks 4, 5, 6, 7 (SCM path), 8, and `internal/cli/privilege_windows.go` compiles and runs only on this developer's local Windows machine, never in CI — the `test` job today runs on `ubuntu-latest` only (confirmed by reading `.github/workflows/ci.yml` during planning).

- [ ] **Step 1: Modify the `test` job to a matrix, mirroring `installer-smoke`'s existing pattern**

```yaml
  test:
    name: Test (${{ matrix.os }})
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod

      - name: Run tests
        run: go test -race -short ./...

      # debug_provider*.go (internal/cli) is only compiled with -tags devtools
      # (see #68); run it too so that code path still has CI coverage.
      - name: Run tests (devtools build tag)
        run: go test -race -short -tags devtools ./internal/cli/...
```

(This replaces the existing `test:` job block's `runs-on: ubuntu-latest` line and adds the `strategy`/`matrix` block and `${{ matrix.os }}` name, exactly matching the pattern the file already uses for `installer-smoke` a few jobs down. The two `Run tests` / `Run tests (devtools build tag)` steps are unchanged.)

- [ ] **Step 2: Verify the YAML is well-formed**

Run: `task lint` if it includes a yamllint step (per this repo's go-devops-configured pipeline), otherwise validate manually that the job block parses (e.g. via a YAML linter or `python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))"` if Python is available; otherwise visually diff against the existing `installer-smoke` job's structure, which uses the identical matrix shape).
Expected: valid YAML, structurally identical in shape to the existing `installer-smoke` job.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run the test job on windows-latest too, covering svcmgr's Windows backends" -m "" -m "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

(This change only takes effect once pushed and run on GitHub Actions — there's no local way to fully verify the new matrix leg here beyond YAML validity. Push this branch and confirm both `Test (ubuntu-latest)` and `Test (windows-latest)` pass in the Actions run before considering this task done.)

---

### Task 18: File follow-up GitHub issues

**Files:** None — GitHub issues only, via `gh issue create`.

- [ ] **Step 1: File the macOS/launchd follow-up**

```bash
gh issue create \
  --title "Add macOS/launchd support to boxy agent/serve service install" \
  --body "Follow-up to the Windows (SCM + Task Scheduler) / Linux (systemd) service-install feature — see docs/superpowers/specs/2026-08-10-service-install-design.md and docs/service-install.md. \`boxy agent service install\` / \`boxy serve service install\` currently return an explicit unsupported error on darwin (internal/svcmgr/manager_darwin.go) rather than installing anything. This issue tracks adding a real launchd-backed svcmgr.Manager implementation (privileged: a LaunchDaemon under /Library/LaunchDaemons; --user fallback: a LaunchAgent under ~/Library/LaunchAgents) so macOS gets the same install/uninstall/start/stop/status support as Windows and Linux." \
  --label feature
```

- [ ] **Step 2: File the multi-instance follow-up**

```bash
gh issue create \
  --title "Support named multi-instance boxy agent service installs on one host" \
  --body "Follow-up to the service-install feature — see docs/superpowers/specs/2026-08-10-service-install-design.md. v1 assumes a single agent/serve instance per host under the fixed service names boxy-agent/boxy-serve; install errors if one is already registered. This issue tracks an --instance-name flag (or similar) producing distinctly named services (e.g. boxy-agent-<name>), with uninstall/start/stop/status all taking the instance name too — useful for e.g. testing two provider configs side by side on one host." \
  --label feature
```

- [ ] **Step 3: File the `boxy update` service-restart follow-up**

```bash
gh issue create \
  --title "boxy update should detect and restart an installed agent/serve service" \
  --body "Follow-up to the service-install feature — see docs/superpowers/specs/2026-08-10-service-install-design.md. Today, boxy update replaces the binary in place but does not know whether boxy-agent/boxy-serve is installed as a service, so an installed service keeps running the old binary in memory until manually restarted (boxy agent service stop && boxy agent service start, or the serve equivalent). This issue tracks having boxy update check svcmgr.Manager.Status for both service names and, if installed, restart the service after a successful binary update." \
  --label feature
```

- [ ] **Step 4: Record the issue numbers**

No commit needed for this step — just note the three issue numbers `gh issue create` prints, for cross-referencing from the PR description when this plan's work is shipped.

---

### Task 19: Final integration pass

**Files:** None new — this task is verification only, closing the loop on every Global Constraint above.

- [ ] **Step 1: Full test + lint pass**

Run: `task test` and `task lint`
Expected: both clean.

- [ ] **Step 2: Manual end-to-end smoke test on this (Windows) dev machine**

```bash
go build -o ./boxy.exe ./cmd/boxy
./boxy.exe agent service install --user --server 127.0.0.1:9091 --providers docker --insecure
./boxy.exe agent service status
./boxy.exe agent service stop
./boxy.exe agent service start
./boxy.exe agent service uninstall
```

Expected: each step succeeds and prints the expected confirmation message; `status` reports `running (user-task)` after install/start and `not installed` after uninstall. (Skip the elevated/SCM path here unless you're prepared to run from an Administrator prompt and clean up a real registered service afterward — the Task 5/13 unit tests already cover that path via the fake SCM.)

- [ ] **Step 3: Confirm `docs/superpowers/specs/2026-08-10-service-install-design.md`'s every section maps to a shipped task**

Cross-check: Non-goals (macOS explicit error — Task 2; multi-instance/update-restart — Task 18), Library choice (Tasks 3–5), Command surface (Tasks 13–14), Architecture/path resolution (Task 10), Persisted service config (Task 10), Secret handling (Tasks 7, 10, 11), Windows implementation (Tasks 4, 5, 6, 8), Linux implementation (Task 3), Uninstall/status (Tasks 13, 14), Logging (Tasks 11–14's `LogFile` wiring), Testing (every task's fake-based unit tests + Task 17's CI matrix), Follow-ups (Task 18).

- [ ] **Step 4: Push the branch and open a PR**

```bash
git push -u origin HEAD
gh pr create --fill
```

Confirm both `Test (ubuntu-latest)` and `Test (windows-latest)` CI legs pass on the PR before merging (per Task 17).

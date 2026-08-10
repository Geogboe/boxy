//go:build linux

package svcmgr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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

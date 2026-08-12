//go:build linux

package svcmgr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
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

// currentUsername resolves the invoking user's name for loginctl
// enable-linger. os/user.Current() is preferred because $USER is frequently
// unset or stale (e.g. under `sudo` without `-E`, or in non-login shells);
// it falls back to $USER only if the os/user lookup itself fails or returns
// an empty username, so the loginctl call still gets a best-effort value
// instead of an empty string. Overridable in tests so they don't depend on
// the real OS user of whatever machine runs them.
var currentUsername = func() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// quoteSystemdArg quotes s per systemd's unit-file command-line syntax
// (systemd.syntax(7) / systemd.service(5) "Command lines") when it contains
// a space, tab, or other syntax-significant character (an embedded double
// quote, single quote, or backslash), so that systemd's own word-splitting
// of the ExecStart= value does not silently break the argument apart or
// misinterpret an embedded quote character. Simple tokens are returned
// unquoted so rendered unit files still read like ordinary, hand-written
// ones instead of being gratuitously quoted everywhere.
func quoteSystemdArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	// Iterate bytes, not runes: Linux paths are arbitrary byte sequences
	// and only ASCII '"' and '\\' are ever escaped here, so a byte loop
	// avoids WriteRune silently mangling invalid UTF-8 into U+FFFD.
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// renderUnit builds the systemd unit file content for spec. Restart=
// on-failure with a 5s backoff is the boot-time-resilience story called
// for by the spec; the actual command line (spec.Args, already carrying
// --service-config) is what makes the unit reproduce the exact invocation
// captured at install time. Each token is passed through quoteSystemdArg
// before joining, since spec.Args elements are user-controlled paths
// (data-dir, config, ca-cert, ...) that may legitimately contain spaces —
// a naive space-join would let systemd's own ExecStart= word-splitting
// silently corrupt such values.
func renderUnit(spec Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", spec.Description)
	fmt.Fprintf(&b, "After=network-online.target\n")
	fmt.Fprintf(&b, "Wants=network-online.target\n\n")
	fmt.Fprintf(&b, "[Service]\n")
	fmt.Fprintf(&b, "Type=simple\n")
	execParts := make([]string, 0, len(spec.Args)+1)
	execParts = append(execParts, quoteSystemdArg(spec.ExecPath))
	for _, a := range spec.Args {
		execParts = append(execParts, quoteSystemdArg(a))
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(execParts, " "))
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

// Install writes the unit file and enables/starts it via systemctl. In
// user mode it also attempts `loginctl enable-linger` so the unit survives
// logout/reboot without an active session. That attempt is best-effort and
// deliberately does not fail Install: some managed hosts restrict linger
// via polkit, but by the time enable-linger runs the unit file is already
// written and systemctl enable --now has already succeeded, so the service
// genuinely is installed and running. Only linger itself doesn't work until
// the operator resolves it (e.g. by rerunning `loginctl enable-linger
// <user>` once polkit allows it); failing Install here would misreport a
// working, running service as a failed install.
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // systemd unit directories are conventionally world-readable; no secrets live here
		return fmt.Errorf("create unit directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(m.renderUnitFor(spec)), 0o644); err != nil { //nolint:gosec // systemd unit files are conventionally world-readable; secrets live in a separate 0600 service-config file
		return fmt.Errorf("write unit file %q: %w", path, err)
	}

	if out, err := runCommand("systemctl", m.systemctlArgs("daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}
	if out, err := runCommand("systemctl", m.systemctlArgs("enable", "--now", spec.Name)...); err != nil {
		return fmt.Errorf("systemctl enable --now %s: %w: %s", spec.Name, err, out)
	}

	if m.userMode {
		username := currentUsername()
		// Best-effort: a failure here does not fail Install (see doc
		// comment above). The unit is already written and running; only
		// linger (surviving logout/reboot) is affected.
		_, _ = runCommand("loginctl", "enable-linger", username)
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

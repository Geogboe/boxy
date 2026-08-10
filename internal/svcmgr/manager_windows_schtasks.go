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
	defer func() { _ = os.Remove(xmlPath.Name()) }()

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
	if err != nil || strings.TrimSpace(string(out)) == "" {
		// schtasks exits non-zero with "cannot find the file specified"
		// (or similar) when the task doesn't exist. An empty result with
		// no error is treated the same way — neither indicates a
		// registered task — rather than as a hard error.
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
		out[i*2] = byte(u)        //nolint:gosec // intentional truncation: low byte of a UTF-16LE code unit
		out[i*2+1] = byte(u >> 8) //nolint:gosec // intentional truncation: high byte of a UTF-16LE code unit
	}
	return out, nil
}

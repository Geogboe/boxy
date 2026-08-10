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

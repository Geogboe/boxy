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

	// LinuxAmbientCapabilities lists POSIX capability names (e.g.
	// "CAP_NET_ADMIN") to grant the process via systemd's
	// AmbientCapabilities= directive. The Windows manager ignores it
	// entirely (no equivalent concept). The Linux manager renders it
	// verbatim into whichever unit type it installs -- callers should
	// leave this empty for a --user install, since an unprivileged
	// systemd --user unit generally cannot be granted ambient capabilities
	// its own user session doesn't already have; #379 blocker 6's caller
	// (`agent service install`) only sets this for a system-unit install.
	//
	// Currently a no-op in practice: renderUnit emits no User=, so a
	// system-unit install runs the service as root, which already has
	// every capability -- AmbientCapabilities= only matters for a
	// non-root User=. It's declared now so the intent is on record and it
	// takes effect the moment a future change adds a dedicated
	// unprivileged User= for this unit; don't read its presence as
	// evidence the agent runs without root today.
	LinuxAmbientCapabilities []string
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

//go:build !windows

package cli

import "os"

// isElevated reports whether the current process is running as root —
// the Unix precondition for installing a system-level systemd unit.
func isElevated() (bool, error) {
	return os.Geteuid() == 0, nil
}

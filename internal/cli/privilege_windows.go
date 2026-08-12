//go:build windows

package cli

import "golang.org/x/sys/windows"

// isElevated reports whether the current process token is elevated
// (running as Administrator) — the precondition for registering a real
// Windows Service via SCM.
func isElevated() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}

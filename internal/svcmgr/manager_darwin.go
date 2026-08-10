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

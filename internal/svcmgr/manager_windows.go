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

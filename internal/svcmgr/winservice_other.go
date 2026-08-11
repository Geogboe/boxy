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

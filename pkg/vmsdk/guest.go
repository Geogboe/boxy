// Package vmsdk provides hypervisor-agnostic VM guest communication interfaces
// and implementations. Any VM provider can use these to run commands inside
// guest operating systems.
package vmsdk

import "context"

// GuestExec executes commands on a VM guest OS.
type GuestExec interface {
	Exec(ctx context.Context, cmd string, args ...string) (*ExecResult, error)
}

// ExecResult holds the output of a guest command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

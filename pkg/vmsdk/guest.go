// Package vmsdk provides hypervisor-agnostic VM guest communication interfaces
// and implementations. Any VM provider can use these to run commands inside
// guest operating systems.
package vmsdk

import (
	"context"

	"github.com/Geogboe/boxy/pkg/eventstream"
)

// GuestExec executes commands on a VM guest OS.
type GuestExec interface {
	Exec(ctx context.Context, cmd string, args ...string) (*ExecResult, error)
}

// GuestExecStreamer is an optional capability for guests that can expose
// command output before the process exits. Implementations must preserve
// stdout/stderr channel identity and return only after a terminal result is
// available; the caller owns completion-event publication.
type GuestExecStreamer interface {
	ExecStream(ctx context.Context, cmd string, args []string, sink eventstream.Sink) (*ExecResult, error)
}

// ExecResult holds the output of a guest command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

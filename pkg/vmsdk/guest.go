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

// GuestExecText is an optional provider-native path for opaque command text.
// Implementations must pass text to the guest command engine without parsing
// it into argv or rebuilding it through quoting rules.
type GuestExecText interface {
	ExecText(ctx context.Context, text string) (*ExecResult, error)
}

// GuestExecScript optionally runs trusted provider script source directly in
// the guest engine. Arguments remain data, without native-process escaping or
// interpolation into source. Implementations must fail on script errors or
// incomplete execution rather than infer success from a stale native exit code.
type GuestExecScript interface {
	ExecScript(ctx context.Context, script string, args ...string) (*ExecResult, error)
}

// GuestExecStreamer is an optional capability for guests that can expose
// command output before the process exits. Implementations must preserve
// stdout/stderr channel identity and return only after a terminal result is
// available; the caller owns completion-event publication.
type GuestExecStreamer interface {
	ExecStream(ctx context.Context, cmd string, args []string, sink eventstream.Sink) (*ExecResult, error)
}

// GuestExecStreamText is the streaming equivalent of GuestExecText.
type GuestExecStreamText interface {
	ExecStreamText(ctx context.Context, text string, sink eventstream.Sink) (*ExecResult, error)
}

// GuestSession is a GuestExec bound to one already-established connection,
// so a caller can run several commands under it before releasing the
// connection. Implementations must not reconnect between calls -- Close
// ends the underlying connection; a GuestSession is not reusable after
// Close returns.
type GuestSession interface {
	GuestExec
	Close(ctx context.Context) error
}

// GuestSessionOpener is an optional GuestExec capability for transports
// whose connection establishment is expensive enough to be worth holding
// open across multiple calls -- for example, a PSRP/WinRM runspace
// negotiation over PowerShell Direct (#361). OpenSession connects once and
// returns a GuestSession bound to that connection; the caller owns closing
// it. A caller that has several guest-exec calls to make under the same
// credential should prefer this, via a type assertion, over calling
// GuestExec.Exec repeatedly when the concrete implementation supports it --
// but implementing this interface is optional, and a plain GuestExec
// remains fully usable without it.
type GuestSessionOpener interface {
	OpenSession(ctx context.Context) (GuestSession, error)
}

// ExecResult holds the output of a guest command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

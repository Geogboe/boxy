package cli

import (
	"errors"
	"fmt"
)

// ExitCodeError reports a command that ran successfully through the Boxy
// transport but returned a non-zero status from the guest. Keeping this type
// distinct from transport and API errors lets cmd/boxy preserve the guest
// status at the process boundary.
type ExitCodeError struct {
	Code int
}

func (e *ExitCodeError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.Code)
}

// NewExitCodeError constructs the typed error returned for a non-zero guest
// command result.
func NewExitCodeError(code int) error {
	return &ExitCodeError{Code: code}
}

// ExitCode extracts a guest command exit status from err, including through
// ordinary wrapping. The second result is false for client, transport, and
// other non-guest failures.
func ExitCode(err error) (int, bool) {
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.Code, true
}

// reportedError wraps an error whose user-facing explanation has already
// been printed to stderr by the command that produced it (e.g. a friendly
// "is `boxy serve` running?" hint). It lets the top-level CLI entrypoint
// (cmd/boxy/main.go) skip printing the raw error a second time while still
// exiting non-zero.
type reportedError struct {
	err error
}

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

// MarkReported wraps err so IsReported returns true for it. Call this from a
// command's RunE after it has already written a user-facing explanation of
// err to stderr, so the caller doesn't print it again.
func MarkReported(err error) error {
	if err == nil {
		return nil
	}
	return &reportedError{err: err}
}

// IsReported reports whether err (or any error it wraps) was produced by
// MarkReported.
func IsReported(err error) bool {
	var re *reportedError
	return errors.As(err, &re)
}

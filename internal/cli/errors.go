package cli

import "errors"

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

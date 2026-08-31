package main

import (
	"errors"
	"os"
	"testing"

	"github.com/Geogboe/boxy/internal/cli"
)

func TestRunVersionCommand(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"boxy", "--version"}

	if code := run(); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
}

func TestExitStatusForError_PreservesGuestExitCode(t *testing.T) {
	t.Parallel()
	if got := exitStatusForError(&cli.ExitCodeError{Code: 23}); got != 23 {
		t.Fatalf("exitStatusForError() = %d, want 23", got)
	}
	if got := exitStatusForError(errors.New("transport failed")); got != 1 {
		t.Fatalf("exitStatusForError(transport) = %d, want 1", got)
	}
}

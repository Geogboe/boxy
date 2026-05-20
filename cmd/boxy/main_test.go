package main

import (
	"os"
	"testing"
)

func TestRunVersionCommand(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"boxy", "--version"}

	if code := run(); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()

	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	runErr := fn()
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String(), runErr
}

func TestRootVersionFlag_PrintsShortVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, GitCommit, BuildDate
	Version = "v1.2.3"
	GitCommit = "abc1234"
	BuildDate = "2026-03-20T12:00:00Z"
	t.Cleanup(func() {
		Version = oldVersion
		GitCommit = oldCommit
		BuildDate = oldDate
	})

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"--version"})

	output, err := captureStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.TrimSpace(output); got != "boxy v1.2.3" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestVersionCommand_PrintsDetailedMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, GitCommit, BuildDate
	Version = "v1.2.3"
	GitCommit = "abc1234"
	BuildDate = "2026-03-20T12:00:00Z"
	t.Cleanup(func() {
		Version = oldVersion
		GitCommit = oldCommit
		BuildDate = oldDate
	})

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"version"})

	output, err := captureStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{
		"boxy v1.2.3",
		"commit: abc1234",
		"built: 2026-03-20T12:00:00Z",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output: %q", want, output)
		}
	}
}

func TestVersionCommand_DefaultMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, GitCommit, BuildDate
	Version = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
	t.Cleanup(func() {
		Version = oldVersion
		GitCommit = oldCommit
		BuildDate = oldDate
	})

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"version"})

	output, err := captureStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{
		"boxy dev",
		"commit: unknown",
		"built: unknown",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output: %q", want, output)
		}
	}
}

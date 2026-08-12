//go:build windows

package svcmgr

import (
	"context"
	"testing"
)

func TestRunAsWindowsService_NotAService_ReturnsUnhandled(t *testing.T) {
	// go test itself is not launched by the SCM, so svc.IsWindowsService()
	// is false here — this exercises the real detection path, not a fake.
	called := false
	handled, err := RunAsWindowsService("boxy-agent-test", func(ctx context.Context) error {
		called = true
		return nil
	})
	if handled {
		t.Fatal("expected handled=false when not running as a Windows service")
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if called {
		t.Fatal("run must not be invoked when handled=false — the caller runs it itself")
	}
}

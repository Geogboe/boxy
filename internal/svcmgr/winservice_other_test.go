//go:build !windows

package svcmgr

import (
	"context"
	"testing"
)

func TestRunAsWindowsService_AlwaysUnhandledOnNonWindows(t *testing.T) {
	called := false
	handled, err := RunAsWindowsService("boxy-agent-test", func(ctx context.Context) error {
		called = true
		return nil
	})
	if handled || err != nil || called {
		t.Fatalf("handled=%v err=%v called=%v, want false/nil/false", handled, err, called)
	}
}

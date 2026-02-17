package process

import (
	"context"
	"runtime"
	"testing"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

func TestDriver_ValidateConfig_NoConfigRequired(t *testing.T) {
	t.Parallel()

	d := New()
	if err := d.ValidateConfig(context.Background(), providersdk.Instance{Name: "p1", Type: "process"}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestDriver_Exec_CapturesStdout(t *testing.T) {
	t.Parallel()

	d := New()
	inst := providersdk.Instance{Name: "p1", Type: "process"}

	var cmd []string
	if runtime.GOOS == "windows" {
		cmd = []string{"cmd", "/c", "echo hello"}
	} else {
		cmd = []string{"/bin/sh", "-lc", "echo hello"}
	}

	res, err := d.Exec(context.Background(), inst, providersdk.Target{Kind: "process", Ref: "local"}, providersdk.ExecSpec{Command: cmd})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout == "" {
		t.Fatalf("expected stdout, got empty (stderr=%q)", res.Stderr)
	}
}

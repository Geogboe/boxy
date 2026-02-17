package hooks

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeExecutor struct {
	calls []execCall
	fn    func(ctx context.Context, target Target, command []string) (ExecResult, error)
}

type execCall struct {
	target  Target
	command []string
}

func (f *fakeExecutor) Exec(ctx context.Context, target Target, command []string) (ExecResult, error) {
	f.calls = append(f.calls, execCall{target: target, command: append([]string(nil), command...)})
	return f.fn(ctx, target, command)
}

func TestRunner_Run_ShellCommand_DefaultShell(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{
		fn: func(ctx context.Context, target Target, command []string) (ExecResult, error) {
			return ExecResult{Stdout: "ok"}, nil
		},
	}

	r := &Runner{Exec: exec}
	target := Target{Kind: "docker.container", Ref: "abc123"}
	hooks := []Hook{{Name: "hello", ShellCommand: "echo hi"}}

	results, err := r.Run(context.Background(), EventCreate, target, hooks)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d, want 1", len(results))
	}
	if got := results[0].Command; len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-lc" || got[2] != "echo hi" {
		t.Fatalf("command=%v, want [/bin/sh -lc echo hi]", got)
	}
}

func TestRunner_Run_Retries_FailThenSucceed(t *testing.T) {
	t.Parallel()

	var n int
	exec := &fakeExecutor{
		fn: func(ctx context.Context, target Target, command []string) (ExecResult, error) {
			n++
			if n == 1 {
				return ExecResult{}, errors.New("boom")
			}
			return ExecResult{Stdout: "ok"}, nil
		},
	}

	r := &Runner{Exec: exec}
	target := Target{Kind: "docker.container", Ref: "abc123"}
	hooks := []Hook{{Name: "maybe", Command: []string{"do"}, Retries: 1}}

	results, err := r.Run(context.Background(), EventCreate, target, hooks)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results len=%d, want 2 attempts", len(results))
	}
	if results[0].Err == nil || results[1].Err != nil {
		t.Fatalf("errs = (%v, %v), want (non-nil, nil)", results[0].Err, results[1].Err)
	}
}

func TestRunner_Run_Retries_Exhausted(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{
		fn: func(ctx context.Context, target Target, command []string) (ExecResult, error) {
			return ExecResult{}, errors.New("nope")
		},
	}

	r := &Runner{Exec: exec}
	target := Target{Kind: "docker.container", Ref: "abc123"}
	hooks := []Hook{{Name: "always-fails", Command: []string{"do"}, Retries: 2}}

	results, err := r.Run(context.Background(), EventDestroy, target, hooks)
	if err == nil {
		t.Fatalf("Run: expected error, got nil")
	}
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run: err=%T, want *RunError", err)
	}
	if runErr.Attempt != 3 {
		t.Fatalf("attempt=%d, want 3", runErr.Attempt)
	}
	if len(results) != 3 {
		t.Fatalf("results len=%d, want 3 attempts", len(results))
	}
}

func TestRunner_Run_Timeout_DefaultApplied(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{
		fn: func(ctx context.Context, target Target, command []string) (ExecResult, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return ExecResult{}, errors.New("expected deadline")
			}
			if time.Until(deadline) <= 0 {
				return ExecResult{}, errors.New("deadline already exceeded")
			}
			return ExecResult{Stdout: "ok"}, nil
		},
	}

	r := &Runner{
		Exec: exec,
		Defaults: Defaults{
			Timeout: 50 * time.Millisecond,
		},
	}
	target := Target{Kind: "docker.container", Ref: "abc123"}
	hooks := []Hook{{Name: "t", Command: []string{"do"}}}

	if _, err := r.Run(context.Background(), EventCreate, target, hooks); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

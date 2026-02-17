package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeTransport struct {
	tasks   []Task
	reports []Result
}

func (f *fakeTransport) Poll(ctx context.Context, info Info) (Task, bool, error) {
	_ = ctx
	_ = info
	if len(f.tasks) == 0 {
		return Task{}, false, nil
	}
	t := f.tasks[0]
	f.tasks = f.tasks[1:]
	return t, true, nil
}

func (f *fakeTransport) Report(ctx context.Context, res Result) error {
	_ = ctx
	f.reports = append(f.reports, res)
	return nil
}

type fakeExecutor struct {
	stdout string
	stderr string
	err    error
}

func (f fakeExecutor) Exec(ctx context.Context, a ExecAction) (string, string, error) {
	_ = ctx
	_ = a
	return f.stdout, f.stderr, f.err
}

func TestRunner_Run_RejectsSelectorMismatch(t *testing.T) {
	t.Parallel()

	tr := &fakeTransport{
		tasks: []Task{{
			ID:       "t1",
			Selector: Selector{"os": "windows"},
			Action:   Action{Type: ActionTypeExec, Exec: &ExecAction{Command: []string{"echo", "hi"}}},
		}},
	}

	r := &Runner{
		Info:      Info{ID: "a1", Labels: map[string]string{"os": "linux"}},
		Transport: tr,
		Executor:  fakeExecutor{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = r.Run(ctx)
	if len(tr.reports) != 1 {
		t.Fatalf("reports=%d, want 1", len(tr.reports))
	}
	if tr.reports[0].Success {
		t.Fatalf("report success=true, want false")
	}
}

func TestRunner_Run_ExecSuccess(t *testing.T) {
	t.Parallel()

	tr := &fakeTransport{
		tasks: []Task{{
			ID:     "t1",
			Action: Action{Type: ActionTypeExec, Exec: &ExecAction{Command: []string{"echo", "hi"}}},
		}},
	}

	r := &Runner{
		Info:      Info{ID: "a1"},
		Transport: tr,
		Executor:  fakeExecutor{stdout: "ok"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = r.Run(ctx)
	if len(tr.reports) != 1 {
		t.Fatalf("reports=%d, want 1", len(tr.reports))
	}
	if !tr.reports[0].Success {
		t.Fatalf("report success=false, want true: %+v", tr.reports[0])
	}
	if tr.reports[0].Stdout != "ok" {
		t.Fatalf("stdout=%q, want %q", tr.reports[0].Stdout, "ok")
	}
}

func TestDefaultPolicy_RequiresExecCommand(t *testing.T) {
	t.Parallel()

	p := DefaultPolicy{}
	err := p.Allow(Info{ID: "a1"}, Task{ID: "t1", Action: Action{Type: ActionTypeExec, Exec: &ExecAction{}}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestRunner_Run_ExecFailureReported(t *testing.T) {
	t.Parallel()

	tr := &fakeTransport{
		tasks: []Task{{
			ID:     "t1",
			Action: Action{Type: ActionTypeExec, Exec: &ExecAction{Command: []string{"nope"}}},
		}},
	}

	r := &Runner{
		Info:      Info{ID: "a1"},
		Transport: tr,
		Executor:  fakeExecutor{err: errors.New("boom")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = r.Run(ctx)
	if len(tr.reports) != 1 {
		t.Fatalf("reports=%d, want 1", len(tr.reports))
	}
	if tr.reports[0].Success {
		t.Fatalf("report success=true, want false")
	}
	if tr.reports[0].Message == "" {
		t.Fatalf("expected message")
	}
}

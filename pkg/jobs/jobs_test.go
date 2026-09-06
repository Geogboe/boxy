package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunnerSubmitRecordsProgressAndCompletes(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	job, err := runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool-a"}, HandlerFuncs{
		RunFunc: func(ctx context.Context, progress Reporter) error {
			if err := progress.Record(ctx, Step{Code: "vm.create", Subject: "vm-1", Status: StepStarted, Attempt: 1}); err != nil {
				return err
			}
			return progress.Record(ctx, Step{Code: "vm.create", Subject: "vm-1", Status: StepSucceeded, Attempt: 1})
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got := waitForStatus(t, runner, job.ID, StatusSucceeded)
	if got.Kind != "pool.fill" || got.Target != "pool-a" {
		t.Fatalf("job identity = %+v", got)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", got.Steps)
	}
	if got.Steps[0].Sequence != 1 || got.Steps[1].Sequence != 2 {
		t.Fatalf("step sequences = %+v, want 1 then 2", got.Steps)
	}
}

func TestRunnerRejectsSecondActiveJobForTarget(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	release := make(chan struct{})
	first, err := runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool-a"}, HandlerFuncs{
		RunFunc: func(ctx context.Context, _ Reporter) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	waitForStatus(t, runner, first.ID, StatusRunning)

	_, err = runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool-a"}, HandlerFuncs{})
	var busy *TargetBusyError
	if !errors.As(err, &busy) || busy.ActiveJobID != first.ID {
		t.Fatalf("Submit second error = %v, want TargetBusyError for %s", err, first.ID)
	}
	close(release)
	waitForStatus(t, runner, first.ID, StatusSucceeded)
}

func TestRunnerUsesRequestedIDAndRejectsDuplicate(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	job, err := runner.Submit(context.Background(), Request{ID: "known-id", Kind: "sandbox.execute", Target: "resource:one"}, HandlerFuncs{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if job.ID != "known-id" {
		t.Fatalf("job id = %q, want known-id", job.ID)
	}
	waitForStatus(t, runner, job.ID, StatusSucceeded)
	_, err = runner.Submit(context.Background(), Request{ID: "known-id", Kind: "sandbox.execute", Target: "resource:two"}, HandlerFuncs{})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Submit error = %v, want ErrAlreadyExists", err)
	}
}

func TestRunnerCancelWaitsForCleanupBeforeCancelled(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	cleanupStarted := make(chan struct{})
	cleanupRelease := make(chan struct{})
	job, err := runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool-a"}, HandlerFuncs{
		RunFunc: func(ctx context.Context, _ Reporter) error {
			<-ctx.Done()
			return ctx.Err()
		},
		CleanupFunc: func(context.Context, Reporter) error {
			close(cleanupStarted)
			<-cleanupRelease
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitForStatus(t, runner, job.ID, StatusRunning)
	if _, err := runner.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-cleanupStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not start")
	}
	if got, err := runner.Get(context.Background(), job.ID); err != nil || got.Status != StatusCancelling {
		t.Fatalf("job while cleanup runs = %+v, err=%v, want cancelling", got, err)
	}
	close(cleanupRelease)
	waitForStatus(t, runner, job.ID, StatusCancelled)
}

func TestRunnerCleanupFailureIsNotCancelled(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	job, err := runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool-a"}, HandlerFuncs{
		RunFunc: func(ctx context.Context, _ Reporter) error {
			<-ctx.Done()
			return ctx.Err()
		},
		CleanupFunc: func(context.Context, Reporter) error { return errors.New("provider cleanup details") },
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitForStatus(t, runner, job.ID, StatusRunning)
	if _, err := runner.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got := waitForStatus(t, runner, job.ID, StatusFailed)
	if got.ErrorCode != ErrorCodeCleanupFailed {
		t.Fatalf("error code = %q, want %q", got.ErrorCode, ErrorCodeCleanupFailed)
	}
	if got.ErrorDetail != "" {
		t.Fatalf("unsafe cleanup detail persisted: %q", got.ErrorDetail)
	}
}

func TestNewRunnerMarksActiveJobsInterrupted(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	for _, job := range []Job{
		{ID: "pending", Kind: "one", Target: "a", Status: StatusPending, CreatedAt: now.Add(-time.Hour)},
		{ID: "running", Kind: "two", Target: "b", Status: StatusRunning, CreatedAt: now.Add(-time.Hour)},
		{ID: "cancelling", Kind: "three", Target: "c", Status: StatusCancelling, CreatedAt: now.Add(-time.Hour)},
		{ID: "done", Kind: "four", Target: "d", Status: StatusSucceeded, CreatedAt: now.Add(-time.Hour)},
	} {
		if err := store.Put(context.Background(), job); err != nil {
			t.Fatalf("Put(%s): %v", job.ID, err)
		}
	}
	if _, err := NewRunner(Config{Store: store, Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	for _, id := range []ID{"pending", "running", "cancelling"} {
		got, err := store.Get(context.Background(), id)
		if err != nil || got.Status != StatusInterrupted {
			t.Fatalf("job %s = %+v, err=%v, want interrupted", id, got, err)
		}
	}
	got, err := store.Get(context.Background(), "done")
	if err != nil || got.Status != StatusSucceeded {
		t.Fatalf("completed job = %+v, err=%v, want unchanged", got, err)
	}
}

func TestRunnerPruneRemovesOnlyExpiredTerminalJobs(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	oldFinished := now.Add(-15 * 24 * time.Hour)
	recentFinished := now.Add(-time.Hour)
	for _, job := range []Job{
		{ID: "old", Kind: "one", Target: "a", Status: StatusSucceeded, CreatedAt: oldFinished, FinishedAt: &oldFinished},
		{ID: "recent", Kind: "two", Target: "b", Status: StatusFailed, CreatedAt: recentFinished, FinishedAt: &recentFinished},
		{ID: "active", Kind: "three", Target: "c", Status: StatusRunning, CreatedAt: oldFinished},
	} {
		if err := store.Put(context.Background(), job); err != nil {
			t.Fatalf("Put(%s): %v", job.ID, err)
		}
	}
	runner, err := NewRunner(Config{Store: store, Retention: 14 * 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := store.Get(context.Background(), "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old job error = %v, want ErrNotFound", err)
	}
	for _, id := range []ID{"recent", "active"} {
		if _, err := store.Get(context.Background(), id); err != nil {
			t.Fatalf("job %s removed unexpectedly: %v", id, err)
		}
	}
}

func TestRunnerKeepsTargetLockedWhenTerminalStateCannotBePersisted(t *testing.T) {
	t.Parallel()
	base := NewMemoryStore()
	store := &terminalFailStore{Store: base, attempted: make(chan struct{})}
	runner, err := NewRunner(Config{Store: store})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	job, err := runner.Submit(context.Background(), Request{Kind: "pool.fill", Target: "pool:windows"}, HandlerFuncs{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-store.attempted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal persistence attempt")
	}
	_, err = runner.Submit(context.Background(), Request{Kind: "pool.retry", Target: "pool:windows"}, HandlerFuncs{})
	var busy *TargetBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second Submit error = %v, want TargetBusyError", err)
	}
	persisted, err := base.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if persisted.Status.IsTerminal() {
		t.Fatalf("persisted status = %s, want active state", persisted.Status)
	}
}

type terminalFailStore struct {
	Store
	attempted chan struct{}
}

func (s *terminalFailStore) Put(ctx context.Context, job Job) error {
	if job.Status.IsTerminal() {
		close(s.attempted)
		return errors.New("terminal write failed")
	}
	return s.Store.Put(ctx, job)
}

func waitForStatus(t *testing.T, runner *Runner, id ID, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := runner.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if job.Status == want {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s status = %s, want %s", id, job.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

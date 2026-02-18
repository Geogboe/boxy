package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Transport polls for tasks and reports results.
//
// Implementations may use gRPC, HTTP, etc.
type Transport interface {
	Poll(ctx context.Context, info Info) (Task, bool, error)
	Report(ctx context.Context, res Result) error
}

// Executor performs local actions.
type Executor interface {
	Exec(ctx context.Context, a ExecAction) (stdout string, stderr string, err error)
}

type Runner struct {
	Info      Info
	Transport Transport
	Policy    Policy
	Executor  Executor
	Logger    *slog.Logger
}

// Run continuously polls for tasks until ctx is canceled.
//
// The poll interval/backoff is delegated to the Transport; Poll should block when idle.
func (r *Runner) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("runner is nil")
	}
	if r.Transport == nil {
		return fmt.Errorf("transport is nil")
	}
	if r.Executor == nil {
		return fmt.Errorf("executor is nil")
	}

	pol := r.Policy
	if pol == nil {
		pol = DefaultPolicy{}
	}

	log := r.Logger
	if log == nil {
		log = slog.Default()
	}

	for {
		task, ok, err := r.Transport.Poll(ctx, r.Info)
		if err != nil {
			return err
		}
		if !ok {
			// Transport may return ok=false to indicate "no task right now".
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}

		start := time.Now().UTC()
		res := Result{TaskID: task.ID, StartedAt: start}

		if err := pol.Allow(r.Info, task); err != nil {
			res.Success = false
			res.Message = "rejected: " + err.Error()
			res.FinishedAt = time.Now().UTC()
			_ = r.Transport.Report(ctx, res)
			log.Warn("task rejected", "task", task.ID, "err", err)
			continue
		}

		switch task.Action.Type {
		case ActionTypeExec:
			stdout, stderr, execErr := r.Executor.Exec(ctx, *task.Action.Exec)
			res.Stdout = stdout
			res.Stderr = stderr
			if execErr != nil {
				res.Success = false
				res.Message = execErr.Error()
			} else {
				res.Success = true
			}
		default:
			res.Success = false
			res.Message = "unsupported action type"
		}

		res.FinishedAt = time.Now().UTC()
		if err := r.Transport.Report(ctx, res); err != nil {
			return err
		}
	}
}

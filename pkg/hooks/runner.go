package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// Executor runs a command against a target.
//
// Implementations may execute locally, remotely, or via provider APIs.
type Executor interface {
	Exec(ctx context.Context, target Target, command []string) (ExecResult, error)
}

type Runner struct {
	Exec     Executor
	Defaults Defaults
	Logger   *slog.Logger
}

type RunError struct {
	Event    Event
	HookName string
	Attempt  int
	Err      error
}

func (e *RunError) Error() string {
	return fmt.Sprintf("hook %q (%s) failed on attempt %d: %v", e.HookName, e.Event, e.Attempt, e.Err)
}

func (e *RunError) Unwrap() error { return e.Err }

// Run executes hooks in order. It is fail-fast: the first hook that exhausts
// retries stops the run and returns a *RunError along with all attempt results
// collected so far (including the failed attempt).
func (r *Runner) Run(ctx context.Context, event Event, target Target, hooks []Hook) ([]Result, error) {
	if r == nil {
		return nil, fmt.Errorf("hooks runner is nil")
	}
	if r.Exec == nil {
		return nil, fmt.Errorf("executor is nil")
	}
	if event == "" {
		return nil, fmt.Errorf("event is required")
	}
	if target.Ref == "" {
		return nil, fmt.Errorf("target ref is required")
	}

	log := r.Logger
	if log == nil {
		log = slog.Default()
	}

	results := make([]Result, 0, len(hooks))
	for _, h := range hooks {
		cmd, err := r.hookCommand(h)
		if err != nil {
			return results, fmt.Errorf("hook %q: %w", h.Name, err)
		}

		attempts := 1
		if h.Retries > 0 {
			attempts += h.Retries
		}

		for attempt := 1; attempt <= attempts; attempt++ {
			attemptCtx, cancel := r.hookContext(ctx, h)
			start := time.Now().UTC()
			out, execErr := r.Exec.Exec(attemptCtx, target, cmd)
			finish := time.Now().UTC()
			cancel()

			res := Result{
				Event:      event,
				Target:     target,
				HookName:   h.Name,
				Attempt:    attempt,
				Command:    slices.Clone(cmd),
				StartedAt:  start,
				FinishedAt: finish,
				Output:     out,
				Err:        execErr,
			}
			results = append(results, res)

			if execErr == nil {
				log.Info("hook succeeded", "event", event, "hook", h.Name, "attempt", attempt, "target", target.Ref)
				break
			}

			log.Warn("hook failed", "event", event, "hook", h.Name, "attempt", attempt, "target", target.Ref, "err", execErr)
			if attempt == attempts {
				return results, &RunError{
					Event:    event,
					HookName: h.Name,
					Attempt:  attempt,
					Err:      execErr,
				}
			}
		}
	}
	return results, nil
}

func (r *Runner) hookContext(parent context.Context, h Hook) (context.Context, context.CancelFunc) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = r.Defaults.Timeout
	}
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func (r *Runner) hookCommand(h Hook) ([]string, error) {
	if h.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if len(h.Command) > 0 && h.ShellCommand != "" {
		return nil, fmt.Errorf("only one of command or shell_command may be set")
	}
	if len(h.Command) > 0 {
		return slices.Clone(h.Command), nil
	}
	if h.ShellCommand == "" {
		return nil, fmt.Errorf("command is required")
	}

	shell := r.Defaults.Shell
	if len(shell) == 0 {
		shell = []string{"/bin/sh", "-lc"}
	}

	cmd := make([]string, 0, len(shell)+1)
	cmd = append(cmd, shell...)
	cmd = append(cmd, h.ShellCommand)
	return cmd, nil
}

package policycontroller

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Observer returns the current state of some managed collection/system.
type Observer[T any] interface {
	Observe(ctx context.Context) (T, error)
}

// Evaluator compares observed state to its policy and returns a decision.
//
// Policy is intentionally not modeled as a separate input: callers can embed
// policy in T, close over policy in the implementation, or compose it however
// they like.
type Evaluator[T any, P any] interface {
	Evaluate(ctx context.Context, observed T) (Decision[P], error)
}

// Actuator applies a plan produced by an Evaluator.
type Actuator[P any] interface {
	Act(ctx context.Context, plan P) error
}

// Decision is the output of an evaluation pass.
//
// If ShouldAct is false, the Controller will not call the Actuator.
type Decision[P any] struct {
	ShouldAct bool
	Plan      P
	Reason    string
}

type Result[T any, P any] struct {
	Observed T
	Decision Decision[P]

	StartedAt  time.Time
	FinishedAt time.Time
	Acted      bool
}

// Controller runs a single-pass (Reconcile) or looped (Run) policy controller.
type Controller[T any, P any] struct {
	Observer  Observer[T]
	Evaluator Evaluator[T, P]
	Actuator  Actuator[P]

	Logger *slog.Logger
}

// Reconcile performs one Observe → Decide → (optional) Act pass.
func (c *Controller[T, P]) Reconcile(ctx context.Context) (Result[T, P], error) {
	if c == nil {
		return Result[T, P]{}, fmt.Errorf("controller is nil")
	}
	if c.Observer == nil {
		return Result[T, P]{}, fmt.Errorf("observer is nil")
	}
	if c.Evaluator == nil {
		return Result[T, P]{}, fmt.Errorf("evaluator is nil")
	}

	log := c.Logger
	if log == nil {
		log = slog.Default()
	}

	start := time.Now().UTC()

	observed, err := c.Observer.Observe(ctx)
	if err != nil {
		return Result[T, P]{}, fmt.Errorf("observe: %w", err)
	}

	decision, err := c.Evaluator.Evaluate(ctx, observed)
	if err != nil {
		return Result[T, P]{}, fmt.Errorf("evaluate: %w", err)
	}

	acted := false
	if decision.ShouldAct {
		if c.Actuator == nil {
			return Result[T, P]{}, fmt.Errorf("actuator is nil (decision requires action)")
		}
		log.Info("policy decision requires action", "reason", decision.Reason)
		if err := c.Actuator.Act(ctx, decision.Plan); err != nil {
			return Result[T, P]{}, fmt.Errorf("act: %w", err)
		}
		acted = true
	} else {
		log.Info("policy decision is noop", "reason", decision.Reason)
	}

	finish := time.Now().UTC()
	return Result[T, P]{
		Observed:   observed,
		Decision:   decision,
		StartedAt:  start,
		FinishedAt: finish,
		Acted:      acted,
	}, nil
}

// Run repeatedly reconciles at the given interval until ctx is cancelled.
// It returns nil on graceful context cancellation.
func (c *Controller[T, P]) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be > 0")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := c.Reconcile(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

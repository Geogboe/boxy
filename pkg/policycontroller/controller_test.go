package policycontroller

import (
	"context"
	"errors"
	"testing"
)

type obs[T any] struct {
	v   T
	err error
}

func (o obs[T]) Observe(ctx context.Context) (T, error) {
	_ = ctx
	return o.v, o.err
}

type eval[T any, P any] struct {
	d   Decision[P]
	err error
}

func (e eval[T, P]) Evaluate(ctx context.Context, observed T) (Decision[P], error) {
	_ = ctx
	_ = observed
	return e.d, e.err
}

type act[P any] struct {
	calls int
	last  P
	err   error
}

func (a *act[P]) Act(ctx context.Context, plan P) error {
	_ = ctx
	a.calls++
	a.last = plan
	return a.err
}

func TestController_Reconcile_NoopDoesNotAct(t *testing.T) {
	t.Parallel()

	a := &act[int]{}
	c := &Controller[int, int]{
		Observer:  obs[int]{v: 1},
		Evaluator: eval[int, int]{d: Decision[int]{ShouldAct: false, Plan: 42, Reason: "ok"}},
		Actuator:  a,
	}

	res, err := c.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Acted {
		t.Fatalf("Acted=true, want false")
	}
	if a.calls != 0 {
		t.Fatalf("Act calls=%d, want 0", a.calls)
	}
}

func TestController_Reconcile_ActsWhenRequired(t *testing.T) {
	t.Parallel()

	a := &act[int]{}
	c := &Controller[int, int]{
		Observer:  obs[int]{v: 1},
		Evaluator: eval[int, int]{d: Decision[int]{ShouldAct: true, Plan: 7, Reason: "needs 7"}},
		Actuator:  a,
	}

	res, err := c.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Acted {
		t.Fatalf("Acted=false, want true")
	}
	if a.calls != 1 {
		t.Fatalf("Act calls=%d, want 1", a.calls)
	}
	if a.last != 7 {
		t.Fatalf("Act plan=%d, want 7", a.last)
	}
}

func TestController_Reconcile_ObserveError(t *testing.T) {
	t.Parallel()

	c := &Controller[int, int]{
		Observer:  obs[int]{err: errors.New("no")},
		Evaluator: eval[int, int]{},
		Actuator:  &act[int]{},
	}

	if _, err := c.Reconcile(context.Background()); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestController_Reconcile_EvaluateError(t *testing.T) {
	t.Parallel()

	c := &Controller[int, int]{
		Observer:  obs[int]{v: 1},
		Evaluator: eval[int, int]{err: errors.New("bad")},
		Actuator:  &act[int]{},
	}

	if _, err := c.Reconcile(context.Background()); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestController_Reconcile_ActError(t *testing.T) {
	t.Parallel()

	c := &Controller[int, int]{
		Observer:  obs[int]{v: 1},
		Evaluator: eval[int, int]{d: Decision[int]{ShouldAct: true, Plan: 1}},
		Actuator:  &act[int]{err: errors.New("fail")},
	}

	if _, err := c.Reconcile(context.Background()); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

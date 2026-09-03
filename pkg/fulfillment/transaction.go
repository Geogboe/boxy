// Package fulfillment provides reusable atomic coordination for keyed claims.
package fulfillment

import (
	"context"
	"errors"
	"fmt"
)

// Group describes one homogeneous claim from a keyed inventory.
type Group[K comparable] struct {
	Key   K
	Count int
}

// Transaction coordinates preparation and all-or-nothing fulfillment across
// multiple keyed groups. Snapshot and rollback are caller-owned because the
// transaction does not know how inventory is persisted.
type Transaction[K comparable, S any] struct {
	Prepare  func(context.Context, Group[K]) error
	Snapshot func(context.Context, []Group[K]) (S, error)
	Fulfill  func(context.Context, Group[K]) error
	Rollback func(context.Context, S, error) error
}

// Abort marks a fulfillment error as lifecycle cancellation. An aborted
// transaction returns the wrapped error without invoking Rollback.
func Abort(err error) error {
	if err == nil {
		return nil
	}
	return abortError{err: err}
}

type abortError struct {
	err error
}

func (e abortError) Error() string { return e.err.Error() }

func (e abortError) Unwrap() error { return e.err }

// RolledBackError reports the original fulfillment failure after Rollback
// completed successfully. Callers that persist their own failure state can
// use errors.As to distinguish this expected recovery path from a rollback
// failure.
type RolledBackError struct {
	Cause error
}

func (e RolledBackError) Error() string { return fmt.Sprintf("fulfillment rolled back: %v", e.Cause) }

func (e RolledBackError) Unwrap() error { return e.Cause }

// Run prepares every group, captures a snapshot, and fulfills groups in input
// order. A fulfillment failure invokes Rollback exactly once. Preparation and
// snapshot failures happen before any claim and therefore do not roll back.
func (t Transaction[K, S]) Run(ctx context.Context, groups []Group[K]) error {
	if ctx == nil {
		return errors.New("fulfillment context is nil")
	}
	if t.Prepare == nil {
		return errors.New("fulfillment prepare callback is nil")
	}
	if t.Snapshot == nil {
		return errors.New("fulfillment snapshot callback is nil")
	}
	if t.Fulfill == nil {
		return errors.New("fulfillment callback is nil")
	}
	if t.Rollback == nil {
		return errors.New("fulfillment rollback callback is nil")
	}

	seen := make(map[K]struct{}, len(groups))
	for _, group := range groups {
		if group.Count <= 0 {
			return fmt.Errorf("fulfillment group %v count must be > 0", group.Key)
		}
		if _, exists := seen[group.Key]; exists {
			return fmt.Errorf("fulfillment group %v is duplicated", group.Key)
		}
		seen[group.Key] = struct{}{}
	}

	for _, group := range groups {
		if err := t.Prepare(ctx, group); err != nil {
			return fmt.Errorf("prepare group %v: %w", group.Key, err)
		}
	}

	snapshot, err := t.Snapshot(ctx, groups)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	for _, group := range groups {
		err := t.Fulfill(ctx, group)
		if err == nil {
			continue
		}
		var abort abortError
		if errors.As(err, &abort) {
			return fmt.Errorf("fulfill group %v aborted: %w", group.Key, abort.err)
		}

		rollbackErr := t.Rollback(ctx, snapshot, err)
		if rollbackErr != nil {
			return fmt.Errorf("fulfill group %v: %w", group.Key, errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr)))
		}
		return RolledBackError{Cause: fmt.Errorf("fulfill group %v: %w", group.Key, err)}
	}

	return nil
}

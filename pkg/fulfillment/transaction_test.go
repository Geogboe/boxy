package fulfillment

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTransactionRun_SequencesPreparationSnapshotAndFulfillment(t *testing.T) {
	t.Parallel()

	var events []string
	tx := Transaction[string, int]{
		Prepare: func(_ context.Context, group Group[string]) error {
			events = append(events, "prepare:"+group.Key)
			return nil
		},
		Snapshot: func(_ context.Context, groups []Group[string]) (int, error) {
			events = append(events, "snapshot")
			if len(groups) != 2 {
				t.Fatalf("snapshot groups = %#v, want two groups", groups)
			}
			return 42, nil
		},
		Fulfill: func(_ context.Context, group Group[string]) error {
			events = append(events, "fulfill:"+group.Key)
			return nil
		},
		Rollback: func(_ context.Context, snapshot int, cause error) error {
			events = append(events, "rollback")
			return nil
		},
	}

	if err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 2}, {Key: "vm", Count: 1}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"prepare:web", "prepare:vm", "snapshot", "fulfill:web", "fulfill:vm"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestTransactionRun_RejectsInvalidGroupsWithoutCallbacks(t *testing.T) {
	t.Parallel()

	called := false
	tx := Transaction[string, struct{}]{
		Prepare: func(context.Context, Group[string]) error { called = true; return nil },
		Snapshot: func(context.Context, []Group[string]) (struct{}, error) {
			called = true
			return struct{}{}, nil
		},
		Fulfill:  func(context.Context, Group[string]) error { called = true; return nil },
		Rollback: func(context.Context, struct{}, error) error { called = true; return nil },
	}

	for name, groups := range map[string][]Group[string]{
		"zero count":     {{Key: "web"}},
		"negative count": {{Key: "web", Count: -1}},
		"duplicate key":  {{Key: "web", Count: 1}, {Key: "web", Count: 2}},
	} {
		t.Run(name, func(t *testing.T) {
			called = false
			if err := tx.Run(context.Background(), groups); err == nil {
				t.Fatal("Run returned nil, want validation error")
			}
			if called {
				t.Fatal("callback invoked for invalid groups")
			}
		})
	}
}

func TestTransactionRun_DoesNotRollbackPreparationOrSnapshotFailure(t *testing.T) {
	t.Parallel()

	prepareErr := errors.New("capacity unavailable")
	tx := Transaction[string, struct{}]{
		Prepare: func(_ context.Context, _ Group[string]) error { return prepareErr },
		Snapshot: func(_ context.Context, _ []Group[string]) (struct{}, error) {
			return struct{}{}, errors.New("snapshot failed")
		},
		Fulfill:  func(_ context.Context, _ Group[string]) error { t.Fatal("fulfill called"); return nil },
		Rollback: func(_ context.Context, _ struct{}, _ error) error { t.Fatal("rollback called"); return nil },
	}

	if err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 1}}); !errors.Is(err, prepareErr) {
		t.Fatalf("Run error = %v, want %v", err, prepareErr)
	}

	tx.Prepare = func(_ context.Context, _ Group[string]) error { return nil }
	if err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 1}}); err == nil || err.Error() != "snapshot: snapshot failed" {
		t.Fatalf("Run snapshot error = %v, want wrapped snapshot failure", err)
	}
}

func TestTransactionRun_RollsBackOnceAndJoinsRollbackFailure(t *testing.T) {
	t.Parallel()

	fulfillErr := errors.New("allocator failed")
	rollbackErr := errors.New("restore failed")
	rollbackCalls := 0
	tx := Transaction[string, string]{
		Prepare:  func(context.Context, Group[string]) error { return nil },
		Snapshot: func(context.Context, []Group[string]) (string, error) { return "snapshot", nil },
		Fulfill: func(_ context.Context, group Group[string]) error {
			if group.Key == "vm" {
				return fulfillErr
			}
			return nil
		},
		Rollback: func(_ context.Context, snapshot string, cause error) error {
			rollbackCalls++
			if snapshot != "snapshot" || !errors.Is(cause, fulfillErr) {
				t.Fatalf("rollback arguments = %q, %v", snapshot, cause)
			}
			return rollbackErr
		},
	}

	err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 1}, {Key: "vm", Count: 1}})
	if !errors.Is(err, fulfillErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("Run error = %v, want fulfillment and rollback errors", err)
	}
	if rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", rollbackCalls)
	}
}

func TestTransactionRun_ReturnsRollbackMarkerAfterSuccessfulRollback(t *testing.T) {
	t.Parallel()

	fulfillErr := errors.New("allocator failed")
	tx := Transaction[string, struct{}]{
		Prepare:  func(context.Context, Group[string]) error { return nil },
		Snapshot: func(context.Context, []Group[string]) (struct{}, error) { return struct{}{}, nil },
		Fulfill:  func(context.Context, Group[string]) error { return fulfillErr },
		Rollback: func(context.Context, struct{}, error) error { return nil },
	}

	err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 1}})
	var rolledBack RolledBackError
	if !errors.As(err, &rolledBack) || !errors.Is(err, fulfillErr) {
		t.Fatalf("Run error = %v, want rollback marker containing fulfillment error", err)
	}
}

func TestTransactionRun_AbortSkipsRollback(t *testing.T) {
	t.Parallel()

	abortErr := errors.New("sandbox deleting")
	rollbackCalled := false
	tx := Transaction[string, struct{}]{
		Prepare:  func(context.Context, Group[string]) error { return nil },
		Snapshot: func(context.Context, []Group[string]) (struct{}, error) { return struct{}{}, nil },
		Fulfill:  func(context.Context, Group[string]) error { return Abort(abortErr) },
		Rollback: func(context.Context, struct{}, error) error { rollbackCalled = true; return nil },
	}

	err := tx.Run(context.Background(), []Group[string]{{Key: "web", Count: 1}})
	if !errors.Is(err, abortErr) {
		t.Fatalf("Run error = %v, want %v", err, abortErr)
	}
	if rollbackCalled {
		t.Fatal("rollback called for explicit abort")
	}
}

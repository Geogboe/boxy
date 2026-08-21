package lifecycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestDispatcherPublishesAndAcknowledgesEvent(t *testing.T) {
	events := store.NewMemoryStore()
	var handled lifecycle.Event
	d := lifecycle.NewDispatcher(events, lifecycle.HandlerFunc(func(_ context.Context, event lifecycle.Event) (lifecycle.Outcome, error) {
		handled = event
		return lifecycle.OutcomeAck, nil
	}))

	event := lifecycle.Event{ID: "resource.provisioned:res-1", Type: "resource.provisioned", Subject: "res-1"}
	if err := d.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	processed, err := d.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !processed {
		t.Fatal("RunOnce processed = false, want true")
	}
	if handled.ID != event.ID || handled.Subject != event.Subject {
		t.Fatalf("handled event = %+v, want %+v", handled, event)
	}

	if _, err := events.Claim(context.Background(), time.Now().UTC(), time.Minute); !errors.Is(err, lifecycle.ErrNoWork) {
		t.Fatalf("Claim after acknowledgement error = %v, want ErrNoWork", err)
	}
}

func TestDispatcherRetriesTransientFailure(t *testing.T) {
	events := store.NewMemoryStore()
	attempts := 0
	d := lifecycle.NewDispatcher(events, lifecycle.HandlerFunc(func(_ context.Context, _ lifecycle.Event) (lifecycle.Outcome, error) {
		attempts++
		if attempts == 1 {
			return lifecycle.OutcomeRetry, errors.New("temporary provider outage")
		}
		return lifecycle.OutcomeAck, nil
	}))
	d.RetryDelay = 0

	if err := d.Publish(context.Background(), lifecycle.Event{ID: "event-1", Type: "test", Subject: "subject-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if _, err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

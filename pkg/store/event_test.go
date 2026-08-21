package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/lifecycle"
)

func TestMemoryStoreEventQueueIsIdempotentAndRecoverable(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	event := lifecycle.Event{ID: "event-1", Type: "resource.provisioned", Subject: "res-1", RecordedAt: now}

	if err := s.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(ctx, event); err != nil {
		t.Fatalf("duplicate Append: %v", err)
	}
	claim, err := s.Claim(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Event.ID != event.ID || claim.Event.Attempt != 1 {
		t.Fatalf("claim = %+v, want event-1 attempt 1", claim)
	}

	if err := s.Retry(ctx, claim, now.Add(-time.Second), errors.New("temporary")); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	reclaimed, err := s.Claim(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.Event.Attempt != 2 {
		t.Fatalf("reclaimed attempt = %d, want 2", reclaimed.Event.Attempt)
	}
	if err := s.Ack(ctx, reclaimed); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if _, err := s.Claim(ctx, now, time.Minute); !errors.Is(err, lifecycle.ErrNoWork) {
		t.Fatalf("Claim after Ack error = %v, want ErrNoWork", err)
	}
}

func TestDiskStoreEventQueuePersistsAcrossReopen(t *testing.T) {
	path := t.TempDir() + "\\state.json"
	first, err := NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	event := lifecycle.Event{ID: "event-1", Type: "test", Subject: "subject-1", RecordedAt: time.Now().UTC()}
	if err := first.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}
	second, err := NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen NewDiskStore: %v", err)
	}
	claim, err := second.Claim(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("Claim after reopen: %v", err)
	}
	if claim.Event.ID != event.ID {
		t.Fatalf("event after reopen = %+v, want %q", claim.Event, event.ID)
	}
}

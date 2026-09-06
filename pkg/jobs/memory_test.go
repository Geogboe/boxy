package jobs

import (
	"context"
	"errors"
	"testing"
)

// TestMemoryStorePutRejectsEmptyID matches the same guard store.MemoryStore
// and store.DiskStore already apply to a job's ID: an empty ID would store
// under the zero key, breaking Runner/lookup invariants (found by Copilot
// review on PR #338).
func TestMemoryStorePutRejectsEmptyID(t *testing.T) {
	s := NewMemoryStore()
	err := s.Put(context.Background(), Job{Kind: "test", Target: "t"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Put with empty ID: err = %v, want ErrInvalidRequest", err)
	}
	if _, err := s.Get(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(\"\") after rejected Put: err = %v, want ErrNotFound", err)
	}
}

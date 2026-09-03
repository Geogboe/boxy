package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewFileStore_DefaultRetentionIsFourteenDays(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "diagnostics.jsonl"), 0, 0)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if store.maxAge != 14*24*time.Hour {
		t.Fatalf("max age = %v, want 14 days", store.maxAge)
	}
}

func TestFileStorePersistsOrdersAndPaginates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	first := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	s, err := NewFileStore(path, 10<<20, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for i, event := range []Event{
		{ID: "event-1", Timestamp: first, Level: "warn", Component: "pool", Message: "older", Pool: "p1"},
		{ID: "event-2", Timestamp: first.Add(time.Minute), Level: "error", Component: "agent", Message: "newer", Agent: "a1"},
	} {
		if err := s.Append(context.Background(), event); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	page, err := s.Query(context.Background(), Query{Limit: 1})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != "event-2" || page.NextCursor == "" {
		t.Fatalf("page = %+v, want newest event and cursor", page)
	}

	page, err = s.Query(context.Background(), Query{Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("Query(cursor): %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != "event-1" || page.NextCursor != "" {
		t.Fatalf("cursor page = %+v, want older event without cursor", page)
	}

	reloaded, err := NewFileStore(path, 10<<20, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore(reload): %v", err)
	}
	all, err := reloaded.Query(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatalf("Query(reload): %v", err)
	}
	if len(all.Events) != 2 || all.Events[0].ID != "event-2" {
		t.Fatalf("reloaded events = %+v, want both events newest-first", all.Events)
	}
}

func TestFileStoreAppliesAgeRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	s, err := NewFileStore(path, 10<<20, time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	now := time.Now().UTC()
	if err := s.Append(context.Background(), Event{ID: "old", Timestamp: now.Add(-2 * time.Hour), Message: "old"}); err != nil {
		t.Fatalf("Append(old): %v", err)
	}
	if err := s.Append(context.Background(), Event{ID: "new", Timestamp: now, Message: "new"}); err != nil {
		t.Fatalf("Append(new): %v", err)
	}
	page, err := s.Query(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != "new" {
		t.Fatalf("events = %+v, want only new event", page.Events)
	}
}

func TestFileStoreAppliesAgeRetentionAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	start := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	initial, err := NewFileStore(path, 10<<20, time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	initial.now = func() time.Time { return start }
	if err := initial.Append(context.Background(), Event{ID: "old-after-restart", Timestamp: start, Message: "old"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	reloaded, err := NewFileStore(path, 10<<20, time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore(reload): %v", err)
	}
	reloaded.now = func() time.Time { return start.Add(2 * time.Hour) }
	page, err := reloaded.Query(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatalf("Query(reload): %v", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("events = %+v, want stale event omitted after restart", page.Events)
	}
}

func TestFileStoreRejectsInvalidCursor(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "diagnostics.jsonl"), 10<<20, time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := s.Query(context.Background(), Query{Cursor: "not-a-cursor"}); err == nil {
		t.Fatal("Query invalid cursor error = nil")
	}
}

func TestFileStoreCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "diagnostics.jsonl")
	s, err := NewFileStore(path, 10<<20, time.Hour)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Append(context.Background(), Event{Message: "event"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "Authorization") {
		t.Fatal("unexpected sensitive field in file")
	}
}

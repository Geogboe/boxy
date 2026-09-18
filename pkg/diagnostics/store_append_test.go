package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStoreAppendRetainsExactByteWindow(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	event := Event{ID: "evt-001", Timestamp: stamp, Level: "INFO", Message: "entry"}
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := NewFileStore(path, int64(2*(len(line)+1)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return stamp }
	for _, id := range []string{"evt-001", "evt-003", "evt-002", "evt-004"} {
		event.ID = id
		if err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	assertPersistedEventIDs(t, path, "evt-004", "evt-003")
}

func TestFileStoreAppendExpiresPreviouslyCachedEvents(t *testing.T) {
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := NewFileStore(path, 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return stamp }
	if err := store.Append(context.Background(), Event{ID: "old", Timestamp: stamp}); err != nil {
		t.Fatal(err)
	}
	stamp = stamp.Add(2 * time.Hour)
	if err := store.Append(context.Background(), Event{ID: "new", Timestamp: stamp}); err != nil {
		t.Fatal(err)
	}
	assertPersistedEventIDs(t, path, "new")
}

func TestFileStoreAppendReloadsAnotherStoreWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first, err := NewFileStore(path, 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(path, 1<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC()
	for i, store := range []*FileStore{first, second, first} {
		id := []string{"first", "external", "last"}[i]
		if err := store.Append(context.Background(), Event{ID: id, Timestamp: stamp.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := second.Query(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 3 || page.Events[0].ID != "last" || page.Events[1].ID != "external" || page.Events[2].ID != "first" {
		t.Fatalf("unexpected persisted events: %+v", page.Events)
	}
}

func assertPersistedEventIDs(t *testing.T, path string, ids ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(ids) {
		t.Fatalf("persisted %d records, want %d", len(lines), len(ids))
	}
	for i, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.ID != ids[i] {
			t.Fatalf("persisted event %d = %q, want %q", i, event.ID, ids[i])
		}
	}
}

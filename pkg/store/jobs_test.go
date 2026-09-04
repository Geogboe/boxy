package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestJobStoreRoundTripIsolationAndDelete(t *testing.T) {
	for name, newStore := range map[string]func(t *testing.T) store.Store{
		"memory": func(t *testing.T) store.Store { return store.NewMemoryStore() },
		"disk": func(t *testing.T) store.Store {
			s, err := store.NewDiskStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := newStore(t)
			now := time.Now().UTC()
			input := jobs.Job{
				ID: "job-1", Kind: "pool.fill", Target: "pool-a", Status: jobs.StatusRunning, CreatedAt: now,
				Steps: []jobs.Step{{Sequence: 1, Code: "vm.create", Subject: "vm-1", Status: jobs.StepStarted, At: now}},
			}
			if err := st.Put(context.Background(), input); err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, err := st.Get(context.Background(), input.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			got.Steps[0].Code = "mutated"
			again, err := st.Get(context.Background(), input.ID)
			if err != nil {
				t.Fatalf("Get again: %v", err)
			}
			if again.Steps[0].Code != "vm.create" {
				t.Fatalf("store returned mutable job alias: %+v", again.Steps)
			}
			listed, err := st.List(context.Background())
			if err != nil || len(listed) != 1 || listed[0].ID != input.ID {
				t.Fatalf("List = %+v, err=%v", listed, err)
			}
			if err := st.Delete(context.Background(), input.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := st.Get(context.Background(), input.ID); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("Get deleted error = %v, want jobs.ErrNotFound", err)
			}
		})
	}
}

func TestDiskStorePersistsJobsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := jobs.Job{ID: "job-1", Kind: "agent.logs", Target: "agent-a", Status: jobs.StatusPending, CreatedAt: time.Now().UTC()}
	if err := first.Put(context.Background(), want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	second, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.Get(context.Background(), want.ID)
	if err != nil || got.ID != want.ID || got.Kind != want.Kind {
		t.Fatalf("reloaded job = %+v, err=%v", got, err)
	}
}

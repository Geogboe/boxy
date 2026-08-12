package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestMemoryStoreAPIKeyLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.NewMemoryStore()
	want := model.APIKey{
		ID:        "key-1",
		Hash:      "hash-1",
		Role:      model.APIKeyRoleAdmin,
		Name:      "operator",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := s.PutAPIKey(ctx, want); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}
	got, err := s.GetAPIKey(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if got != want {
		t.Fatalf("GetAPIKey = %+v, want %+v", got, want)
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != want.ID {
		t.Fatalf("ListAPIKeys = %+v, want key-1", keys)
	}
	if err := s.DeleteAPIKey(ctx, want.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := s.GetAPIKey(ctx, want.ID); err != store.ErrNotFound {
		t.Fatalf("GetAPIKey after delete = %v, want ErrNotFound", err)
	}
}

func TestDiskStoreAPIKeysPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/state.json"
	s, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	want := model.APIKey{ID: "key-1", Hash: "hash-1", Role: model.APIKeyRoleUser}
	if err := s.PutAPIKey(ctx, want); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}

	reopened, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen DiskStore: %v", err)
	}
	got, err := reopened.GetAPIKey(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetAPIKey after reopen: %v", err)
	}
	if got != want {
		t.Fatalf("reopened API key = %+v, want %+v", got, want)
	}
}

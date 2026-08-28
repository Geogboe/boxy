package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestMemoryStoreSessionLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.NewMemoryStore()
	want := model.Session{
		ID:        "session-1",
		Hash:      "hash-1",
		Kind:      model.SessionKindLocalAdmin,
		Subject:   model.LocalAdminUsername,
		Role:      model.APIKeyRoleAdmin,
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC),
	}

	if err := s.PutSession(ctx, want); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	got, err := s.GetSession(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != want {
		t.Fatalf("GetSession = %+v, want %+v", got, want)
	}
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != want.ID {
		t.Fatalf("ListSessions = %+v, want session-1", sessions)
	}
	if err := s.DeleteSession(ctx, want.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(ctx, want.ID); err != store.ErrNotFound {
		t.Fatalf("GetSession after delete = %v, want ErrNotFound", err)
	}
}

func TestDiskStoreSessionsPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/state.json"
	s, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	want := model.Session{ID: "session-1", Hash: "hash-1", Kind: model.SessionKindLocalAdmin, Subject: "admin", Role: model.APIKeyRoleAdmin}
	if err := s.PutSession(ctx, want); err != nil {
		t.Fatalf("PutSession: %v", err)
	}

	reopened, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen DiskStore: %v", err)
	}
	got, err := reopened.GetSession(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if got != want {
		t.Fatalf("reopened session = %+v, want %+v", got, want)
	}
}

func TestMemoryStoreLocalAdminLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.NewMemoryStore()

	if _, err := s.GetLocalAdmin(ctx); err != store.ErrNotFound {
		t.Fatalf("GetLocalAdmin before bootstrap = %v, want ErrNotFound", err)
	}

	want := model.LocalAdminAccount{
		Username:     model.LocalAdminUsername,
		PasswordHash: "hash-1",
		CreatedAt:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.PutLocalAdmin(ctx, want); err != nil {
		t.Fatalf("PutLocalAdmin: %v", err)
	}
	got, err := s.GetLocalAdmin(ctx)
	if err != nil {
		t.Fatalf("GetLocalAdmin: %v", err)
	}
	if got != want {
		t.Fatalf("GetLocalAdmin = %+v, want %+v", got, want)
	}
}

func TestDiskStoreLocalAdminPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/state.json"
	s, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	if _, err := s.GetLocalAdmin(ctx); err != store.ErrNotFound {
		t.Fatalf("GetLocalAdmin before bootstrap = %v, want ErrNotFound", err)
	}

	want := model.LocalAdminAccount{Username: model.LocalAdminUsername, PasswordHash: "hash-1"}
	if err := s.PutLocalAdmin(ctx, want); err != nil {
		t.Fatalf("PutLocalAdmin: %v", err)
	}

	reopened, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen DiskStore: %v", err)
	}
	got, err := reopened.GetLocalAdmin(ctx)
	if err != nil {
		t.Fatalf("GetLocalAdmin after reopen: %v", err)
	}
	if got != want {
		t.Fatalf("reopened local admin = %+v, want %+v", got, want)
	}
}

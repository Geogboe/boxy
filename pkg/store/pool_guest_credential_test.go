package store_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestMemoryStorePoolGuestCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()

	if _, err := s.GetPoolGuestCredential(ctx, "missing"); err != store.ErrNotFound {
		t.Fatalf("GetPoolGuestCredential missing error = %v, want ErrNotFound", err)
	}
	if err := s.PutPoolGuestCredential(ctx, "hyperv", "bootstrap-secret"); err != nil {
		t.Fatalf("PutPoolGuestCredential: %v", err)
	}
	got, err := s.GetPoolGuestCredential(ctx, model.PoolName("hyperv"))
	if err != nil {
		t.Fatalf("GetPoolGuestCredential: %v", err)
	}
	if got != "bootstrap-secret" {
		t.Fatalf("credential = %q, want bootstrap-secret", got)
	}
}

func TestDiskStorePoolGuestCredentialPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.json"
	s, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	if err := s.PutPoolGuestCredential(ctx, "hyperv", "bootstrap-secret"); err != nil {
		t.Fatalf("PutPoolGuestCredential: %v", err)
	}

	reopened, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen NewDiskStore: %v", err)
	}
	got, err := reopened.GetPoolGuestCredential(ctx, "hyperv")
	if err != nil {
		t.Fatalf("GetPoolGuestCredential after reopen: %v", err)
	}
	if got != "bootstrap-secret" {
		t.Fatalf("reopened credential = %q, want bootstrap-secret", got)
	}
}

func TestPoolGuestCredentialRejectsBlankValues(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()
	for _, value := range []string{"", "   "} {
		if err := s.PutPoolGuestCredential(ctx, "hyperv", value); err == nil {
			t.Fatalf("PutPoolGuestCredential(%q) error = nil", value)
		}
	}
}

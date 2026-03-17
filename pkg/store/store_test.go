package store_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// storeFactory returns a fresh, empty Store implementation.
type storeFactory func(t *testing.T) store.Store

// testStoreList runs List* tests against any Store implementation.
func testStoreList(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("ListPools_empty", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		pools, err := s.ListPools(context.Background())
		if err != nil {
			t.Fatalf("ListPools: %v", err)
		}
		if len(pools) != 0 {
			t.Fatalf("ListPools len = %d, want 0", len(pools))
		}
	})

	t.Run("ListPools_returns_all", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.PutPool(ctx, model.Pool{Name: "a"})
		_ = s.PutPool(ctx, model.Pool{Name: "b"})

		pools, err := s.ListPools(ctx)
		if err != nil {
			t.Fatalf("ListPools: %v", err)
		}
		if len(pools) != 2 {
			t.Fatalf("ListPools len = %d, want 2", len(pools))
		}
		names := map[model.PoolName]bool{}
		for _, p := range pools {
			names[p.Name] = true
		}
		if !names["a"] || !names["b"] {
			t.Fatalf("ListPools missing expected pools: got %v", names)
		}
	})

	t.Run("ListResources_empty", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		res, err := s.ListResources(context.Background())
		if err != nil {
			t.Fatalf("ListResources: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("ListResources len = %d, want 0", len(res))
		}
	})

	t.Run("ListResources_returns_all", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.PutResource(ctx, model.Resource{ID: "r1"})
		_ = s.PutResource(ctx, model.Resource{ID: "r2"})
		_ = s.PutResource(ctx, model.Resource{ID: "r3"})

		res, err := s.ListResources(ctx)
		if err != nil {
			t.Fatalf("ListResources: %v", err)
		}
		if len(res) != 3 {
			t.Fatalf("ListResources len = %d, want 3", len(res))
		}
	})

	t.Run("ListSandboxes_empty", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		sbs, err := s.ListSandboxes(context.Background())
		if err != nil {
			t.Fatalf("ListSandboxes: %v", err)
		}
		if len(sbs) != 0 {
			t.Fatalf("ListSandboxes len = %d, want 0", len(sbs))
		}
	})

	t.Run("ListSandboxes_returns_all", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "one"})
		_ = s.CreateSandbox(ctx, model.Sandbox{ID: "sb-2", Name: "two"})

		sbs, err := s.ListSandboxes(ctx)
		if err != nil {
			t.Fatalf("ListSandboxes: %v", err)
		}
		if len(sbs) != 2 {
			t.Fatalf("ListSandboxes len = %d, want 2", len(sbs))
		}
	})
}

// testStoreDeleteSandbox runs DeleteSandbox tests against any Store implementation.
func testStoreDeleteSandbox(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("DeleteSandbox_existing", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "one"})

		err := s.DeleteSandbox(ctx, "sb-1")
		if err != nil {
			t.Fatalf("DeleteSandbox: %v", err)
		}

		_, err = s.GetSandbox(ctx, "sb-1")
		if err != store.ErrNotFound {
			t.Fatalf("GetSandbox after delete: got %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteSandbox_not_found", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		err := s.DeleteSandbox(ctx, "no-such-id")
		if err != store.ErrNotFound {
			t.Fatalf("DeleteSandbox non-existent: got %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteSandbox_does_not_affect_others", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "one"})
		_ = s.CreateSandbox(ctx, model.Sandbox{ID: "sb-2", Name: "two"})

		_ = s.DeleteSandbox(ctx, "sb-1")

		sbs, err := s.ListSandboxes(ctx)
		if err != nil {
			t.Fatalf("ListSandboxes: %v", err)
		}
		if len(sbs) != 1 {
			t.Fatalf("ListSandboxes len = %d, want 1", len(sbs))
		}
		if sbs[0].ID != "sb-2" {
			t.Fatalf("remaining sandbox ID = %q, want %q", sbs[0].ID, "sb-2")
		}
	})
}

var memoryFactory = func(t *testing.T) store.Store {
	return store.NewMemoryStore()
}

var diskFactory = func(t *testing.T) store.Store {
	path := t.TempDir() + "/test.json"
	s, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	return s
}

func TestMemoryStore_List(t *testing.T) {
	t.Parallel()
	testStoreList(t, memoryFactory)
}

func TestDiskStore_List(t *testing.T) {
	t.Parallel()
	testStoreList(t, diskFactory)
}

func TestMemoryStore_DeleteSandbox(t *testing.T) {
	t.Parallel()
	testStoreDeleteSandbox(t, memoryFactory)
}

func TestDiskStore_DeleteSandbox(t *testing.T) {
	t.Parallel()
	testStoreDeleteSandbox(t, diskFactory)
}

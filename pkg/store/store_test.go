package store_test

import (
	"context"
	"testing"
	"time"

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

func testStoreDeleteResource(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("DeleteResource_existing", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		if err := s.PutResource(ctx, model.Resource{ID: "res-1"}); err != nil {
			t.Fatalf("PutResource: %v", err)
		}

		if err := s.DeleteResource(ctx, "res-1"); err != nil {
			t.Fatalf("DeleteResource: %v", err)
		}

		_, err := s.GetResource(ctx, "res-1")
		if err != store.ErrNotFound {
			t.Fatalf("GetResource after delete: got %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteResource_not_found", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		err := s.DeleteResource(ctx, "no-such-id")
		if err != store.ErrNotFound {
			t.Fatalf("DeleteResource non-existent: got %v, want ErrNotFound", err)
		}
	})
}

func testStoreSandboxFieldsRoundTrip(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("Sandbox_fields_round_trip", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		want := model.Sandbox{
			ID:     "sb-async",
			Name:   "async-lab",
			Status: model.SandboxStatusProvisioning,
			Error:  "still provisioning",
			Policies: model.SandboxPolicies{
				AutoDestroyAfter: "8h",
				SecurityProfile:  "lab",
			},
			Requests: []model.ResourceRequest{
				{Type: model.ResourceTypeContainer, Profile: "alpine", Count: 2},
			},
			Resources: []model.ResourceID{"res-1"},
		}

		if err := s.CreateSandbox(ctx, want); err != nil {
			t.Fatalf("CreateSandbox: %v", err)
		}

		got, err := s.GetSandbox(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetSandbox: %v", err)
		}

		if got.Status != want.Status {
			t.Fatalf("status = %q, want %q", got.Status, want.Status)
		}
		if got.Error != want.Error {
			t.Fatalf("error = %q, want %q", got.Error, want.Error)
		}
		if len(got.Requests) != 1 {
			t.Fatalf("requests len = %d, want 1", len(got.Requests))
		}
		if got.Requests[0] != want.Requests[0] {
			t.Fatalf("request = %+v, want %+v", got.Requests[0], want.Requests[0])
		}
	})
}

func testStoreResourceFieldsRoundTrip(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("Resource_fields_round_trip", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		want := model.Resource{
			ID:         "res-origin",
			Type:       model.ResourceTypeContainer,
			Profile:    "web",
			OriginPool: "web",
			Provider:   model.ProviderRef{Name: "docker"},
			State:      model.ResourceStateAllocated,
		}

		if err := s.PutResource(ctx, want); err != nil {
			t.Fatalf("PutResource: %v", err)
		}

		got, err := s.GetResource(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetResource: %v", err)
		}
		if got.OriginPool != want.OriginPool {
			t.Fatalf("origin_pool = %q, want %q", got.OriginPool, want.OriginPool)
		}
		if got.State != want.State {
			t.Fatalf("state = %q, want %q", got.State, want.State)
		}
	})
}

// testStoreAgentTokensAndRevocation runs agent-token and revoked-identity
// tests against any Store implementation.
func testStoreAgentTokensAndRevocation(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("AgentToken_lifecycle_unused_to_used", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		now := time.Now().UTC()
		tok := model.AgentRegistrationToken{
			ID:        "tok-1",
			TokenHash: "deadbeef",
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
			Label:     "lab-hypervisor-1",
		}
		if err := s.PutAgentToken(ctx, tok); err != nil {
			t.Fatalf("PutAgentToken: %v", err)
		}

		got, err := s.GetAgentToken(ctx, "tok-1")
		if err != nil {
			t.Fatalf("GetAgentToken: %v", err)
		}
		if got.Used() {
			t.Fatal("expected a freshly created token to be unused")
		}
		if got.Expired(now) {
			t.Fatal("expected a token with a future expiry to not be expired yet")
		}
		if got.TokenHash != "deadbeef" || got.Label != "lab-hypervisor-1" {
			t.Fatalf("token fields did not round-trip: %+v", got)
		}

		usedAt := now.Add(time.Minute)
		got.UsedAt = &usedAt
		if err := s.PutAgentToken(ctx, got); err != nil {
			t.Fatalf("PutAgentToken (mark used): %v", err)
		}

		reread, err := s.GetAgentToken(ctx, "tok-1")
		if err != nil {
			t.Fatalf("GetAgentToken after mark-used: %v", err)
		}
		if !reread.Used() {
			t.Fatal("expected token to be marked used after re-reading")
		}
	})

	t.Run("AgentToken_expired_still_readable", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		now := time.Now().UTC()
		tok := model.AgentRegistrationToken{ID: "tok-2", ExpiresAt: now.Add(-time.Hour)}
		if err := s.PutAgentToken(ctx, tok); err != nil {
			t.Fatalf("PutAgentToken: %v", err)
		}

		got, err := s.GetAgentToken(ctx, "tok-2")
		if err != nil {
			t.Fatalf("GetAgentToken: %v", err)
		}
		if !got.Expired(now) {
			t.Fatal("expected the token to report expired")
		}
	})

	t.Run("AgentToken_not_found", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		if _, err := s.GetAgentToken(context.Background(), "no-such-token"); err != store.ErrNotFound {
			t.Fatalf("GetAgentToken error = %v, want ErrNotFound", err)
		}
	})

	t.Run("AgentToken_delete", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.PutAgentToken(ctx, model.AgentRegistrationToken{ID: "tok-3"})

		if err := s.DeleteAgentToken(ctx, "tok-3"); err != nil {
			t.Fatalf("DeleteAgentToken: %v", err)
		}
		if _, err := s.GetAgentToken(ctx, "tok-3"); err != store.ErrNotFound {
			t.Fatalf("GetAgentToken after delete = %v, want ErrNotFound", err)
		}
		if err := s.DeleteAgentToken(ctx, "tok-3"); err != store.ErrNotFound {
			t.Fatalf("DeleteAgentToken (already gone) = %v, want ErrNotFound", err)
		}
	})

	t.Run("AgentToken_list_returns_all", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)
		_ = s.PutAgentToken(ctx, model.AgentRegistrationToken{ID: "tok-a"})
		_ = s.PutAgentToken(ctx, model.AgentRegistrationToken{ID: "tok-b"})

		toks, err := s.ListAgentTokens(ctx)
		if err != nil {
			t.Fatalf("ListAgentTokens: %v", err)
		}
		if len(toks) != 2 {
			t.Fatalf("ListAgentTokens len = %d, want 2", len(toks))
		}
	})

	t.Run("RevokedAgentIdentity_deny_list", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := newStore(t)

		revoked, err := s.IsAgentIdentityRevoked(ctx, "serial-123")
		if err != nil {
			t.Fatalf("IsAgentIdentityRevoked: %v", err)
		}
		if revoked {
			t.Fatal("expected an unknown cert serial to not be revoked")
		}

		if err := s.PutRevokedAgentIdentity(ctx, model.RevokedAgentIdentity{
			ID:         "rev-1",
			AgentID:    "agent-a",
			CertSerial: "serial-123",
			RevokedAt:  time.Now().UTC(),
			Reason:     "host decommissioned",
		}); err != nil {
			t.Fatalf("PutRevokedAgentIdentity: %v", err)
		}

		revoked, err = s.IsAgentIdentityRevoked(ctx, "serial-123")
		if err != nil {
			t.Fatalf("IsAgentIdentityRevoked: %v", err)
		}
		if !revoked {
			t.Fatal("expected the revoked cert serial to now report revoked")
		}

		list, err := s.ListRevokedAgentIdentities(ctx)
		if err != nil {
			t.Fatalf("ListRevokedAgentIdentities: %v", err)
		}
		if len(list) != 1 || list[0].AgentID != "agent-a" {
			t.Fatalf("ListRevokedAgentIdentities = %+v, want one entry for agent-a", list)
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

func TestMemoryStore_DeleteResource(t *testing.T) {
	t.Parallel()
	testStoreDeleteResource(t, memoryFactory)
}

func TestDiskStore_DeleteResource(t *testing.T) {
	t.Parallel()
	testStoreDeleteResource(t, diskFactory)
}

func TestMemoryStore_SandboxFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	testStoreSandboxFieldsRoundTrip(t, memoryFactory)
}

func TestDiskStore_SandboxFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	testStoreSandboxFieldsRoundTrip(t, diskFactory)
}

func TestMemoryStore_ResourceFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	testStoreResourceFieldsRoundTrip(t, memoryFactory)
}

func TestDiskStore_ResourceFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	testStoreResourceFieldsRoundTrip(t, diskFactory)
}

func TestMemoryStore_AgentTokensAndRevocation(t *testing.T) {
	t.Parallel()
	testStoreAgentTokensAndRevocation(t, memoryFactory)
}

func TestDiskStore_AgentTokensAndRevocation(t *testing.T) {
	t.Parallel()
	testStoreAgentTokensAndRevocation(t, diskFactory)
}

func TestDiskStore_AgentTokensAndRevocationPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/test.json"

	s1, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	if err := s1.PutAgentToken(ctx, model.AgentRegistrationToken{ID: "tok-1", TokenHash: "abc"}); err != nil {
		t.Fatalf("PutAgentToken: %v", err)
	}
	if err := s1.PutRevokedAgentIdentity(ctx, model.RevokedAgentIdentity{ID: "rev-1", AgentID: "agent-a", CertSerial: "serial-123"}); err != nil {
		t.Fatalf("PutRevokedAgentIdentity: %v", err)
	}

	s2, err := store.NewDiskStore(path)
	if err != nil {
		t.Fatalf("reopen NewDiskStore: %v", err)
	}

	tok, err := s2.GetAgentToken(ctx, "tok-1")
	if err != nil {
		t.Fatalf("GetAgentToken after reopen: %v", err)
	}
	if tok.TokenHash != "abc" {
		t.Fatalf("token did not survive reopen: %+v", tok)
	}

	revoked, err := s2.IsAgentIdentityRevoked(ctx, "serial-123")
	if err != nil {
		t.Fatalf("IsAgentIdentityRevoked after reopen: %v", err)
	}
	if !revoked {
		t.Fatal("expected the revoked identity to survive reopen")
	}
}

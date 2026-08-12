package auth

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

type fakeAPIKeyStore struct {
	keys []model.APIKey
}

func (s *fakeAPIKeyStore) ListAPIKeys(context.Context) ([]model.APIKey, error) {
	return append([]model.APIKey(nil), s.keys...), nil
}

func TestGenerateAPIKey(t *testing.T) {
	raw, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if len(raw) < len(rawAPIKeyPrefix)+32 {
		t.Fatalf("generated key is unexpectedly short: %d", len(raw))
	}
	if raw[:len(rawAPIKeyPrefix)] != rawAPIKeyPrefix {
		t.Fatalf("generated key prefix = %q, want %q", raw[:len(rawAPIKeyPrefix)], rawAPIKeyPrefix)
	}
	if hash == "" || hash == raw {
		t.Fatalf("generated hash must be non-empty and must not equal raw key: %q", hash)
	}
	if HashAPIKey(raw) != hash {
		t.Fatalf("HashAPIKey(raw) = %q, want %q", HashAPIKey(raw), hash)
	}
}

func TestAuthenticate(t *testing.T) {
	raw, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	store := &fakeAPIKeyStore{keys: []model.APIKey{{
		ID:        "key-1",
		Hash:      hash,
		Role:      model.APIKeyRoleAdmin,
		CreatedAt: now,
		ExpiresAt: &future,
	}}}

	principal, err := Authenticate(context.Background(), store, raw, now)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if principal.KeyID != "key-1" || principal.Role != model.APIKeyRoleAdmin {
		t.Fatalf("principal = %+v, want key-1/admin", principal)
	}

	if _, err := Authenticate(context.Background(), store, "boxy_invalid", now); err == nil {
		t.Fatal("Authenticate(invalid) succeeded, want error")
	}
}

func TestAuthenticateRejectsExpiredAndRevokedKeys(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	raw, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	past := now.Add(-time.Hour)
	store := &fakeAPIKeyStore{keys: []model.APIKey{
		{ID: "expired", Hash: hash, Role: model.APIKeyRoleUser, ExpiresAt: &past},
	}}
	if _, err := Authenticate(context.Background(), store, raw, now); err == nil {
		t.Fatal("Authenticate(expired) succeeded, want error")
	}

	revokedAt := now.Add(-time.Minute)
	store.keys[0] = model.APIKey{ID: "revoked", Hash: hash, Role: model.APIKeyRoleUser, RevokedAt: &revokedAt}
	if _, err := Authenticate(context.Background(), store, raw, now); err == nil {
		t.Fatal("Authenticate(revoked) succeeded, want error")
	}
}

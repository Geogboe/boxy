package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestProfilePage_ShowsOnlyCurrentPersonalKeyMetadata(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	keys := []model.APIKey{
		{ID: "personal-alice", Hash: auth.HashAPIKey("alice-key"), Kind: model.APIKeyKindPersonal, Subject: "oidc:alice", Name: "alice-cli", Role: model.APIKeyRoleUser, CreatedAt: now},
		{ID: "personal-bob", Hash: auth.HashAPIKey("bob-key"), Kind: model.APIKeyKindPersonal, Subject: "oidc:bob", Name: "bob-cli", Role: model.APIKeyRoleUser, CreatedAt: now},
		{ID: "service-build", Hash: auth.HashAPIKey("service-key"), Kind: model.APIKeyKindService, Name: "build-service", Role: model.APIKeyRoleAdmin, CreatedAt: now},
	}
	for _, key := range keys {
		if err := st.PutAPIKey(context.Background(), key); err != nil {
			t.Fatalf("PutAPIKey(%s): %v", key.ID, err)
		}
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.OIDCAuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/profile", nil), st, "alice", model.APIKeyRoleUser)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "alice-cli") {
		t.Fatalf("profile page missing current user's key metadata, body = %q", body)
	}
	for _, forbidden := range []string{"bob-cli", "build-service", "personal-bob", "service-build", "alice-key", "bob-key", "service-key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("profile page disclosed %q, body = %q", forbidden, body)
		}
	}
}

func TestAdminServiceKeyPage_CreateAndListNeverListsRawKey(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	if err := st.PutAPIKey(context.Background(), model.APIKey{
		ID: "personal-alice", Hash: auth.HashAPIKey("alice-key"), Kind: model.APIKeyKindPersonal,
		Subject: "oidc:alice", Name: "alice-cli", Role: model.APIKeyRoleUser, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	form := url.Values{"name": {"deploy-service"}, "role": {"admin"}, "expires": {"24h"}}
	post := httptest.NewRecorder()
	create := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/service-keys", strings.NewReader(form.Encode())))
	create.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(post, create)
	if post.Code != http.StatusOK || !strings.Contains(post.Body.String(), "New service key") || !strings.Contains(post.Body.String(), "boxy_") {
		t.Fatalf("create status = %d, body = %q, want one-time raw key reveal", post.Code, post.Body.String())
	}

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/service-keys", nil)))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %q", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "boxy_") || strings.Contains(list.Body.String(), "alice-cli") {
		t.Fatalf("service-key listing disclosed a raw/personal key, body = %q", list.Body.String())
	}
	if !strings.Contains(list.Body.String(), "deploy-service") {
		t.Fatalf("service-key listing missing created service, body = %q", list.Body.String())
	}

	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	var serviceKey *model.APIKey
	for i := range keys {
		if keys[i].ID == "personal-alice" {
			continue
		}
		serviceKey = &keys[i]
	}
	if len(keys) != 2 || serviceKey == nil || serviceKey.Kind != model.APIKeyKindService || serviceKey.ExpiresAt == nil || !serviceKey.ExpiresAt.After(serviceKey.CreatedAt) {
		t.Fatalf("keys = %+v, want one expiring service key plus personal key", keys)
	}
}

func TestServiceKeyManagement_IsAdminOnlyAndRevokeIsIdempotent(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	created := time.Now().UTC()
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "service-1", Hash: auth.HashAPIKey("service-key"), Kind: model.APIKeyKindService, Name: "service", Role: model.APIKeyRoleAdmin, CreatedAt: created}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(url.Values{"name": {"bad"}, "role": {"user"}, "expires": {"24h"}}.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := server.OIDCAuthedRequest(httptest.NewRequest(method, "/ui/service-keys", body), st, "alice", model.APIKeyRoleUser)
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403; body = %q", method, w.Code, w.Body.String())
		}
	}

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/service-keys/service-1/revoke", nil)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("revoke attempt %d status = %d, want 204; body = %q", i+1, w.Code, w.Body.String())
		}
	}
	key, err := st.GetAPIKey(context.Background(), "service-1")
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if key.RevokedAt == nil {
		t.Fatalf("key = %+v, want revoked metadata", key)
	}
}

func TestAdminAPIKeyListExcludesPersonalKeys(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	for _, key := range []model.APIKey{
		{ID: "service-1", Hash: auth.HashAPIKey("service-key"), Kind: model.APIKeyKindService, Name: "service", Role: model.APIKeyRoleAdmin, CreatedAt: time.Now().UTC()},
		{ID: "personal-1", Hash: auth.HashAPIKey("personal-key"), Kind: model.APIKeyKindPersonal, Subject: "oidc:alice", Name: "personal", Role: model.APIKeyRoleUser, CreatedAt: time.Now().UTC()},
	} {
		if err := st.PutAPIKey(context.Background(), key); err != nil {
			t.Fatalf("PutAPIKey: %v", err)
		}
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(listed) != 1 || listed[0]["id"] != "service-1" || strings.Contains(w.Body.String(), "personal") {
		t.Fatalf("listed = %#v, body = %q, want service metadata only", listed, w.Body.String())
	}
	deletePersonal := httptest.NewRecorder()
	mux.ServeHTTP(deletePersonal, httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/personal-1", nil))
	if deletePersonal.Code != http.StatusNotFound {
		t.Fatalf("delete personal status = %d, want 404; body = %q", deletePersonal.Code, deletePersonal.Body.String())
	}
}

func TestServiceKeyCreationRejectsNonPositiveExpiry(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	form := url.Values{"name": {"bad-service"}, "role": {"user"}, "expires": {"0s"}}
	r := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/service-keys", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "positive Go duration") {
		t.Fatalf("status = %d, body = %q, want positive-expiry validation error", w.Code, w.Body.String())
	}
	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %+v, want no key after invalid expiry", keys)
	}
}

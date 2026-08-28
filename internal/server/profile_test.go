package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestProfilePage_ShowsIdentity(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/profile", nil)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "test-admin") {
		t.Fatalf("profile page missing subject, body = %q", body)
	}
	if !strings.Contains(body, "Local admin account") {
		t.Fatalf("profile page missing sign-in method, body = %q", body)
	}
}

func TestProfilePage_RequiresSession(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/profile", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect to login, body = %q", w.Code, w.Body.String())
	}
}

func TestMintPersonalKey_LocalAdminSession_UsesLocalSubjectPrefix(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	form := url.Values{"name": {"my-laptop"}}
	r := httptest.NewRequest(http.MethodPost, "/ui/profile/personal-key", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(r))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "New key") {
		t.Fatalf("profile page did not render the minted key, body = %q", w.Body.String())
	}

	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys = %+v, want exactly one minted key", keys)
	}
	if keys[0].Kind != model.APIKeyKindPersonal || keys[0].Subject != "local:test-admin" || keys[0].Name != "my-laptop" {
		t.Fatalf("key = %+v, want personal key with subject local:test-admin named my-laptop", keys[0])
	}
}

func TestMintPersonalKey_OIDCSession_UsesOIDCSubjectPrefix(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	r := httptest.NewRequest(http.MethodPost, "/ui/profile/personal-key", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = server.OIDCAuthedRequest(r, st, "alice", model.APIKeyRoleAdmin)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}

	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Subject != "oidc:alice" {
		t.Fatalf("keys = %+v, want exactly one personal key with subject oidc:alice", keys)
	}
}

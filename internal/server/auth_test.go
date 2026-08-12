package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestAuthenticatedAPIRejectsMissingAndInvalidCredentials(t *testing.T) {
	st := store.NewMemoryStore()
	s := &Server{store: st, authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	for _, tt := range []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "invalid", header: "Bearer boxy_invalid", status: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.status, w.Body.String())
			}
		})
	}
}

func TestAuthenticatedAPIAcceptsValidAdminCredential(t *testing.T) {
	st := store.NewMemoryStore()
	raw, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "admin-1", Hash: hash, Role: model.APIKeyRoleAdmin}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}
	s := &Server{store: st, authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestUserKeysAreScopedToOwnedSandboxes(t *testing.T) {
	st := store.NewMemoryStore()
	userRaw, userHash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey user: %v", err)
	}
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "user-1", Hash: userHash, Role: model.APIKeyRoleUser}); err != nil {
		t.Fatalf("PutAPIKey user: %v", err)
	}
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "owned", OwnerID: "user-1"}); err != nil {
		t.Fatalf("CreateSandbox owned: %v", err)
	}
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "other", OwnerID: "user-2"}); err != nil {
		t.Fatalf("CreateSandbox other: %v", err)
	}
	s := &Server{store: st, sandboxMgr: sandbox.New(st, nil), authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+userRaw)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	list := request(http.MethodGet, "/api/v1/sandboxes")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body.String())
	}
	var sandboxes []model.Sandbox
	if err := json.Unmarshal(list.Body.Bytes(), &sandboxes); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(sandboxes) != 1 || sandboxes[0].ID != "owned" {
		t.Fatalf("user sandbox list = %+v, want owned only", sandboxes)
	}
	other := request(http.MethodGet, "/api/v1/sandboxes/other")
	if other.Code != http.StatusForbidden {
		t.Fatalf("other sandbox status = %d, want 403; body=%s", other.Code, other.Body.String())
	}
	pools := request(http.MethodGet, "/api/v1/pools")
	if pools.Code != http.StatusForbidden {
		t.Fatalf("user pools status = %d, want 403; body=%s", pools.Code, pools.Body.String())
	}
}

func TestBootstrapAPIKeyIsLocalOnlyAndOneTime(t *testing.T) {
	st := store.NewMemoryStore()
	s := &Server{store: st, authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	request := func(remoteAddr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys/bootstrap", nil)
		r.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	first := request("127.0.0.1:1234")
	if first.Code != http.StatusCreated {
		t.Fatalf("first bootstrap status = %d, want 201; body=%s", first.Code, first.Body.String())
	}
	var response struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if response.ID == "" || response.Key == "" || response.Role != string(model.APIKeyRoleAdmin) {
		t.Fatalf("bootstrap response = %+v, want id/key/admin", response)
	}

	second := request("127.0.0.1:1234")
	if second.Code != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d, want 409; body=%s", second.Code, second.Body.String())
	}
	remote := request("192.0.2.10:1234")
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote bootstrap status = %d, want 403; body=%s", remote.Code, remote.Body.String())
	}
}

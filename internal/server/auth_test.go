package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestUserRoleKeyCanVerifyIdentityAndDiscoverPools covers #359's acceptance
// criteria: a `user`-role key can call the role-neutral identity endpoint
// (what `boxy login` now verifies against) and the safe pool-summary
// discovery endpoint, while still being denied the full administrative pool
// listing and other admin-only operations.
func TestUserRoleKeyCanVerifyIdentityAndDiscoverPools(t *testing.T) {
	st := store.NewMemoryStore()
	userRaw, userHash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey user: %v", err)
	}
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "user-1", Hash: userHash, Role: model.APIKeyRoleUser}); err != nil {
		t.Fatalf("PutAPIKey user: %v", err)
	}
	// A fully populated administrative pool -- inventory, policies, and
	// configuration provenance -- to prove the summary view actually
	// redacts everything beyond name/type/profile, not just an
	// already-empty struct.
	if err := st.PutPool(context.Background(), model.Pool{
		Name:     "web",
		Template: "web-template",
		Source:   "web-source",
		Packages: []string{"baseline@1.0.0"},
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 2, MaxTotal: 10},
			Recycle: model.RecyclePolicy{MaxAge: "24h"},
		},
		Configuration: model.PoolConfigurationState{Provenance: "local", LocalRevision: "abc123"},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "web",
			Resources: []model.Resource{
				{ID: "res-1", Type: model.ResourceTypeContainer, Profile: "web", State: model.ResourceStateReady},
			},
		},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}

	s := &Server{store: st, sandboxMgr: sandbox.New(st, nil), authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	request := func(method, path, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	// Login verification path: any valid role, including user, succeeds.
	identity := request(http.MethodGet, "/api/v1/identity", userRaw)
	if identity.Code != http.StatusOK {
		t.Fatalf("identity status = %d, want 200; body=%s", identity.Code, identity.Body.String())
	}
	var idResp struct {
		KeyID string `json:"key_id"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(identity.Body.Bytes(), &idResp); err != nil {
		t.Fatalf("decode identity: %v", err)
	}
	if idResp.KeyID != "user-1" || idResp.Role != string(model.APIKeyRoleUser) {
		t.Fatalf("identity = %+v, want key_id=user-1 role=user", idResp)
	}

	// Missing/invalid credentials still fail identity like every other route.
	if unauth := request(http.MethodGet, "/api/v1/identity", ""); unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity status = %d, want 401", unauth.Code)
	}

	// Safe discovery: pool summary is exactly name/type/profile.
	summary := request(http.MethodGet, "/api/v1/pools/summary", userRaw)
	if summary.Code != http.StatusOK {
		t.Fatalf("pool summary status = %d, want 200; body=%s", summary.Code, summary.Body.String())
	}
	var raw []map[string]any
	if err := json.Unmarshal(summary.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode pool summary: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("pool summary len = %d, want 1", len(raw))
	}
	gotKeys := make(map[string]bool, len(raw[0]))
	for k := range raw[0] {
		gotKeys[k] = true
	}
	wantKeys := map[string]bool{"name": true, "type": true, "profile": true}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("pool summary keys = %v, want exactly %v", raw[0], wantKeys)
	}
	for k := range wantKeys {
		if !gotKeys[k] {
			t.Fatalf("pool summary keys = %v, missing %q", raw[0], k)
		}
	}
	if raw[0]["name"] != "web" || raw[0]["type"] != "container" || raw[0]["profile"] != "web" {
		t.Fatalf("pool summary = %+v, want web/container/web", raw[0])
	}

	// The full administrative pool listing, and other admin-only
	// operations, remain denied -- this must not become a privilege
	// escalation.
	if pools := request(http.MethodGet, "/api/v1/pools", userRaw); pools.Code != http.StatusForbidden {
		t.Fatalf("user pools status = %d, want 403; body=%s", pools.Code, pools.Body.String())
	}
	if fill := request(http.MethodPost, "/api/v1/pools/web/fill", userRaw); fill.Code != http.StatusForbidden {
		t.Fatalf("user pool fill status = %d, want 403; body=%s", fill.Code, fill.Body.String())
	}
	if resources := request(http.MethodGet, "/api/v1/resources", userRaw); resources.Code != http.StatusForbidden {
		t.Fatalf("user resources status = %d, want 403; body=%s", resources.Code, resources.Body.String())
	}
}

// TestUserRoleKeyOwnedSandboxLifecycle covers the create/get/exec/delete
// lifecycle for a `user`-role key against sandboxes it owns, and confirms
// the same key still cannot touch another owner's sandbox at any of those
// operations (#359).
func TestUserRoleKeyOwnedSandboxLifecycle(t *testing.T) {
	st := store.NewMemoryStore()
	userRaw, userHash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey user: %v", err)
	}
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "user-1", Hash: userHash, Role: model.APIKeyRoleUser}); err != nil {
		t.Fatalf("PutAPIKey user: %v", err)
	}
	if err := st.PutResource(context.Background(), model.Resource{ID: "res-1", Type: model.ResourceTypeContainer, Profile: "web", State: model.ResourceStateAllocated}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "owned", OwnerID: "user-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}}); err != nil {
		t.Fatalf("CreateSandbox owned: %v", err)
	}
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "other", OwnerID: "user-2", Status: model.SandboxStatusReady}); err != nil {
		t.Fatalf("CreateSandbox other: %v", err)
	}

	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, sandboxMgr: sandbox.New(st, nil), executor: executor, authRequired: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	// Create: 202, owned by the caller.
	create := request(http.MethodPost, "/api/v1/sandboxes", userRaw,
		`{"name":"lab","requests":[{"type":"container","profile":"web","count":1}]}`)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202; body=%s", create.Code, create.Body.String())
	}
	var created model.Sandbox
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created sandbox: %v", err)
	}
	if created.OwnerID != "user-1" {
		t.Fatalf("created sandbox owner = %q, want user-1", created.OwnerID)
	}

	// Get own: 200.
	get := request(http.MethodGet, "/api/v1/sandboxes/owned", userRaw, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get owned status = %d, want 200; body=%s", get.Code, get.Body.String())
	}
	// Get another owner's sandbox: 403.
	if getOther := request(http.MethodGet, "/api/v1/sandboxes/other", userRaw, ""); getOther.Code != http.StatusForbidden {
		t.Fatalf("get other status = %d, want 403; body=%s", getOther.Code, getOther.Body.String())
	}

	// Exec on own: 202.
	exec := request(http.MethodPost, "/api/v1/sandboxes/owned/exec", userRaw, `{"command":["echo","hi"]}`)
	if exec.Code != http.StatusAccepted {
		t.Fatalf("exec own status = %d, want 202; body=%s", exec.Code, exec.Body.String())
	}
	// Exec on another owner's sandbox: 403.
	if execOther := request(http.MethodPost, "/api/v1/sandboxes/other/exec", userRaw, `{"command":["echo","hi"]}`); execOther.Code != http.StatusForbidden {
		t.Fatalf("exec other status = %d, want 403; body=%s", execOther.Code, execOther.Body.String())
	}

	// Delete own: 202.
	del := request(http.MethodDelete, "/api/v1/sandboxes/owned", userRaw, "")
	if del.Code != http.StatusAccepted {
		t.Fatalf("delete own status = %d, want 202; body=%s", del.Code, del.Body.String())
	}
	// Delete another owner's sandbox: 403.
	if delOther := request(http.MethodDelete, "/api/v1/sandboxes/other", userRaw, ""); delOther.Code != http.StatusForbidden {
		t.Fatalf("delete other status = %d, want 403; body=%s", delOther.Code, delOther.Body.String())
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

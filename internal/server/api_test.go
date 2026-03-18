package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestAPI_ListPools_empty(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var pools []model.Pool
	if err := json.Unmarshal(w.Body.Bytes(), &pools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pools) != 0 {
		t.Fatalf("pools len = %d, want 0", len(pools))
	}
}

func TestAPI_ListPools_withData(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.PutPool(ctx, model.Pool{Name: "web"})
	_ = st.PutPool(ctx, model.Pool{Name: "db"})

	mux := server.NewTestMux(st, false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var pools []model.Pool
	if err := json.Unmarshal(w.Body.Bytes(), &pools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("pools len = %d, want 2", len(pools))
	}
}

func TestAPI_ListResources_empty(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var res []model.Resource
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("resources len = %d, want 0", len(res))
	}
}

func TestAPI_ListSandboxes_empty(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var sbs []model.Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &sbs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sbs) != 0 {
		t.Fatalf("sandboxes len = %d, want 0", len(sbs))
	}
}

func TestAPI_ListSandboxes_withData(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "test"})

	mux := server.NewTestMux(st, false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes", nil)
	mux.ServeHTTP(w, r)

	var sbs []model.Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &sbs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sbs) != 1 {
		t.Fatalf("sandboxes len = %d, want 1", len(sbs))
	}
	if sbs[0].ID != "sb-1" {
		t.Fatalf("sandbox id = %q, want %q", sbs[0].ID, "sb-1")
	}
}

// Integration test: full HTTP round-trip via httptest.Server.
func TestAPI_Integration(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.PutPool(ctx, model.Pool{Name: "integ-pool"})

	mux := server.NewTestMux(st, false)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/pools")
	if err != nil {
		t.Fatalf("GET /api/v1/pools: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}

	var pools []model.Pool
	if err := json.NewDecoder(resp.Body).Decode(&pools); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pools) != 1 || pools[0].Name != "integ-pool" {
		t.Fatalf("pools = %v", pools)
	}
}

func TestAPI_ContentType(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	mux.ServeHTTP(w, r)

	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}

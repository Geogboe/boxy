package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestAPI_ListPools_empty(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)

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

	mux := server.NewTestMux(st, sandbox.New(st), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)

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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)

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

	mux := server.NewTestMux(st, sandbox.New(st), false)
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

	mux := server.NewTestMux(st, sandbox.New(st), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	mux.ServeHTTP(w, r)

	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestAPI_GetPool_found(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.PutPool(ctx, model.Pool{Name: "mypool"})

	mux := server.NewTestMux(st, sandbox.New(st), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools/mypool", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var pool model.Pool
	if err := json.Unmarshal(w.Body.Bytes(), &pool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pool.Name != "mypool" {
		t.Fatalf("pool name = %q, want %q", pool.Name, "mypool")
	}
}

func TestAPI_GetPool_notfound(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pools/nonexistent", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPI_GetResource_found(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.PutResource(ctx, model.Resource{ID: "res-1"})

	mux := server.NewTestMux(st, sandbox.New(st), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/resources/res-1", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var res model.Resource
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.ID != "res-1" {
		t.Fatalf("resource id = %q, want %q", res.ID, "res-1")
	}
}

func TestAPI_GetResource_notfound(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/resources/nonexistent", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPI_GetSandbox_found(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-42", Name: "found"})

	mux := server.NewTestMux(st, sandbox.New(st), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-42", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var sb model.Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &sb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sb.ID != "sb-42" {
		t.Fatalf("sandbox id = %q, want %q", sb.ID, "sb-42")
	}
}

func TestAPI_GetSandbox_notfound(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/nonexistent", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPI_CreateSandbox(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st), false)

	body := bytes.NewBufferString(`{"name":"new-box"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var sb model.Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &sb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sb.ID == "" {
		t.Fatal("sandbox id is empty")
	}
	if sb.Name != "new-box" {
		t.Fatalf("sandbox name = %q, want %q", sb.Name, "new-box")
	}
}

func TestAPI_DeleteSandbox_found(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-del", Name: "to-delete"})

	mux := server.NewTestMux(st, sandbox.New(st), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-del", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestAPI_DeleteSandbox_notfound(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/nonexistent", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakePoolMaintenance struct {
	drainPool model.Pool
	fillPool  model.Pool
	drainErr  error
	fillErr   error
	drained   []model.PoolName
	filled    []model.PoolName
}

func (m *fakePoolMaintenance) Drain(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	_ = ctx
	m.drained = append(m.drained, poolName)
	return m.drainPool, m.drainErr
}

func (m *fakePoolMaintenance) Fill(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	_ = ctx
	m.filled = append(m.filled, poolName)
	return m.fillPool, m.fillErr
}

func TestAPI_ListPools_empty(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

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

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

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
	_ = st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-1",
		Name:     "test",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "alpine", Count: 1}},
	})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	if sbs[0].Status != model.SandboxStatusPending {
		t.Fatalf("sandbox status = %q, want %q", sbs[0].Status, model.SandboxStatusPending)
	}
}

// Integration test: full HTTP round-trip via httptest.Server.
func TestAPI_Integration(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.PutPool(ctx, model.Pool{Name: "integ-pool"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

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

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)
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

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)
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
	_ = st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-42",
		Name:     "found",
		Status:   model.SandboxStatusFailed,
		Error:    "pool not found",
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "demo", Count: 1}},
	})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
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
	if sb.Status != model.SandboxStatusFailed {
		t.Fatalf("sandbox status = %q, want %q", sb.Status, model.SandboxStatusFailed)
	}
	if sb.Error != "pool not found" {
		t.Fatalf("sandbox error = %q, want %q", sb.Error, "pool not found")
	}
}

func TestAPI_GetSandbox_notfound(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)
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
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	body := bytes.NewBufferString(`{"name":"new-box","requests":[{"type":"container","profile":"alpine","count":2}]}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
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
	if sb.Status != model.SandboxStatusPending {
		t.Fatalf("sandbox status = %q, want %q", sb.Status, model.SandboxStatusPending)
	}
	if len(sb.Resources) != 0 {
		t.Fatalf("sandbox resources len = %d, want 0", len(sb.Resources))
	}
	if len(sb.Requests) != 1 {
		t.Fatalf("sandbox requests len = %d, want 1", len(sb.Requests))
	}
	if sb.Requests[0].Type != model.ResourceTypeContainer || sb.Requests[0].Profile != "alpine" || sb.Requests[0].Count != 2 {
		t.Fatalf("sandbox request = %+v", sb.Requests[0])
	}
}

func TestAPI_CreateSandbox_requiresName(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	body := bytes.NewBufferString(`{"requests":[{"type":"container","profile":"alpine","count":2}]}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`sandbox name is required`)) {
		t.Fatalf("body = %s, want sandbox name validation error", w.Body.String())
	}
}

func TestAPI_CreateSandbox_requiresRequests(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	body := bytes.NewBufferString(`{"name":"new-box"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreateSandbox_rejectsUnknownFields(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	body := bytes.NewBufferString(`{"name":"new-box","resources":[{"pool":"web","count":1}]}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`invalid request body`)) {
		t.Fatalf("body = %s, want invalid request body error", w.Body.String())
	}
}

func TestAPI_CreateSandbox_validatesRequests(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	body := bytes.NewBufferString(`{"name":"new-box","requests":[{"type":"container","profile":"","count":0}]}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_DeleteSandbox_found(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-del", Name: "to-delete"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-del", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var got model.Sandbox
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ID != "sb-del" || got.Status != model.SandboxStatusDeleting {
		t.Fatalf("sandbox = %+v, want sb-del deleting", got)
	}
}

func TestAPI_DeleteSandbox_alreadyDeletingIsAccepted(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-del", Name: "to-delete", Status: model.SandboxStatusDeleting})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-del", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var got model.Sandbox
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ID != "sb-del" || got.Status != model.SandboxStatusDeleting {
		t.Fatalf("sandbox = %+v, want sb-del deleting", got)
	}
}

func TestAPI_DeleteSandbox_notfound(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/nonexistent", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPI_ExtendSandbox_pushesExpiryOut(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	original := time.Unix(1000, 0).UTC()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "to-extend", Status: model.SandboxStatusReady, ExpiresAt: &original})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	body := bytes.NewBufferString(`{"duration":"15m"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got model.Sandbox
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := original.Add(15 * time.Minute)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, want)
	}
}

func TestAPI_ExtendSandbox_notFound(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	body := bytes.NewBufferString(`{"duration":"15m"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/nonexistent/extend", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_ExtendSandbox_noExpiryIsConflict(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "no-expiry", Status: model.SandboxStatusReady})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	body := bytes.NewBufferString(`{"duration":"15m"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_ExtendSandbox_deletingIsConflict(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	expiry := time.Unix(1000, 0).UTC()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "deleting", Status: model.SandboxStatusDeleting, ExpiresAt: &expiry})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	body := bytes.NewBufferString(`{"duration":"15m"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", body)
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_ExtendSandbox_invalidDuration(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	expiry := time.Unix(1000, 0).UTC()
	_ = st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Name: "to-extend", Status: model.SandboxStatusReady, ExpiresAt: &expiry})

	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	for _, duration := range []string{"not-a-duration", "-15m", "0m", ""} {
		body := bytes.NewBufferString(`{"duration":"` + duration + `"}`)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", body)
		r.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("duration %q: status = %d, want 400; body: %s", duration, w.Code, w.Body.String())
		}
	}
}

func TestAPI_DrainPool(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	maintenance := &fakePoolMaintenance{drainPool: model.Pool{
		Name:  "web",
		Drain: model.PoolDrainState{Operator: true},
	}}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false, maintenance)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/pools/web/drain", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if len(maintenance.drained) != 1 || maintenance.drained[0] != "web" {
		t.Fatalf("drained = %v, want [web]", maintenance.drained)
	}
	var got model.Pool
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Drain.Operator {
		t.Fatalf("operator drain = false, want true")
	}
}

func TestAPI_DrainPool_notFound(t *testing.T) {
	t.Parallel()
	maintenance := &fakePoolMaintenance{drainErr: store.ErrNotFound}
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false, maintenance)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/pools/missing/drain", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestAPI_FillPool_ConfigDeclaredDrain(t *testing.T) {
	t.Parallel()
	maintenance := &fakePoolMaintenance{fillErr: &pool.ConfigDeclaredDrainError{PoolName: "web"}}
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false, maintenance)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/pools/web/fill", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`configured drained`)) {
		t.Fatalf("body = %s, want configured drained message", w.Body.String())
	}
}

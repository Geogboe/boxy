package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// listErrStore wraps a store.Store and fails every List* call, letting tests
// exercise the UI's data-loader-error paths without a real backend outage.
type listErrStore struct {
	store.Store
	err error
}

func (s *listErrStore) ListPools(ctx context.Context) ([]model.Pool, error) {
	return nil, s.err
}

func (s *listErrStore) ListSandboxes(ctx context.Context) ([]model.Sandbox, error) {
	return nil, s.err
}

func (s *listErrStore) ListResources(ctx context.Context) ([]model.Resource, error) {
	return nil, s.err
}

func TestUI_home_renders(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Boxy") {
		t.Fatal("home page missing 'Boxy' brand")
	}
	if !strings.Contains(body, "Overview") {
		t.Fatal("home page missing 'Overview' heading")
	}
}

func TestUI_pools_renders(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "test-pool"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/pools", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test-pool") {
		t.Fatal("pools page missing pool name")
	}
}

func TestUI_sandboxes_renders(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-test", Name: "my-sandbox", Status: model.SandboxStatusReady})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "my-sandbox") {
		t.Fatal("sandboxes page missing sandbox name")
	}
	if !strings.Contains(body, `class="badge badge-ready"`) {
		t.Fatalf("sandboxes page missing status badge, body = %q", body)
	}
}

func TestUI_fragment_stats(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "p1"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/fragments/stats", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "stat-card") {
		t.Fatal("stats fragment missing stat-card")
	}
}

func TestUI_fragment_pools_table(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/fragments/pools-table", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No pools configured") {
		t.Fatal("empty pools fragment missing empty state")
	}
}

func TestUI_fragment_dataError_returns200WithBanner(t *testing.T) {
	t.Parallel()
	failing := &listErrStore{Store: store.NewMemoryStore(), err: errors.New("store unavailable")}
	mux := server.NewTestMux(failing, sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/fragments/sandboxes-table", nil)
	mux.ServeHTTP(w, r)

	// HTMX only swaps 2xx responses by default; a non-2xx status here means
	// a failing 5s poll goes silently unnoticed with no visible indication
	// anything is wrong.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (HTMX won't swap a non-2xx fragment response)", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("body = %q, want an error banner", w.Body.String())
	}
}

func TestUI_page_dataError_returns500Branded(t *testing.T) {
	t.Parallel()
	failing := &listErrStore{Store: store.NewMemoryStore(), err: errors.New("store unavailable")}
	mux := server.NewTestMux(failing, sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Boxy") {
		t.Fatalf("body = %q, want branded HTML (layout), not a plain-text error", body)
	}
	if !strings.Contains(body, "error") {
		t.Fatalf("body = %q, want it to mention an error", body)
	}
}

func TestUI_disabled_returns_404(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mux.ServeHTTP(w, r)

	// When UI is disabled, / should not match any route (404).
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (UI disabled)", w.Code, http.StatusNotFound)
	}
}

func TestUI_static_css(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "--bg:") {
		t.Fatal("CSS missing expected variable")
	}
}

func TestUI_static_htmx(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.Len() < 1000 {
		t.Fatalf("htmx.min.js too small: %d bytes", w.Body.Len())
	}
}

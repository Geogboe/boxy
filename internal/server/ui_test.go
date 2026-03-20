package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestUI_home_renders(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), true)

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

	mux := server.NewTestMux(st, sandbox.New(st), true)
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
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-test", Name: "my-sandbox"})

	mux := server.NewTestMux(st, sandbox.New(st), true)
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
}

func TestUI_fragment_stats(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "p1"})

	mux := server.NewTestMux(st, sandbox.New(st), true)
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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), true)

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

func TestUI_disabled_returns_404(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), false)

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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), true)

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
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore()), true)

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

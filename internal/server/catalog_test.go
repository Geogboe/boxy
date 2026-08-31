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
	"github.com/Geogboe/boxy/pkg/store"
)

func TestUICatalog_RendersSortedSectionsAndMissingReferences(t *testing.T) {
	t.Parallel()
	snapshot := server.CatalogSnapshot{
		Templates: []server.CatalogTemplate{
			{Name: "z-template", Type: "vm", Packages: []string{"z-package@1.0.0"}},
			{Name: "a-template", Type: "container", Source: "missing-source"},
		},
		Packages: []server.CatalogPackage{
			{Name: "z-package", Version: "1.0.0", Method: "shell"},
			{Name: "a-package", Version: "1.0.0", Method: "powershell"},
		},
		Sources: []server.CatalogSourceEntry{{Name: "windows", Store: "missing-store", Path: "windows.vhdx", Digest: "sha256:abc"}},
		Stores:  []server.CatalogStore{{Name: "images", Type: "local"}},
		Pools: []server.CatalogPool{
			{Name: "apps", Template: "missing-template", Source: "missing-source", Packages: []string{"missing-package@1.0.0"}},
		},
	}
	mux := server.NewTestMuxWithCatalog(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), server.NewStaticCatalogSource(snapshot), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/catalog", nil)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Catalog", "Templates", "Packages", "Sources", "Artifact stores", "Pool relationships", "missing-template", "missing-source", "missing-package@1.0.0", "missing-store"} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalog page missing %q, body = %q", want, body)
		}
	}
	if strings.Index(body, "a-template") > strings.Index(body, "z-template") {
		t.Fatal("templates are not rendered in stable sorted order")
	}
	if strings.LastIndex(body, "a-package@1.0.0") > strings.LastIndex(body, "z-package@1.0.0") {
		t.Fatal("packages are not rendered in stable sorted order")
	}
}

func TestUICatalog_RequiresSession(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMuxWithCatalog(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), server.NewStaticCatalogSource(server.CatalogSnapshot{}), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/catalog", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want redirect to login", w.Code)
	}
}

func TestUICatalog_EmptyState(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMuxWithCatalog(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), server.NewStaticCatalogSource(server.CatalogSnapshot{}), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/catalog", nil)))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No catalog entries are configured") {
		t.Fatalf("status = %d, body = %q, want empty catalog state", w.Code, w.Body.String())
	}
}

func TestStaticCatalogSource_ReturnsIndependentSortedCopies(t *testing.T) {
	t.Parallel()
	source := server.NewStaticCatalogSource(server.CatalogSnapshot{
		Templates: []server.CatalogTemplate{{Name: "b"}, {Name: "a"}},
	})

	first, err := source.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("first LoadCatalog: %v", err)
	}
	first.Templates[0].Name = "mutated"
	second, err := source.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("second LoadCatalog: %v", err)
	}
	if len(second.Templates) != 2 || second.Templates[0].Name != "a" {
		t.Fatalf("second snapshot = %#v, want an independent sorted copy", second)
	}
}

type failingCatalogSource struct{}

func (failingCatalogSource) LoadCatalog(context.Context) (server.CatalogSnapshot, error) {
	return server.CatalogSnapshot{}, errors.New("catalog backend leaked-secret unavailable")
}

func TestUICatalog_LoadErrorIsGeneric(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMuxWithCatalog(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), failingCatalogSource{}, true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/catalog", nil)))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Catalog is temporarily unavailable") {
		t.Fatalf("status = %d, body = %q, want generic load-error state", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "leaked-secret") {
		t.Fatalf("catalog load error disclosed internal detail: %q", w.Body.String())
	}
}

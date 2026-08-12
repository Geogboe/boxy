package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/internal/server"
)

func TestCommittedAPIDocsMatchGenerator(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "api.md"))
	if err != nil {
		t.Fatalf("read committed API docs: %v", err)
	}
	want := render(server.APIRouteCatalog())
	if string(data) != want {
		t.Fatal("docs/api.md is stale; run `go generate ./...`")
	}
}

func TestAPIRouteCatalogHasUniqueMethodPaths(t *testing.T) {
	seen := make(map[string]struct{})
	for _, route := range server.APIRouteCatalog() {
		key := route.Method + " " + route.Path
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate API route %q", key)
		}
		seen[key] = struct{}{}
	}
}

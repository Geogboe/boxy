package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommittedHelpTemplateMatchesGenerator(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "docs", "help", "index.md"))
	if err != nil {
		t.Fatalf("read help source: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "internal", "server", "templates", "help.html"))
	if err != nil {
		t.Fatalf("read committed help template: %v", err)
	}
	want, err := render(source)
	if err != nil {
		t.Fatalf("render help source: %v", err)
	}
	if string(committed) != want {
		t.Fatal("internal/server/templates/help.html is stale; run `go generate ./...`")
	}
}

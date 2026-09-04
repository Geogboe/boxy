package server_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUIDesignLanguageDocumentsStableSurface(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))

	design, err := os.ReadFile(filepath.Join(root, "docs", "ui-design-language.md"))
	if err != nil {
		t.Fatalf("read UI design language: %v", err)
	}
	for _, heading := range []string{
		"# Boxy web UI design language",
		"## Foundations",
		"## Layout and navigation",
		"## Component vocabulary",
		"## Accessibility and responsive behavior",
	} {
		if !strings.Contains(string(design), heading) {
			t.Errorf("design language missing %q", heading)
		}
	}

	css, err := os.ReadFile(filepath.Join(root, "internal", "server", "static", "style.css"))
	if err != nil {
		t.Fatalf("read UI stylesheet: %v", err)
	}
	for _, selector := range []string{
		":root[data-theme=\"light\"]",
		".layout",
		".table-card",
		".button-link",
		".badge",
		".quick-link-card",
	} {
		if !strings.Contains(string(css), selector) {
			t.Errorf("stylesheet missing documented selector %q", selector)
		}
	}
}

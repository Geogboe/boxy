package server_test

import (
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestREADMEReferencesValidDashboardScreenshot(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	assetPath := filepath.Join(root, "docs", "assets", "boxy-dashboard.png")
	asset, err := os.Open(assetPath)
	if err != nil {
		t.Fatalf("open dashboard screenshot: %v", err)
	}
	defer func() { _ = asset.Close() }()
	info, err := asset.Stat()
	if err != nil {
		t.Fatalf("stat dashboard screenshot: %v", err)
	}
	if info.Size() == 0 || info.Size() > 250<<10 {
		t.Fatalf("dashboard screenshot size = %d bytes, want 1..256 KiB", info.Size())
	}
	image, err := png.Decode(asset)
	if err != nil {
		t.Fatalf("decode dashboard screenshot: %v", err)
	}
	if image.Bounds().Dx() < 1000 || image.Bounds().Dy() < 600 {
		t.Fatalf("dashboard screenshot dimensions = %v, want desktop capture", image.Bounds())
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), "docs/assets/boxy-dashboard.png") {
		t.Fatal("README does not reference the dashboard screenshot")
	}
}

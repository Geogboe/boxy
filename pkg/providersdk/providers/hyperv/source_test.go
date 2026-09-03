package hyperv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestMaterializeSourceDownloadsVHDXAndVerifiesDigest(t *testing.T) {
	t.Parallel()
	payload := []byte("hyper-v disk")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	path, err := materializeSource(context.Background(), &providersdk.SourceDescriptor{
		URL:       server.URL,
		Digest:    "sha256:" + hex.EncodeToString(sum[:]),
		Format:    "vhdx",
		ExpiresAt: time.Now().Add(time.Minute),
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".vhdx") {
		t.Fatalf("path = %q, want .vhdx", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded bytes = %q, want %q", got, payload)
	}
}

func TestMaterializeSourceRejectsUnsupportedFormat(t *testing.T) {
	t.Parallel()
	_, err := materializeSource(context.Background(), &providersdk.SourceDescriptor{
		Path:   filepath.Join(t.TempDir(), "disk.iso"),
		Digest: "sha256:" + strings.Repeat("a", 64),
		Format: "iso",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported source format") {
		t.Fatalf("error = %v", err)
	}
}

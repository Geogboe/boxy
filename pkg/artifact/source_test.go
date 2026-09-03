package artifact

import (
	"bytes"
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
)

func TestPullSourceCopiesAndVerifiesLocalPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.vhdx")
	payload := []byte("vhdx bytes")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	descriptor := SourceDescriptor{Path: sourcePath, Digest: "sha256:" + hex.EncodeToString(digest[:]), Format: "vhdx"}
	destination := filepath.Join(dir, "cache", "source.vhdx")
	if err := PullSource(context.Background(), descriptor, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("destination = %q, want %q", got, payload)
	}
}

func TestPullSourceFailsClosedOnExpiryAndDigestMismatch(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("expected"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("wrong"))
	}))
	defer server.Close()

	expired := SourceDescriptor{URL: server.URL, Digest: "sha256:" + hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(-time.Second)}
	if err := PullSource(context.Background(), expired, filepath.Join(t.TempDir(), "expired")); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired pull error = %v", err)
	}

	destination := filepath.Join(t.TempDir(), "mismatch")
	active := SourceDescriptor{URL: server.URL, Digest: "sha256:" + hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(time.Minute)}
	if err := PullSource(context.Background(), active, destination); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("mismatch pull error = %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("mismatched destination stat error = %v, want not exist", err)
	}
}

func TestPullSourceHonorsCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	digest := sha256.Sum256(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := PullSource(ctx, SourceDescriptor{URL: server.URL, Digest: "sha256:" + hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(time.Minute)}, filepath.Join(t.TempDir(), "cancelled"))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancelled pull error = %v", err)
	}
}

func TestPullSourceRejectsConfiguredMaximum(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.bin")
	if err := os.WriteFile(sourcePath, []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(bytes.TrimSpace([]byte("four")))
	destination := filepath.Join(root, "materialized.bin")
	err := PullSourceWithOptions(context.Background(), SourceDescriptor{
		Path:   sourcePath,
		Digest: "sha256:" + hex.EncodeToString(hash[:]),
	}, destination, PullOptions{MaxBytes: 3})
	if err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("PullSourceWithOptions() error = %v, want maximum-size failure", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination stat error = %v, want destination to remain absent", statErr)
	}
}

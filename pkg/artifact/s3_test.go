package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		value, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(value)
	case http.MethodPut:
		value, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("If-Match") == "*" && f.objects[key] != nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		f.objects[key] = value
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestS3RegistryPublishesImmutableArtifactsAndSourceMetadata(t *testing.T) {
	fake := &fakeS3{objects: make(map[string][]byte)}
	server := httptest.NewServer(fake)
	defer server.Close()
	registry, err := NewS3Registry(S3RegistryConfig{
		Endpoint: server.URL, Bucket: "test-bucket", Region: "us-east-1",
		UsePathStyle: true, AccessKey: "test", SecretKey: "secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	value := Artifact{Type: ArtifactTypePackage, Ref: Ref{Name: "base", Version: "1.0.0"}, Manifest: []byte("name: base\n")}
	if err := registry.Publish(ctx, value); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := registry.Publish(ctx, value); err != nil {
		t.Fatalf("identical Publish: %v", err)
	}
	value.Manifest = []byte("different")
	if err := registry.Publish(ctx, value); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("conflicting Publish error = %v, want immutable error", err)
	}
	got, err := registry.Resolve(ctx, Ref{Type: ArtifactTypePackage, Name: "base", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got.Manifest) != "name: base\n" {
		t.Fatalf("manifest = %q, want original content", got.Manifest)
	}

	body := []byte("source bytes")
	sum := sha256.Sum256(body)
	source := Source{Name: "base-image", Store: "images", Path: "images/base.vhdx", Digest: "sha256:" + hex.EncodeToString(sum[:]), Format: "hyperv-vhdx"}
	if err := registry.PutSource(ctx, source); err != nil {
		t.Fatalf("PutSource: %v", err)
	}
	if err := registry.PutSource(ctx, source); err != nil {
		t.Fatalf("identical PutSource: %v", err)
	}
	gotSource, err := registry.ResolveSource(ctx, source.Name)
	if err != nil || !reflect.DeepEqual(gotSource, source) {
		t.Fatalf("ResolveSource = %#v, %v; want %#v", gotSource, err, source)
	}
}

func TestS3RegistrySignsObjectSpecificSourceURL(t *testing.T) {
	fake := &fakeS3{objects: make(map[string][]byte)}
	server := httptest.NewServer(fake)
	defer server.Close()
	registry, err := NewS3Registry(S3RegistryConfig{
		Endpoint: server.URL, Bucket: "test-bucket", Region: "us-east-1",
		UsePathStyle: true, AccessKey: "test", SecretKey: "secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	source := Source{Name: "disk", Store: "images", Path: "owned/disk.vhdx", Digest: digest, Format: "hyperv-vhdx"}
	if err := registry.PutSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	descriptor, err := registry.ResolveSourceDescriptor(context.Background(), source.Name, time.Minute)
	if err != nil {
		t.Fatalf("ResolveSourceDescriptor: %v", err)
	}
	if descriptor.URL == "" || !strings.Contains(descriptor.URL, "/owned/disk.vhdx") {
		t.Fatalf("descriptor URL = %q, want signed owned object URL", descriptor.URL)
	}
	if descriptor.Path != "" || descriptor.Digest != digest || descriptor.ExpiresAt.IsZero() {
		t.Fatalf("descriptor = %#v, want URL-only descriptor with digest and expiry", descriptor)
	}
}

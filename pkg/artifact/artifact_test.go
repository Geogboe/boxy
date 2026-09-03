package artifact

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	ref, err := ParseRef("app3@1.0.0")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	if ref.Name != "app3" || ref.Version != "1.0.0" {
		t.Fatalf("ref = %#v, want app3@1.0.0", ref)
	}
	if _, err := ParseRef("app3"); err == nil {
		t.Fatal("ParseRef without version succeeded, want immutable version error")
	}
}

func TestMemoryRegistryPublishesImmutableArtifacts(t *testing.T) {
	ctx := context.Background()
	reg := NewMemoryRegistry()
	ref := Ref{Type: ArtifactTypePackage, Name: "app3", Version: "1.0.0"}
	want := Artifact{Type: ArtifactTypePackage, Ref: ref, Manifest: []byte("name: app3\nversion: 1.0.0\n")}
	if err := reg.Publish(ctx, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := reg.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got.Manifest) != string(want.Manifest) {
		t.Fatalf("manifest = %q, want %q", got.Manifest, want.Manifest)
	}
	got.Manifest[0] = 'X'
	again, err := reg.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve after mutation: %v", err)
	}
	if strings.HasPrefix(string(again.Manifest), "X") {
		t.Fatal("Resolve returned mutable registry storage")
	}
	if err := reg.Publish(ctx, Artifact{Type: ArtifactTypePackage, Ref: ref, Manifest: []byte("different")}); err == nil {
		t.Fatal("conflicting republish succeeded")
	}
}

func TestMemoryRegistrySources(t *testing.T) {
	ctx := context.Background()
	reg := NewMemoryRegistry()
	want := Source{Name: "windows-2022", Store: "images", Path: "base/windows-2022.vhdx", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Format: "hyperv-vhdx"}
	if err := reg.PutSource(ctx, want); err != nil {
		t.Fatalf("PutSource: %v", err)
	}
	got, err := reg.ResolveSource(ctx, want.Name)
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source = %#v, want %#v", got, want)
	}
}

func TestDirectoryRegistryPersistsImmutableArtifacts(t *testing.T) {
	ctx := context.Background()
	reg, err := NewDirectoryRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := Artifact{
		Type:     ArtifactTypePackage,
		Ref:      Ref{Type: ArtifactTypePackage, Name: "baseline", Version: "1.0.0"},
		Manifest: []byte("name: baseline\nversion: 1.0.0\n"),
		Blobs:    map[string][]byte{"script": []byte("echo ready")},
	}
	if err := reg.Publish(ctx, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	other, err := NewDirectoryRegistry(reg.Root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := other.Resolve(ctx, want.Ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("artifact = %#v, want %#v", got, want)
	}
	want.Manifest = []byte("changed")
	if err := reg.Publish(ctx, want); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("republish error = %v, want immutable error", err)
	}
}

func TestDirectoryRegistryRejectsInvalidArtifactTypes(t *testing.T) {
	reg, err := NewDirectoryRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, artifactType := range []ArtifactType{"../outside", "custom"} {
		err := reg.Publish(context.Background(), Artifact{
			Type:     artifactType,
			Ref:      Ref{Name: "baseline", Version: "1.0.0"},
			Manifest: []byte("name: baseline\nversion: 1.0.0\n"),
		})
		if err == nil {
			t.Fatalf("Publish with artifact type %q succeeded", artifactType)
		}
	}
}

func TestDirectoryRegistryRejectsSourceOutsideRoot(t *testing.T) {
	reg, err := NewDirectoryRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.SignSource(context.Background(), Source{
		Path:   filepath.Join(reg.Root, "..", "outside.vhdx"),
		Digest: "sha256:" + strings.Repeat("0", 64),
	}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "outside artifact registry directory") {
		t.Fatalf("SignSource() error = %v, want registry containment failure", err)
	}
}

func TestValidateDigestRejectsMalformedValues(t *testing.T) {
	for _, digest := range []string{"", "sha1:abc", "sha256:abc", "sha256:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		if err := ValidateDigest(digest); err == nil {
			t.Errorf("ValidateDigest(%q) succeeded", digest)
		}
	}
}

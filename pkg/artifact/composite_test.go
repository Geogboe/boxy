package artifact

import (
	"context"
	"strings"
	"testing"
)

func TestCompositeRegistryRejectsConflictingImmutableContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	left := NewMemoryRegistry()
	right := NewMemoryRegistry()
	value := Artifact{Type: ArtifactTypePackage, Ref: Ref{Type: ArtifactTypePackage, Name: "tools", Version: "1.0.0"}, Manifest: []byte("method: shell")}
	if err := left.Publish(ctx, value); err != nil {
		t.Fatal(err)
	}
	value.Manifest = []byte("method: powershell")
	if err := right.Publish(ctx, value); err != nil {
		t.Fatal(err)
	}
	_, err := NewCompositeRegistry(left, right).Resolve(ctx, value.Ref)
	if err == nil || !strings.Contains(err.Error(), "conflicting content") {
		t.Fatalf("Resolve error = %v", err)
	}
}

func TestCompositeRegistryResolvesIdenticalImmutableContentOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	left := NewMemoryRegistry()
	right := NewMemoryRegistry()
	value := Source{Name: "base", Store: "images", Path: "base.vhdx", Digest: "sha256:" + strings.Repeat("a", 64)}
	if err := left.PutSource(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := right.PutSource(ctx, value); err != nil {
		t.Fatal(err)
	}
	got, err := NewCompositeRegistry(left, right).ResolveSource(ctx, value.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != value.Name || got.Store != value.Store || got.Path != value.Path || got.Digest != value.Digest {
		t.Fatalf("source = %#v, want %#v", got, value)
	}
}

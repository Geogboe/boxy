package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/artifact"
)

type testSourceSigner struct {
	descriptor artifact.SourceDescriptor
}

func (s testSourceSigner) SignSource(context.Context, artifact.Source, time.Duration) (artifact.SourceDescriptor, error) {
	return s.descriptor, nil
}

func TestAgentProvisionerConfigForSourceInjectsDescriptorWithoutMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("a", 64)
	registry := artifact.NewMemoryRegistry()
	if err := registry.PutSource(ctx, artifact.Source{
		Name: "base", Store: "images", Path: "base.vhdx", Digest: digest,
		Format: "vhdx",
	}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute)
	ap := &AgentProvisioner{
		ArtifactRegistry: registry,
		SourceSigners: map[string]artifact.SourceSigner{"images": testSourceSigner{descriptor: artifact.SourceDescriptor{
			URL: "https://objects.example.invalid/base.vhdx", Digest: digest, Format: "vhdx", ExpiresAt: expires,
		}}},
		SourceTTL: time.Minute,
	}
	spec := boxyconfig.PoolSpec{Source: "base", Config: map[string]any{"memory_mb": 1024}}
	got, err := ap.configForSource(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Config["source"]; ok {
		t.Fatal("source was added to the original provider config")
	}
	descriptor, ok := got["source"].(artifact.SourceDescriptor)
	if !ok || descriptor.URL == "" || descriptor.Path != "" {
		t.Fatalf("injected source = %#v", got["source"])
	}
	if got["memory_mb"] != 1024 {
		t.Fatalf("provider config was not preserved: %#v", got)
	}
}

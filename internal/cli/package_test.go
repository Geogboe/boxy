package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/pkg/artifact"
)

func TestPackageBuildAndPublish(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "package.yaml")
	scriptPath := filepath.Join(dir, "install.sh")
	artifactPath := filepath.Join(dir, "out", "package.json")
	if err := os.WriteFile(manifestPath, []byte("name: baseline\nversion: 1.0.0\nmethod: shell\nscopes: [resource]\nevents: [provision]\ninputs:\n  script: install.sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("echo baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runPackageBuild(context.Background(), manifestPath, artifactPath); err != nil {
		t.Fatalf("runPackageBuild: %v", err)
	}
	value, err := readPackageArtifact(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(value.Blobs["install.sh"]) != "echo baseline\n" {
		t.Fatalf("script blob = %q", value.Blobs["install.sh"])
	}
	registryPath := filepath.Join(dir, "registry")
	if err := runPackagePublish(context.Background(), artifactPath, registryPath); err != nil {
		t.Fatalf("runPackagePublish: %v", err)
	}
	registry, err := artifact.NewDirectoryRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := registry.Resolve(context.Background(), value.Ref)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 {
		t.Fatal("published artifact encoded as empty JSON")
	}
}

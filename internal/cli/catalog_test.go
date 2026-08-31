package cli

import (
	"fmt"
	"strings"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/resourcepack"
)

func TestCatalogSnapshotFromConfig_AllowlistsAndSorts(t *testing.T) {
	t.Parallel()
	secret := "catalog-secret-value"
	cfg := boxyconfig.Config{
		Templates: map[string]boxyconfig.TemplateSpec{
			"z-template": {Type: "vm", Config: map[string]any{"password": secret}},
			"a-template": {Type: "container", Source: "source-a"},
		},
		Packages: map[string]resourcepack.Manifest{
			"z-package": {Name: "z-package", Version: "1.0.0", Method: resourcepack.MethodShell, Scopes: []resourcepack.Scope{resourcepack.ScopeResource}, Events: []resourcepack.Event{resourcepack.EventProvision}, Inputs: map[string]any{"token": secret}},
			"a-package": {Name: "a-package", Version: "1.0.0", Method: resourcepack.MethodPowerShell, Scopes: []resourcepack.Scope{resourcepack.ScopeResource}, Events: []resourcepack.Event{resourcepack.EventProvision}},
		},
		Sources: map[string]boxyconfig.SourceSpec{
			"source-a": {Store: "store-a", Path: "image.vhdx", Digest: "sha256:abc", Metadata: map[string]string{"token": secret}},
		},
		ArtifactStores: map[string]boxyconfig.ArtifactStoreSpec{
			"store-a": {Type: "s3", Endpoint: "https://example.invalid", AccessKey: "env:ACCESS", SecretKey: secret},
		},
	}

	snapshot := catalogSnapshotFromConfig(cfg, nil)
	if len(snapshot.Templates) != 2 || snapshot.Templates[0].Name != "a-template" {
		t.Fatalf("templates = %#v, want sorted snapshot", snapshot.Templates)
	}
	if len(snapshot.Packages) != 2 || snapshot.Packages[0].Name != "a-package" {
		t.Fatalf("packages = %#v, want sorted snapshot", snapshot.Packages)
	}
	if len(snapshot.Sources) != 1 || len(snapshot.Stores) != 1 {
		t.Fatalf("snapshot = %#v, want source and store", snapshot)
	}
	if strings.Contains(fmt.Sprintf("%#v", snapshot), secret) || strings.Contains(fmt.Sprintf("%#v", snapshot), "env:ACCESS") {
		t.Fatal("snapshot exposed secret-bearing configuration")
	}
}

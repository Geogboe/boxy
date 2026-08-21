package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestMigrateSecretsMovesLegacyCredentialAfterVerification(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "boxy.yaml")
	config := []byte("server:\n  secrets:\n    backend: file\n    path: secrets.json\n")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	statePath := filepath.Join(dir, ".boxy", "state.json")
	state, err := store.NewDiskStore(statePath)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	poolName := model.PoolName("win-vm")
	const credential = "${BOXY_TEST_PASSWORD}"
	if err := state.PutPoolGuestCredential(context.Background(), poolName, credential); err != nil {
		t.Fatalf("PutPoolGuestCredential: %v", err)
	}

	root := NewRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"migrate", "secrets", "--config", configPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("migrate secrets: %v", err)
	}
	if got := output.String(); got == "" {
		t.Fatal("migrate secrets produced no output")
	}

	reopened, err := store.NewDiskStore(statePath)
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	if _, err := reopened.GetPoolGuestCredential(context.Background(), poolName); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("legacy credential error = %v, want ErrNotFound", err)
	}
	secretStore, err := boxysecrets.Open(boxysecrets.Config{Backend: boxysecrets.BackendFile, Path: filepath.Join(dir, ".boxy", "secrets.json")})
	if err != nil {
		t.Fatalf("open migrated secret store: %v", err)
	}
	got, err := secretStore.Get(context.Background(), boxysecrets.PoolBootstrapKey(string(poolName)))
	if err != nil {
		t.Fatalf("get migrated credential: %v", err)
	}
	if string(got) != credential {
		t.Fatalf("migrated credential = %q, want %q", got, credential)
	}
}

package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenRequiresExplicitBackend(t *testing.T) {
	if _, err := Open(Config{}); !errors.Is(err, ErrBackendRequired) {
		t.Fatalf("Open with empty config error = %v, want ErrBackendRequired", err)
	}
}

func TestFileStoreRoundTripAndUsesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s, err := Open(Config{Backend: BackendFile, Path: path})
	if err != nil {
		t.Fatalf("Open file backend: %v", err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, "resource/res-1", []byte("${BOXY_TEST_PASSWORD}")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "resource/res-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "${BOXY_TEST_PASSWORD}" {
		t.Fatalf("secret = %q, want test secret", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSecretKeysAreStable(t *testing.T) {
	if got := PoolBootstrapKey("win-vm"); got != "pool/win-vm/bootstrap" {
		t.Fatalf("PoolBootstrapKey = %q", got)
	}
	if got := ResourceCredentialKey("res-1"); got != "resource/res-1/guest-credential" {
		t.Fatalf("ResourceCredentialKey = %q", got)
	}
}

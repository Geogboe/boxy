package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/auth"
)

func TestBootstrapLocalAdmin_FirstRunGeneratesAndPersistsAccount(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "boxy.yaml")
	st, statePath, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore: %v", err)
	}

	bootstrapped, err := bootstrapLocalAdmin(context.Background(), st, statePath)
	if err != nil {
		t.Fatalf("bootstrapLocalAdmin: %v", err)
	}
	if !bootstrapped {
		t.Fatal("bootstrapLocalAdmin = false on first run, want true")
	}

	account, err := st.GetLocalAdmin(context.Background())
	if err != nil {
		t.Fatalf("GetLocalAdmin: %v", err)
	}
	if account.Username != "admin" {
		t.Fatalf("username = %q, want admin", account.Username)
	}

	passwordPath, err := serveBootstrapPasswordPath(cfgPath)
	if err != nil {
		t.Fatalf("serveBootstrapPasswordPath: %v", err)
	}
	raw, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read bootstrap password file: %v", err)
	}
	rawPassword := strings.TrimSpace(string(raw))
	if rawPassword == "" {
		t.Fatal("bootstrap password file is empty")
	}
	if !auth.VerifyPassword(account.PasswordHash, rawPassword) {
		t.Fatal("bootstrap password file does not match the persisted account hash")
	}
}

func TestBootstrapLocalAdmin_SecondRunIsANoOp(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "boxy.yaml")
	st, statePath, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore: %v", err)
	}

	if _, err := bootstrapLocalAdmin(context.Background(), st, statePath); err != nil {
		t.Fatalf("bootstrapLocalAdmin(first): %v", err)
	}
	before, err := st.GetLocalAdmin(context.Background())
	if err != nil {
		t.Fatalf("GetLocalAdmin: %v", err)
	}

	bootstrapped, err := bootstrapLocalAdmin(context.Background(), st, statePath)
	if err != nil {
		t.Fatalf("bootstrapLocalAdmin(second): %v", err)
	}
	if bootstrapped {
		t.Fatal("bootstrapLocalAdmin = true on second run, want false (already bootstrapped)")
	}
	after, err := st.GetLocalAdmin(context.Background())
	if err != nil {
		t.Fatalf("GetLocalAdmin: %v", err)
	}
	if before.PasswordHash != after.PasswordHash {
		t.Fatal("second bootstrapLocalAdmin call changed the persisted account hash")
	}
}

func TestBootstrapPasswordCommand_ShowsAndClearsPassword(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(cfgPath, []byte("providers: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	st, statePath, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore: %v", err)
	}
	if _, err := bootstrapLocalAdmin(context.Background(), st, statePath); err != nil {
		t.Fatalf("bootstrapLocalAdmin: %v", err)
	}

	cmd := NewRootCommand()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetArgs([]string{"admin", "bootstrap-password", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "username: admin") || !strings.Contains(out.String(), "password: ") {
		t.Fatalf("output = %q, want username/password lines", out.String())
	}

	passwordPath, err := serveBootstrapPasswordPath(cfgPath)
	if err != nil {
		t.Fatalf("serveBootstrapPasswordPath: %v", err)
	}
	if _, err := os.Stat(passwordPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap password file still exists after being shown: %v", err)
	}

	// Running it again with nothing pending must fail, not silently succeed.
	cmd2 := NewRootCommand()
	cmd2.SetArgs([]string{"admin", "bootstrap-password", "--config", cfgPath})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("second run succeeded, want an error since the password was already retrieved")
	}
}

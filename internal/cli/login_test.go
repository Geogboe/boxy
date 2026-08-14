package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/spf13/cobra"
)

func TestPromptAPIKeyTerminalUsesInteractivePrompt(t *testing.T) {
	oldPrompt := loginInteractivePrompt
	oldIsTerminal := loginIsTerminal
	loginInteractivePrompt = func(*cobra.Command) (string, error) {
		return "interactive-key", nil
	}
	loginIsTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() {
		loginInteractivePrompt = oldPrompt
		loginIsTerminal = oldIsTerminal
	})

	cmd := &cobra.Command{}
	cmd.SetIn(os.Stdin)
	got, err := promptAPIKey(cmd)
	if err != nil {
		t.Fatalf("promptAPIKey: %v", err)
	}
	if got != "interactive-key" {
		t.Fatalf("promptAPIKey = %q, want interactive-key", got)
	}
}

func TestPromptAPIKeyTerminalPropagatesCancellation(t *testing.T) {
	oldPrompt := loginInteractivePrompt
	oldIsTerminal := loginIsTerminal
	loginInteractivePrompt = func(*cobra.Command) (string, error) {
		return "", errLoginPromptCanceled
	}
	loginIsTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() {
		loginInteractivePrompt = oldPrompt
		loginIsTerminal = oldIsTerminal
	})

	cmd := &cobra.Command{}
	cmd.SetIn(os.Stdin)
	_, err := promptAPIKey(cmd)
	if !errors.Is(err, errLoginPromptCanceled) {
		t.Fatalf("promptAPIKey error = %v, want %v", err, errLoginPromptCanceled)
	}
}

func TestRunLoginStoresCredentialAfterVerification(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer boxy_test_key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()

	backend := &testCredentialBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("boxy", backend)
	oldFactory := loginCredentialsStore
	loginCredentialsStore = func() *credentials.Store { return store }
	t.Cleanup(func() { loginCredentialsStore = oldFactory })

	if err := runLogin(context.Background(), loginOptions{
		server:   server.URL,
		apiKey:   "boxy_test_key",
		insecure: true,
	}, &strings.Builder{}, &strings.Builder{}); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	got, err := store.Get(server.URL)
	if err != nil {
		t.Fatalf("stored credential: %v", err)
	}
	if got != "boxy_test_key" {
		t.Fatalf("stored credential = %q, want boxy_test_key", got)
	}
	cfg, err := loadClientConfig()
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}
	if cfg.Server != server.URL {
		t.Fatalf("client server default = %q, want %q", cfg.Server, server.URL)
	}
}

func TestRunLogoutDeletesCredential(t *testing.T) {
	backend := &testCredentialBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("boxy", backend)
	if err := store.Set("http://boxy.example:9090", "boxy_test_key"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	oldFactory := loginCredentialsStore
	loginCredentialsStore = func() *credentials.Store { return store }
	t.Cleanup(func() { loginCredentialsStore = oldFactory })

	if err := runLogout(context.Background(), "http://boxy.example:9090", &strings.Builder{}); err != nil {
		t.Fatalf("runLogout: %v", err)
	}
	if _, err := store.Get("http://boxy.example:9090"); err == nil {
		t.Fatal("credential still exists after logout")
	}
}

func TestLoginCommandExists(t *testing.T) {
	root := NewRootCommand()
	if _, _, err := root.Find([]string{"login"}); err != nil {
		t.Fatalf("find login command: %v", err)
	}
}

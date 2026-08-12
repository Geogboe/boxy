package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/credentials"
)

func TestRunLoginStoresCredentialAfterVerification(t *testing.T) {
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

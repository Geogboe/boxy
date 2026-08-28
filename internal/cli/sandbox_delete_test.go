package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestSandboxDelete_RemovesSavedGuestCredentials(t *testing.T) {
	backend := &sandboxExecFakeBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("test", backend)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1","res-2"]}`)
	}))
	defer server.Close()

	for _, resourceID := range []string{"res-1", "res-2"} {
		credential := providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"password":"${BOXY_TEST_PASSWORD}"}`)}
		if err := store.SetGuestCredential(server.URL, "sb-1", resourceID, credential); err != nil {
			t.Fatalf("SetGuestCredential(%s): %v", resourceID, err)
		}
	}

	previousStore := guestCredentialStore
	guestCredentialStore = func() *credentials.Store { return store }
	t.Cleanup(func() { guestCredentialStore = previousStore })

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", server.URL, "delete", "sb-1", "--no-wait"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, resourceID := range []string{"res-1", "res-2"} {
		if _, err := store.GetGuestCredential(server.URL, "sb-1", resourceID); !errors.Is(err, credentials.ErrNotFound) {
			t.Fatalf("GetGuestCredential(%s) after delete = %v, want ErrNotFound", resourceID, err)
		}
	}
}

func TestSandboxDelete_NoSavedCredentialIsNotAnError(t *testing.T) {
	backend := &sandboxExecFakeBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("test", backend)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
	}))
	defer server.Close()

	previousStore := guestCredentialStore
	guestCredentialStore = func() *credentials.Store { return store }
	t.Cleanup(func() { guestCredentialStore = previousStore })

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", server.URL, "delete", "sb-1", "--no-wait"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v, want success when the sandbox was never created with --save-guest-cred", err)
	}
}

func TestSandboxDelete_CredentialCleanupFailureIsAWarningNotAFatalError(t *testing.T) {
	backend := &sandboxDeleteFailingBackend{}
	store := credentials.NewWithBackend("test", backend)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
	}))
	defer server.Close()

	previousStore := guestCredentialStore
	guestCredentialStore = func() *credentials.Store { return store }
	t.Cleanup(func() { guestCredentialStore = previousStore })

	cmd := NewRootCommand()
	stderr := &strings.Builder{}
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sandbox", "--server", server.URL, "delete", "sb-1", "--no-wait"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v, want a keyring backend failure to warn rather than fail the delete", err)
	}
	if !strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("stderr = %q, want a cleanup warning", stderr.String())
	}
}

func TestSandboxDelete_ShowsResourceDestroyProgress(t *testing.T) {
	var mu sync.Mutex
	getCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			_, _ = io.WriteString(w, `{"id":"sb-1","status":"deleting","resources":["res-1","res-2","res-3"]}`)
		case http.MethodGet:
			mu.Lock()
			getCalls++
			call := getCalls
			mu.Unlock()
			if call == 1 {
				// One resource has been destroyed since the delete request
				// was accepted: 2 of 3 remain.
				_, _ = io.WriteString(w, `{"id":"sb-1","status":"deleting","resources":["res-1","res-2"]}`)
				return
			}
			// Fully cleaned up and purged from the store.
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"sandbox not found"}`)
		default:
			t.Fatalf("unexpected method %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cmd := NewRootCommand()
	stdout := &strings.Builder{}
	cmd.SetOut(stdout)
	cmd.SetArgs([]string{"sandbox", "--server", server.URL, "delete", "sb-1"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "1/3 resource(s) destroyed") {
		t.Fatalf("output = %q, want progress showing 1/3 resources destroyed", out)
	}
	if !strings.Contains(out, "deleted sandbox sb-1") {
		t.Fatalf("output = %q, want final deletion confirmation", out)
	}
}

// sandboxDeleteFailingBackend simulates an inaccessible OS keyring (e.g. a
// locked/headless secret service) so credential cleanup fails for a reason
// other than "nothing was ever saved".
type sandboxDeleteFailingBackend struct{}

func (sandboxDeleteFailingBackend) Get(service, user string) (string, error) {
	return "", errors.New("keyring unavailable")
}

func (sandboxDeleteFailingBackend) Set(service, user, value string) error {
	return errors.New("keyring unavailable")
}

func (sandboxDeleteFailingBackend) Delete(service, user string) error {
	return errors.New("keyring unavailable")
}

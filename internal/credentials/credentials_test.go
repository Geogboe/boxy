package credentials

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeBackend struct {
	values map[string]string
}

func (b *fakeBackend) Get(service, user string) (string, error) {
	value, ok := b.values[service+"\x00"+user]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (b *fakeBackend) Set(service, user, value string) error {
	b.values[service+"\x00"+user] = value
	return nil
}

func (b *fakeBackend) Delete(service, user string) error {
	key := service + "\x00" + user
	if _, ok := b.values[key]; !ok {
		return ErrNotFound
	}
	delete(b.values, key)
	return nil
}

func TestKeyringStoreRoundTrip(t *testing.T) {
	backend := &fakeBackend{values: make(map[string]string)}
	store := NewWithBackend("boxy", backend)
	server := "HTTPS://Boxy.Example:9090/"

	if err := store.Set(server, "${BOXY_TEST_SECRET}"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(server)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "${BOXY_TEST_SECRET}" {
		t.Fatalf("Get = %q, want ${BOXY_TEST_SECRET}", got)
	}
	if err := store.Delete(server); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(server); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestKeyringStoreRejectsEmptyInputs(t *testing.T) {
	store := NewWithBackend("boxy", &fakeBackend{values: make(map[string]string)})
	for _, tt := range []struct {
		name string
		call func() error
	}{
		{"get server", func() error { _, err := store.Get(" "); return err }},
		{"set server", func() error { return store.Set(" ", "${BOXY_TEST_SECRET}") }},
		{"set value", func() error { return store.Set("https://boxy.example", " ") }},
		{"delete server", func() error { return store.Delete(" ") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("call succeeded, want validation error")
			}
		})
	}
}

func TestKeyringStoreCertificateRoundTrip(t *testing.T) {
	backend := &fakeBackend{values: make(map[string]string)}
	store := NewWithBackend("boxy", backend)
	server := "https://boxy.example.test:9090"
	if err := store.SetCA(server, []byte("certificate")); err != nil {
		t.Fatalf("SetCA: %v", err)
	}
	got, err := store.GetCA(server)
	if err != nil {
		t.Fatalf("GetCA: %v", err)
	}
	if string(got) != "certificate" {
		t.Fatalf("GetCA = %q, want certificate", got)
	}
}

func TestKeyringStoreGuestCredentialRoundTripUsesSeparateNamespace(t *testing.T) {
	backend := &fakeBackend{values: make(map[string]string)}
	store := NewWithBackend("boxy", backend)
	credential := providersdk.GuestCredential{
		Kind: "password",
		Data: json.RawMessage(`{"username":"Administrator","password":"rotated"}`),
	}
	if err := store.SetGuestCredential("https://boxy.example:9090", "sb-1", "res-1", credential); err != nil {
		t.Fatalf("SetGuestCredential: %v", err)
	}
	got, err := store.GetGuestCredential("https://boxy.example:9090", "sb-1", "res-1")
	if err != nil {
		t.Fatalf("GetGuestCredential: %v", err)
	}
	if got.Kind != credential.Kind || string(got.Data) != string(credential.Data) {
		t.Fatalf("credential = %+v, want %+v", got, credential)
	}
	if _, err := store.Get("https://boxy.example:9090"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("API credential lookup = %v, want ErrNotFound", err)
	}
}

func TestKeyringStoreGuestCredentialRejectsMissingIDs(t *testing.T) {
	store := NewWithBackend("boxy", &fakeBackend{values: make(map[string]string)})
	credential := providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"password":"secret"}`)}
	if err := store.SetGuestCredential("https://boxy.example", "", "res-1", credential); err == nil {
		t.Fatal("SetGuestCredential succeeded with empty sandbox ID")
	}
	if _, err := store.GetGuestCredential("https://boxy.example", "sb-1", ""); err == nil {
		t.Fatal("GetGuestCredential succeeded with empty resource ID")
	}
}

func TestNormalizeServerURL(t *testing.T) {
	got, err := normalizeServerURL("HTTPS://Boxy.Example.test:9090/path/")
	if err != nil {
		t.Fatalf("normalizeServerURL: %v", err)
	}
	if got != "https://boxy.example.test:9090" {
		t.Fatalf("normalized URL = %q, want https://boxy.example.test:9090", got)
	}
}

// TestNormalizeServerURL_BareAddressDefaultsToHTTPS guards the same GitHub
// Copilot review finding as internal/cli's apiBaseURL: a schemeless address
// must normalize to the same https:// key that function builds for the
// actual connection. If this ever drifts from apiBaseURL's default,
// `boxy login --server host:port` and a later `boxy sandbox list --server
// host:port` would compute different keyring keys, and the stored
// credential would silently appear missing.
func TestNormalizeServerURL_BareAddressDefaultsToHTTPS(t *testing.T) {
	got, err := normalizeServerURL("myhost:9090")
	if err != nil {
		t.Fatalf("normalizeServerURL: %v", err)
	}
	if got != "https://myhost:9090" {
		t.Fatalf("normalized URL = %q, want https://myhost:9090", got)
	}
}

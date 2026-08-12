package credentials

import (
	"errors"
	"testing"
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

	if err := store.Set(server, "secret-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(server)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret-value" {
		t.Fatalf("Get = %q, want secret-value", got)
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
		{"set server", func() error { return store.Set(" ", "secret") }},
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
	server := "https://boxy.example:9090"
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

func TestNormalizeServerURL(t *testing.T) {
	got, err := normalizeServerURL("HTTPS://Boxy.Example:9090/path/")
	if err != nil {
		t.Fatalf("normalizeServerURL: %v", err)
	}
	if got != "https://boxy.example:9090" {
		t.Fatalf("normalized URL = %q, want https://boxy.example:9090", got)
	}
}

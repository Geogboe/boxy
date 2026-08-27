// Package credentials stores Boxy operator credentials in the operating
// system's keyring rather than in Boxy configuration or state files.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
	keyring "github.com/zalando/go-keyring"
)

const defaultService = "boxy"

var ErrNotFound = errors.New("boxy credential not found")

// Backend is the small keyring API used by Store. Tests can inject an in-memory
// implementation without touching a host keychain or secret service.
type Backend interface {
	Get(service, user string) (string, error)
	Set(service, user, value string) error
	Delete(service, user string) error
}

type systemBackend struct{}

func (systemBackend) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (systemBackend) Set(service, user, value string) error {
	return keyring.Set(service, user, value)
}

func (systemBackend) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// Store is a keyring-backed operator credential store.
type Store struct {
	service string
	backend Backend
}

// New returns a Store backed by the host operating system's keyring.
func New() *Store {
	return NewWithBackend(defaultService, systemBackend{})
}

// NewWithBackend constructs a Store with an injected backend, primarily for
// tests and platform-specific integration tests.
func NewWithBackend(service string, backend Backend) *Store {
	if strings.TrimSpace(service) == "" {
		service = defaultService
	}
	return &Store{service: service, backend: backend}
}

func (s *Store) Get(server string) (string, error) {
	user, err := normalizeServerURL(server)
	if err != nil {
		return "", err
	}
	value, err := s.backend.Get(s.service, user)
	if errors.Is(err, keyring.ErrNotFound) || errors.Is(err, ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read credential for %s: %w", user, err)
	}
	if strings.TrimSpace(value) == "" {
		return "", ErrNotFound
	}
	return value, nil
}

func (s *Store) Set(server, value string) error {
	user, err := normalizeServerURL(server)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("credential must not be empty")
	}
	if err := s.backend.Set(s.service, user, value); err != nil {
		return fmt.Errorf("write credential for %s: %w", user, err)
	}
	return nil
}

func (s *Store) SetCA(server string, certificate []byte) error {
	user, err := normalizeServerURL(server)
	if err != nil {
		return err
	}
	if len(certificate) == 0 {
		return errors.New("CA certificate must not be empty")
	}
	if err := s.backend.Set(s.service, caUser(user), string(certificate)); err != nil {
		return fmt.Errorf("write CA certificate for %s: %w", user, err)
	}
	return nil
}

func (s *Store) GetCA(server string) ([]byte, error) {
	user, err := normalizeServerURL(server)
	if err != nil {
		return nil, err
	}
	value, err := s.backend.Get(s.service, caUser(user))
	if errors.Is(err, keyring.ErrNotFound) || errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read CA certificate for %s: %w", user, err)
	}
	if value == "" {
		return nil, ErrNotFound
	}
	return []byte(value), nil
}

// SetGuestCredential stores a one-time-delivered guest credential for a
// sandbox resource. Guest credentials use a separate keyring namespace from
// API keys and CA certificates, and are stored as the opaque provider envelope
// so the CLI does not need to understand provider-specific fields.
func (s *Store) SetGuestCredential(server, sandboxID, resourceID string, credential providersdk.GuestCredential) error {
	user, err := guestCredentialUser(server, sandboxID, resourceID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode guest credential: %w", err)
	}
	if err := s.backend.Set(s.service, user, string(data)); err != nil {
		return fmt.Errorf("write guest credential for %s/%s: %w", sandboxID, resourceID, err)
	}
	return nil
}

// GetGuestCredential retrieves a previously saved guest credential.
func (s *Store) GetGuestCredential(server, sandboxID, resourceID string) (providersdk.GuestCredential, error) {
	var zero providersdk.GuestCredential
	user, err := guestCredentialUser(server, sandboxID, resourceID)
	if err != nil {
		return zero, err
	}
	value, err := s.backend.Get(s.service, user)
	if errors.Is(err, keyring.ErrNotFound) || errors.Is(err, ErrNotFound) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, fmt.Errorf("read guest credential for %s/%s: %w", sandboxID, resourceID, err)
	}
	if strings.TrimSpace(value) == "" {
		return zero, ErrNotFound
	}
	if err := json.Unmarshal([]byte(value), &zero); err != nil {
		return zero, fmt.Errorf("decode guest credential for %s/%s: %w", sandboxID, resourceID, err)
	}
	return zero, nil
}

// DeleteGuestCredential removes a previously saved guest credential from the
// keyring. A missing entry is a no-op success, the same way Delete already
// treats a missing API credential/CA entry — not every sandbox is created
// with --save-guest-cred, so callers (sandbox delete) don't need to check
// existence first.
func (s *Store) DeleteGuestCredential(server, sandboxID, resourceID string) error {
	user, err := guestCredentialUser(server, sandboxID, resourceID)
	if err != nil {
		return err
	}
	if err := s.backend.Delete(s.service, user); err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("delete guest credential for %s/%s: %w", sandboxID, resourceID, err)
	}
	return nil
}

func (s *Store) Delete(server string) error {
	user, err := normalizeServerURL(server)
	if err != nil {
		return err
	}
	if err := s.backend.Delete(s.service, user); err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("delete credential for %s: %w", user, err)
	}
	if err := s.backend.Delete(s.service, caUser(user)); err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("delete CA certificate for %s: %w", user, err)
	}
	return nil
}

func caUser(user string) string {
	return user + "#ca"
}

func guestCredentialUser(server, sandboxID, resourceID string) (string, error) {
	serverUser, err := normalizeServerURL(server)
	if err != nil {
		return "", err
	}
	sandboxID = strings.TrimSpace(sandboxID)
	resourceID = strings.TrimSpace(resourceID)
	if sandboxID == "" || resourceID == "" {
		return "", errors.New("sandbox and resource IDs must not be empty")
	}
	return fmt.Sprintf("%s#guest/%s/%s", serverUser, sandboxID, resourceID), nil
}

func normalizeServerURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("server URL must not be empty")
	}
	if !strings.Contains(value, "://") {
		// Must match internal/cli's apiBaseURL's scheme default exactly: a
		// bare address has to normalize to the same keyring key whether it
		// arrives via `boxy login --server host:port` or a later command's
		// `--server host:port` lookup. boxy serve is HTTPS by default, so
		// https:// is the default here too — see apiBaseURL's doc comment
		// for why defaulting to http:// broke the most natural way to point
		// the CLI at a remote server.
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("server URL must include an http or https host")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

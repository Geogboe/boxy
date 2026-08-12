// Package credentials stores Boxy operator credentials in the operating
// system's keyring rather than in Boxy configuration or state files.
package credentials

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

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

func normalizeServerURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("server URL must not be empty")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
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

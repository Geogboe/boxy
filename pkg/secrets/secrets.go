// Package secrets provides explicit server-owned secret backends.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

var (
	ErrNotFound        = errors.New("secret not found")
	ErrBackendRequired = errors.New("secret backend is required")
	ErrUnsupported     = errors.New("secret backend is unsupported on this platform")
)

type Backend string

const (
	BackendFile    Backend = "file"
	BackendKeyring Backend = "keyring"
	BackendDPAPI   Backend = "dpapi"
)

// Config selects exactly one secret backend. There is intentionally no
// automatic fallback: the deployment must make its storage posture explicit.
type Config struct {
	Backend Backend
	Path    string
	Service string
}

// Store is the runtime secret contract. Keys are logical Boxy keys, never
// provider paths or user-visible credential values.
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
	Check() error
}

// Open constructs the selected backend.
func Open(cfg Config) (Store, error) {
	switch Backend(strings.ToLower(strings.TrimSpace(string(cfg.Backend)))) {
	case BackendFile:
		return openFileStore(cfg.Path)
	case BackendKeyring:
		return newKeyringStore(cfg.Service, systemKeyringBackend{}), nil
	case BackendDPAPI:
		return openDPAPIStore(cfg.Path)
	case "":
		return nil, ErrBackendRequired
	default:
		return nil, fmt.Errorf("unsupported secret backend %q", cfg.Backend)
	}
}

func PoolBootstrapKey(poolName string) string {
	return "pool/" + strings.TrimSpace(poolName) + "/bootstrap"
}

func ResourceCredentialKey(resourceID string) string {
	return "resource/" + strings.TrimSpace(resourceID) + "/guest-credential"
}

type fileStore struct {
	mu   sync.Mutex
	path string
	data map[string][]byte
}

func openFileStore(path string) (*fileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("file secret backend path is required")
	}
	s := &fileStore{path: path, data: make(map[string][]byte)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *fileStore) load() error {
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat secret file %q: %w", s.path, err)
	}
	if err := checkSecretFilePermissions(info); err != nil {
		return fmt.Errorf("check secret file %q: %w", s.path, err)
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read secret file %q: %w", s.path, err)
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return fmt.Errorf("decode secret file %q: %w", s.path, err)
	}
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	return nil
}

func (s *fileStore) Check() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, err := os.Stat(s.path); err == nil {
		return checkSecretFilePermissions(info)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return ensureSecretDir(filepath.Dir(s.path))
}

func (s *fileStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.data[strings.TrimSpace(key)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *fileStore) Put(_ context.Context, key string, value []byte) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("secret key is required")
	}
	if len(value) == 0 {
		return fmt.Errorf("secret value is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return s.persistLocked()
}

func (s *fileStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key = strings.TrimSpace(key)
	if _, ok := s.data[key]; !ok {
		return ErrNotFound
	}
	delete(s.data, key)
	return s.persistLocked()
}

func (s *fileStore) persistLocked() error {
	if err := ensureSecretDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode secret file: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write secret file temporary path %q: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("set secret file permissions: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace secret file: %w", err)
	}
	return nil
}

type keyringAPI interface {
	Get(string, string) (string, error)
	Set(string, string, string) error
	Delete(string, string) error
}

type systemKeyringBackend struct{}

func (systemKeyringBackend) Get(service, key string) (string, error) {
	return keyring.Get(service, key)
}
func (systemKeyringBackend) Set(service, key, value string) error {
	return keyring.Set(service, key, value)
}
func (systemKeyringBackend) Delete(service, key string) error {
	return keyring.Delete(service, key)
}

type keyringStore struct {
	service string
	backend keyringAPI
}

func newKeyringStore(service string, backend keyringAPI) *keyringStore {
	service = strings.TrimSpace(service)
	if service == "" {
		service = "boxy-secrets"
	}
	return &keyringStore{service: service, backend: backend}
}

func (s *keyringStore) Check() error {
	if s == nil || s.backend == nil {
		return fmt.Errorf("keyring backend is unavailable")
	}
	_, err := s.backend.Get(s.service, "__boxy_backend_check__")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("keyring is unavailable: %w", err)
}

func (s *keyringStore) Get(_ context.Context, key string) ([]byte, error) {
	value, err := s.backend.Get(s.service, strings.TrimSpace(key))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read keyring secret: %w", err)
	}
	return []byte(value), nil
}

func (s *keyringStore) Put(_ context.Context, key string, value []byte) error {
	key = strings.TrimSpace(key)
	if key == "" || len(value) == 0 {
		return fmt.Errorf("secret key and value are required")
	}
	if err := s.backend.Set(s.service, key, string(value)); err != nil {
		return fmt.Errorf("write keyring secret: %w", err)
	}
	return nil
}

func (s *keyringStore) Delete(_ context.Context, key string) error {
	err := s.backend.Delete(s.service, strings.TrimSpace(key))
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete keyring secret: %w", err)
	}
	return nil
}

func ensureSecretDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secret directory %q: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: secret parent directories require owner-only execute/read access.
		return fmt.Errorf("set secret directory permissions %q: %w", dir, err)
	}
	return nil
}

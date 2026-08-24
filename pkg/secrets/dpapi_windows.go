//go:build windows

package secrets

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/internal/svcmgr"
)

type dpapiStore struct{ file *fileStore }

func openDPAPIStore(path string) (Store, error) {
	file, err := openFileStore(path)
	if err != nil {
		return nil, err
	}
	return &dpapiStore{file: file}, nil
}

func (s *dpapiStore) Check() error { return s.file.Check() }

func (s *dpapiStore) Get(ctx context.Context, key string) ([]byte, error) {
	ciphertext, err := s.file.getRaw(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := svcmgr.DecryptToken(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return plaintext, nil
}

func (s *dpapiStore) Put(_ context.Context, key string, value []byte) error {
	ciphertext, err := svcmgr.EncryptToken(value)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	return s.file.putRaw(key, ciphertext)
}

func (s *dpapiStore) Delete(_ context.Context, key string) error {
	return s.file.deleteRaw(key)
}

// getRaw, putRaw, and deleteRaw adapt fileStore's context-taking Store
// methods for dpapiStore, whose own Get/Put/Delete need ctx-free access to
// the underlying ciphertext. They live here (not in secrets.go) because
// dpapiStore is their only caller and this file is windows-only; keeping
// them in the shared, cross-platform file left them unused on every other
// GOOS, which golangci-lint's `unused` linter (correctly) flags when run on
// non-Windows CI runners.
func (s *fileStore) getRaw(key string) ([]byte, error) {
	return s.Get(context.Background(), key)
}

func (s *fileStore) putRaw(key string, value []byte) error {
	return s.Put(context.Background(), key, value)
}

func (s *fileStore) deleteRaw(key string) error {
	return s.Delete(context.Background(), key)
}

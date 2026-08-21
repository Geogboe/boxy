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

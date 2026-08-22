// Package diskjson provides a generic, mutex-guarded, atomically-written
// JSON file store for a single value.
//
// It exists so "persist one JSON blob to disk safely" doesn't get
// reimplemented ad hoc by every package that needs it.
// [pkg/store.DiskStore] established the pattern this package generalizes:
// write to a ".tmp" sibling then os.Rename it over the real path, because
// os.WriteFile's mode argument is only honored on file creation, not on
// rewrite of a pre-existing file — a direct overwrite can leave a reader (or
// a crash) observing a truncated file.
//
// Store is deliberately stateless between calls: every Load, Save, or
// Update round-trips through disk rather than caching the value in memory.
// A caller that wants an on-disk file it can inspect with cat/jq mid-run,
// or that has no long-lived process to hold a cache in, gets that for free
// at the cost of a disk round trip per call. A caller that wants an
// in-memory cache with disk as a write-behind backup (Boxy's own
// state.json) should keep doing what pkg/store.DiskStore does instead —
// this package is not a drop-in replacement for that shape.
package diskjson

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists a single JSON-serializable value of type T to a file. It
// is safe for concurrent use: every exported method holds an internal lock
// for the duration of its disk round trip.
type Store[T any] struct {
	mu      sync.Mutex
	path    string
	newFunc func() T
}

// New creates a Store backed by the file at path. newFunc produces the
// value returned by Load and Update when the file doesn't exist yet (e.g.
// on first run); pass nil to use T's zero value instead.
func New[T any](path string, newFunc func() T) *Store[T] {
	return &Store[T]{path: path, newFunc: newFunc}
}

// Path returns the file path this Store persists to.
func (s *Store[T]) Path() string {
	return s.path
}

// Load reads the current value from disk, or the value produced by New's
// newFunc (T's zero value if none was given) if the file doesn't exist yet.
func (s *Store[T]) Load() (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store[T]) loadLocked() (T, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.zero(), nil
		}
		var zero T
		return zero, fmt.Errorf("diskjson: read %q: %w", s.path, err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		var zero T
		return zero, fmt.Errorf("diskjson: decode %q: %w", s.path, err)
	}
	return v, nil
}

func (s *Store[T]) zero() T {
	if s.newFunc != nil {
		return s.newFunc()
	}
	var v T
	return v
}

// Save atomically writes v to disk, creating the parent directory (mode
// 0700) if needed. It writes to a ".tmp" sibling file (mode 0600) and
// renames it over the real path, so a concurrent reader or a crash mid-save
// never observes a partially-written file.
func (s *Store[T]) Save(v T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(v)
}

func (s *Store[T]) saveLocked(v T) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("diskjson: mkdir %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("diskjson: encode %q: %w", s.path, err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("diskjson: write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("diskjson: rename tmp: %w", err)
	}
	return nil
}

// Update loads the current value, applies fn, and atomically saves fn's
// result — all under Store's own lock, giving callers read-modify-write
// safety without managing a mutex of their own. If fn returns an error, no
// write occurs and fn's returned value and error are passed straight back.
func (s *Store[T]) Update(fn func(T) (T, error)) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.loadLocked()
	if err != nil {
		return cur, err
	}
	next, err := fn(cur)
	if err != nil {
		return next, err
	}
	if err := s.saveLocked(next); err != nil {
		return next, err
	}
	return next, nil
}

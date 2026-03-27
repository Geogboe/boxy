package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Geogboe/boxy/pkg/model"
)

// DiskStore is a simple JSON-backed Store implementation.
//
// It exists to make the CLI work end-to-end in environments where pulling new
// dependencies (e.g. bbolt) is not possible. It can be replaced with a bbolt
// implementation later without changing the Store interface.
type DiskStore struct {
	mu   sync.Mutex
	path string
	data diskState
}

type diskState struct {
	Pools     map[model.PoolName]model.Pool       `json:"pools"`
	Resources map[model.ResourceID]model.Resource `json:"resources"`
	Sandboxes map[model.SandboxID]model.Sandbox   `json:"sandboxes"`
}

func NewDiskStore(path string) (*DiskStore, error) {
	if path == "" {
		return nil, fmt.Errorf("disk store path is required")
	}
	s := &DiskStore{
		path: path,
		data: diskState{
			Pools:     make(map[model.PoolName]model.Pool),
			Resources: make(map[model.ResourceID]model.Resource),
			Sandboxes: make(map[model.SandboxID]model.Sandbox),
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DiskStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read store %q: %w", s.path, err)
	}
	if len(b) == 0 {
		return nil
	}
	var st diskState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("decode store %q: %w", s.path, err)
	}
	if st.Pools == nil {
		st.Pools = make(map[model.PoolName]model.Pool)
	}
	if st.Resources == nil {
		st.Resources = make(map[model.ResourceID]model.Resource)
	}
	if st.Sandboxes == nil {
		st.Sandboxes = make(map[model.SandboxID]model.Sandbox)
	}
	s.data = st
	return nil
}

func (s *DiskStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}

	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write store tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename store tmp: %w", err)
	}
	return nil
}

func (s *DiskStore) GetPool(ctx context.Context, name model.PoolName) (model.Pool, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Pools[name]
	if !ok {
		return model.Pool{}, ErrNotFound
	}
	return p, nil
}

func (s *DiskStore) PutPool(ctx context.Context, pool model.Pool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if pool.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	s.data.Pools[pool.Name] = pool
	return s.persistLocked()
}

func (s *DiskStore) GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Resources[id]
	if !ok {
		return model.Resource{}, ErrNotFound
	}
	return r, nil
}

func (s *DiskStore) PutResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	s.data.Resources[res.ID] = res
	return s.persistLocked()
}

func (s *DiskStore) GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.data.Sandboxes[id]
	if !ok {
		return model.Sandbox{}, ErrNotFound
	}
	return sb, nil
}

func (s *DiskStore) CreateSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	if _, exists := s.data.Sandboxes[sb.ID]; exists {
		return fmt.Errorf("sandbox already exists: %s", sb.ID)
	}
	s.data.Sandboxes[sb.ID] = sb
	return s.persistLocked()
}

func (s *DiskStore) PutSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	s.data.Sandboxes[sb.ID] = sb
	return s.persistLocked()
}

func (s *DiskStore) DeleteSandbox(_ context.Context, id model.SandboxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sandboxes[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.Sandboxes, id)
	return s.persistLocked()
}

func (s *DiskStore) ListPools(_ context.Context) ([]model.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Pool, 0, len(s.data.Pools))
	for _, p := range s.data.Pools {
		out = append(out, p)
	}
	return out, nil
}

func (s *DiskStore) ListResources(_ context.Context) ([]model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Resource, 0, len(s.data.Resources))
	for _, r := range s.data.Resources {
		out = append(out, r)
	}
	return out, nil
}

func (s *DiskStore) ListSandboxes(_ context.Context) ([]model.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Sandbox, 0, len(s.data.Sandboxes))
	for _, sb := range s.data.Sandboxes {
		out = append(out, sb)
	}
	return out, nil
}

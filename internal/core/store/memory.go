package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// MemoryStore is an in-memory Store implementation for scaffolding and tests.
type MemoryStore struct {
	mu sync.Mutex

	pools     map[model.PoolName]model.Pool
	resources map[model.ResourceID]model.Resource
	sandboxes map[model.SandboxID]model.Sandbox
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		pools:     make(map[model.PoolName]model.Pool),
		resources: make(map[model.ResourceID]model.Resource),
		sandboxes: make(map[model.SandboxID]model.Sandbox),
	}
}

func (s *MemoryStore) GetPool(ctx context.Context, name model.PoolName) (model.Pool, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pools[name]
	if !ok {
		return model.Pool{}, ErrNotFound
	}
	return p, nil
}

func (s *MemoryStore) PutPool(ctx context.Context, pool model.Pool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if pool.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	s.pools[pool.Name] = pool
	return nil
}

func (s *MemoryStore) GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.resources[id]
	if !ok {
		return model.Resource{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) PutResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	s.resources[res.ID] = res
	return nil
}

func (s *MemoryStore) GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sandboxes[id]
	if !ok {
		return model.Sandbox{}, ErrNotFound
	}
	return sb, nil
}

func (s *MemoryStore) CreateSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	if _, exists := s.sandboxes[sb.ID]; exists {
		return fmt.Errorf("sandbox already exists: %s", sb.ID)
	}
	s.sandboxes[sb.ID] = sb
	return nil
}

func (s *MemoryStore) PutSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	s.sandboxes[sb.ID] = sb
	return nil
}

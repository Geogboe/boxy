package sandbox

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/v2/internal/core/model"
	"github.com/Geogboe/boxy/v2/internal/core/store"
)

// Manager creates sandboxes and consumes resources from pools.
//
// This is the "demand side" counterpart to PoolManager.
type Manager struct {
	store store.Store
}

func New(s store.Store) *Manager {
	return &Manager{store: s}
}

// CreateFromPool creates a sandbox and attaches N ready resources from a pool.
//
// Resources never return to the pool (see ADR-0002).
func (m *Manager) CreateFromPool(
	ctx context.Context,
	poolName model.PoolName,
	count int,
	sbName string,
	policies model.SandboxPolicies,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if poolName == "" {
		return model.Sandbox{}, fmt.Errorf("pool name is required")
	}
	if count <= 0 {
		return model.Sandbox{}, fmt.Errorf("count must be > 0")
	}

	pool, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get pool: %w", err)
	}

	selected, remaining, err := takeReady(pool.Inventory.Resources, count)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("select from pool %q: %w", poolName, err)
	}

	pool.Inventory.Resources = remaining
	if err := m.store.PutPool(ctx, pool); err != nil {
		return model.Sandbox{}, fmt.Errorf("put pool: %w", err)
	}

	sbID, err := newSandboxID()
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("new sandbox id: %w", err)
	}

	sb := model.Sandbox{
		ID:       sbID,
		Name:     sbName,
		Policies: policies,
		Resources: resourceIDs(selected),
	}
	if err := m.store.CreateSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}

	// Mark resources allocated in the global resource store.
	for _, res := range selected {
		if res.ID == "" {
			return model.Sandbox{}, fmt.Errorf("selected resource has empty id")
		}
		res.State = model.ResourceStateAllocated
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Sandbox{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
	}

	return sb, nil
}

func takeReady(resources []model.Resource, n int) (picked []model.Resource, remaining []model.Resource, err error) {
	if n <= 0 {
		return nil, resources, nil
	}
	picked = make([]model.Resource, 0, n)
	remaining = make([]model.Resource, 0, len(resources))

	for _, r := range resources {
		if len(picked) < n && r.State == model.ResourceStateReady {
			picked = append(picked, r)
			continue
		}
		remaining = append(remaining, r)
	}
	if len(picked) < n {
		return nil, resources, fmt.Errorf("insufficient ready resources: need=%d got=%d", n, len(picked))
	}
	return picked, remaining, nil
}

func resourceIDs(rs []model.Resource) []model.ResourceID {
	ids := make([]model.ResourceID, 0, len(rs))
	for _, r := range rs {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

package sandbox

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/v2/internal/model"
	"github.com/Geogboe/boxy/v2/internal/store"
	"github.com/Geogboe/boxy/v2/pkg/resourcepool"
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

type invKey struct {
	Type    model.ResourceType
	Profile model.ResourceProfile
}

type keyedResource struct{ model.Resource }

func (r keyedResource) PoolKey() invKey {
	return invKey{Type: r.Type, Profile: r.Profile}
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

	inv := resourcepool.Pool[invKey, keyedResource, struct{}]{
		Key:   invKey{Type: pool.Inventory.ExpectedType, Profile: pool.Inventory.ExpectedProfile},
		Items: wrapResources(pool.Inventory.Resources),
	}
	picked, err := inv.Take(count, func(r keyedResource) bool { return r.State == model.ResourceStateReady })
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("select from pool %q: %w", poolName, err)
	}
	selected := unwrapResources(picked)
	pool.Inventory.Resources = unwrapResources(inv.Items)

	if err := m.store.PutPool(ctx, pool); err != nil {
		return model.Sandbox{}, fmt.Errorf("put pool: %w", err)
	}

	sbID, err := newSandboxID()
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("new sandbox id: %w", err)
	}

	sb := model.Sandbox{
		ID:        sbID,
		Name:      sbName,
		Policies:  policies,
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

func resourceIDs(rs []model.Resource) []model.ResourceID {
	ids := make([]model.ResourceID, 0, len(rs))
	for _, r := range rs {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func wrapResources(rs []model.Resource) []keyedResource {
	out := make([]keyedResource, 0, len(rs))
	for _, r := range rs {
		out = append(out, keyedResource{r})
	}
	return out
}

func unwrapResources(rs []keyedResource) []model.Resource {
	out := make([]model.Resource, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Resource)
	}
	return out
}

package sandbox

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/resourcepool"
	"github.com/Geogboe/boxy/pkg/store"
)

// Manager creates sandboxes and consumes resources from pools.
//
// This is the "demand side" counterpart to PoolManager.
type Manager struct {
	store     store.Store
	allocator SandboxAllocator
}

// New creates a Manager. allocator may be nil — if so, allocation-time hooks
// are skipped and resource Properties are not updated at allocation time.
func New(s store.Store, allocator SandboxAllocator) *Manager {
	return &Manager{store: s, allocator: allocator}
}

// Create creates an empty sandbox request.
func (m *Manager) Create(ctx context.Context, sbName string, policies model.SandboxPolicies) (model.Sandbox, error) {
	return m.CreateRequested(ctx, sbName, policies, nil)
}

// CreateRequested creates a sandbox request in pending state.
func (m *Manager) CreateRequested(
	ctx context.Context,
	sbName string,
	policies model.SandboxPolicies,
	requests []model.ResourceRequest,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	sbID, err := newSandboxID()
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("new sandbox id: %w", err)
	}
	sb := model.Sandbox{
		ID:       sbID,
		Name:     sbName,
		Policies: policies,
		Status:   model.SandboxStatusPending,
		Requests: append([]model.ResourceRequest(nil), requests...),
	}
	if err := m.store.CreateSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}
	return sb, nil
}

// AddFromPool attaches N ready resources from a pool to an existing sandbox.
//
// Resources never return to the pool (see ADR-0002).
func (m *Manager) AddFromPool(
	ctx context.Context,
	sbID model.SandboxID,
	poolName model.PoolName,
	count int,
) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}
	if poolName == "" {
		return model.Sandbox{}, fmt.Errorf("pool name is required")
	}
	if count <= 0 {
		return model.Sandbox{}, fmt.Errorf("count must be > 0")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
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

	sb.Resources = append(sb.Resources, resourceIDs(selected)...)
	for _, res := range selected {
		if res.ID == "" {
			return model.Sandbox{}, fmt.Errorf("selected resource has empty id")
		}
		if m.allocator != nil {
			extra, err := m.allocator.Allocate(ctx, pool, res)
			if err != nil {
				return model.Sandbox{}, fmt.Errorf("allocate resource %q: %w", res.ID, err)
			}
			if extra != nil {
				if res.Properties == nil {
					res.Properties = make(map[string]any)
				}
				for k, v := range extra {
					res.Properties[k] = v
				}
			}
		}
		res.State = model.ResourceStateAllocated
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Sandbox{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
	}

	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("put sandbox: %w", err)
	}

	return sb, nil
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
		Status:    model.SandboxStatusReady,
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
		if m.allocator != nil {
			extra, err := m.allocator.Allocate(ctx, pool, res)
			if err != nil {
				return model.Sandbox{}, fmt.Errorf("allocate resource %q: %w", res.ID, err)
			}
			if extra != nil {
				if res.Properties == nil {
					res.Properties = make(map[string]any)
				}
				for k, v := range extra {
					res.Properties[k] = v
				}
			}
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

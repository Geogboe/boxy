package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/resourcepool"
	"github.com/Geogboe/boxy/pkg/store"
)

var (
	ErrSandboxDeleting = errors.New("sandbox is deleting")

	// ErrNoExpiry is returned by RequestExtend when the sandbox has no
	// Policies.AutoDestroyAfter-derived expiry to extend.
	ErrNoExpiry = errors.New("sandbox has no expiry to extend (policies.auto_destroy_after not set)")
)

// Clock abstracts time so expiry computation is deterministic in tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Manager creates sandboxes and consumes resources from pools.
//
// This is the "demand side" counterpart to PoolManager.
type Manager struct {
	store     store.Store
	allocator SandboxAllocator
	clock     Clock
}

// New creates a Manager. allocator may be nil — if so, allocation-time hooks
// are skipped and resource Properties are not updated at allocation time.
func New(s store.Store, allocator SandboxAllocator) *Manager {
	return &Manager{store: s, allocator: allocator, clock: realClock{}}
}

// SetClock overrides the manager's time source. Used by tests.
func (m *Manager) SetClock(c Clock) {
	if c != nil {
		m.clock = c
	}
}

func (m *Manager) now() time.Time {
	if m.clock == nil {
		return time.Now().UTC()
	}
	return m.clock.Now()
}

// expiresAt computes an absolute expiry from policies.AutoDestroyAfter,
// relative to now. Returns nil (no expiry) if the policy is unset.
func (m *Manager) expiresAt(policies model.SandboxPolicies) (*time.Time, error) {
	if policies.AutoDestroyAfter == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(policies.AutoDestroyAfter)
	if err != nil {
		return nil, fmt.Errorf("policies.auto_destroy_after %q: %w", policies.AutoDestroyAfter, err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("policies.auto_destroy_after %q must be positive", policies.AutoDestroyAfter)
	}
	t := m.now().Add(d)
	return &t, nil
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
	expiresAt, err := m.expiresAt(policies)
	if err != nil {
		return model.Sandbox{}, err
	}
	sb := model.Sandbox{
		ID:        sbID,
		Name:      sbName,
		Policies:  policies,
		Status:    model.SandboxStatusPending,
		Requests:  append([]model.ResourceRequest(nil), requests...),
		ExpiresAt: expiresAt,
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
	if sb.Status == model.SandboxStatusDeleting {
		return model.Sandbox{}, ErrSandboxDeleting
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
		if res.OriginPool == "" {
			res.OriginPool = pool.Name
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
				maps.Copy(res.Properties, extra)
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
	expiresAt, err := m.expiresAt(policies)
	if err != nil {
		return model.Sandbox{}, err
	}

	sb := model.Sandbox{
		ID:        sbID,
		Name:      sbName,
		Policies:  policies,
		Status:    model.SandboxStatusReady,
		Resources: resourceIDs(selected),
		ExpiresAt: expiresAt,
	}
	if err := m.store.CreateSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("create sandbox: %w", err)
	}

	// Mark resources allocated in the global resource store.
	for _, res := range selected {
		if res.ID == "" {
			return model.Sandbox{}, fmt.Errorf("selected resource has empty id")
		}
		if res.OriginPool == "" {
			res.OriginPool = pool.Name
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
				maps.Copy(res.Properties, extra)
			}
		}
		res.State = model.ResourceStateAllocated
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Sandbox{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
	}

	return sb, nil
}

// RequestDelete marks a sandbox for asynchronous deletion. Cleanup is performed
// by the daemon reconciliation loop.
func (m *Manager) RequestDelete(ctx context.Context, sbID model.SandboxID) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
	}
	if sb.Status == model.SandboxStatusDeleting {
		return sb, nil
	}
	sb.Status = model.SandboxStatusDeleting
	sb.Error = ""
	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("mark sandbox %q deleting: %w", sb.ID, err)
	}
	return sb, nil
}

// RequestExtend pushes a sandbox's automatic-expiry deadline further out by
// extension, measured from its current expiry (not from now), so extending
// twice compounds rather than resetting the clock. Fails if the sandbox has
// no expiry to extend (Policies.AutoDestroyAfter was never set) or is
// already being deleted.
func (m *Manager) RequestExtend(ctx context.Context, sbID model.SandboxID, extension time.Duration) (model.Sandbox, error) {
	if m == nil {
		return model.Sandbox{}, fmt.Errorf("sandbox manager is nil")
	}
	if m.store == nil {
		return model.Sandbox{}, fmt.Errorf("store is nil")
	}
	if sbID == "" {
		return model.Sandbox{}, fmt.Errorf("sandbox id is required")
	}
	if extension <= 0 {
		return model.Sandbox{}, fmt.Errorf("extension duration must be positive")
	}

	sb, err := m.store.GetSandbox(ctx, sbID)
	if err != nil {
		return model.Sandbox{}, fmt.Errorf("get sandbox: %w", err)
	}
	if sb.Status == model.SandboxStatusDeleting {
		return model.Sandbox{}, ErrSandboxDeleting
	}
	if sb.ExpiresAt == nil {
		return model.Sandbox{}, ErrNoExpiry
	}

	newExpiry := sb.ExpiresAt.Add(extension)
	sb.ExpiresAt = &newExpiry
	if err := m.store.PutSandbox(ctx, sb); err != nil {
		return model.Sandbox{}, fmt.Errorf("extend sandbox %q: %w", sb.ID, err)
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

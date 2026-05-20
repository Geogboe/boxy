package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/policycontroller"
	"github.com/Geogboe/boxy/pkg/store"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type MaxTotalReachedError struct {
	PoolName       model.PoolName
	MaxTotal       int
	CurrentTotal   int
	ReadyCount     int
	RequestedReady int
}

func (e *MaxTotalReachedError) Error() string {
	return fmt.Sprintf(
		"pool %q is at max_total %d (%d total, %d ready), cannot satisfy requested ready count %d",
		e.PoolName,
		e.MaxTotal,
		e.CurrentTotal,
		e.ReadyCount,
		e.RequestedReady,
	)
}

type DrainedPoolError struct {
	PoolName       model.PoolName
	RequestedReady int
}

func (e *DrainedPoolError) Error() string {
	return fmt.Sprintf("pool %q is drained; cannot satisfy requested ready count %d", e.PoolName, e.RequestedReady)
}

type ConfigDeclaredDrainError struct {
	PoolName model.PoolName
}

func (e *ConfigDeclaredDrainError) Error() string {
	return fmt.Sprintf("pool %q is configured drained; edit config before filling it", e.PoolName)
}

// Manager reconciles a pool's inventory against its policies.
type Manager struct {
	store       store.Store
	provisioner Provisioner
	clock       Clock
	locksMu     sync.Mutex
	poolLocks   map[model.PoolName]*sync.Mutex
}

func New(s store.Store, p Provisioner) *Manager {
	return &Manager{
		store:       s,
		provisioner: p,
		clock:       realClock{},
		poolLocks:   make(map[model.PoolName]*sync.Mutex),
	}
}

func (m *Manager) SetClock(c Clock) {
	if c != nil {
		m.clock = c
	}
}

// Reconcile performs one reconciliation pass for the pool.
func (m *Manager) Reconcile(ctx context.Context, poolName model.PoolName) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	unlock := m.lockPool(poolName)
	defer unlock()
	return m.reconcileLocked(ctx, poolName, 0, false)
}

// EnsureReady ensures the pool has at least minReady resources available,
// without mutating the pool's configured preheat policy.
func (m *Manager) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	if minReady <= 0 {
		return nil
	}
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	unlock := m.lockPool(poolName)
	defer unlock()
	return m.reconcileLocked(ctx, poolName, minReady, true)
}

// Drain persists an operator drain override and immediately destroys unused ready inventory.
func (m *Manager) Drain(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	if m == nil {
		return model.Pool{}, fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return model.Pool{}, fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return model.Pool{}, fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return model.Pool{}, fmt.Errorf("pool name is required")
	}
	unlock := m.lockPool(poolName)
	defer unlock()

	p, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Pool{}, fmt.Errorf("get pool: %w", err)
	}
	p.Drain.Operator = true
	if err := m.store.PutPool(ctx, p); err != nil {
		return model.Pool{}, fmt.Errorf("put pool drain override: %w", err)
	}
	if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
		return model.Pool{}, err
	}
	return m.store.GetPool(ctx, poolName)
}

// Fill clears an operator drain override and immediately reconciles configured capacity.
func (m *Manager) Fill(ctx context.Context, poolName model.PoolName) (model.Pool, error) {
	if m == nil {
		return model.Pool{}, fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return model.Pool{}, fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return model.Pool{}, fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return model.Pool{}, fmt.Errorf("pool name is required")
	}
	unlock := m.lockPool(poolName)
	defer unlock()

	p, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return model.Pool{}, fmt.Errorf("get pool: %w", err)
	}
	p.Drain.Operator = false
	if err := m.store.PutPool(ctx, p); err != nil {
		return model.Pool{}, fmt.Errorf("clear pool drain override: %w", err)
	}
	if p.Drain.ConfigDeclared {
		if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
			return model.Pool{}, err
		}
		updated, err := m.store.GetPool(ctx, poolName)
		if err != nil {
			return model.Pool{}, fmt.Errorf("get pool after config-declared drain fill: %w", err)
		}
		return updated, &ConfigDeclaredDrainError{PoolName: poolName}
	}
	if err := m.reconcileLocked(ctx, poolName, 0, false); err != nil {
		return model.Pool{}, err
	}
	return m.store.GetPool(ctx, poolName)
}

// DestroyResource tears down a tracked resource through its origin pool's
// provider lifecycle and removes Boxy's resource record. Resources are
// single-use; this never returns resources to ready inventory.
func (m *Manager) DestroyResource(ctx context.Context, res model.Resource) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return fmt.Errorf("provisioner is nil")
	}
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	if res.OriginPool == "" {
		return fmt.Errorf("resource %q has no origin pool", res.ID)
	}

	unlock := m.lockPool(res.OriginPool)
	defer unlock()

	p, err := m.store.GetPool(ctx, res.OriginPool)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("origin pool %q for resource %q not found", res.OriginPool, res.ID)
	}
	if err != nil {
		return fmt.Errorf("get origin pool %q for resource %q: %w", res.OriginPool, res.ID, err)
	}

	if err := m.provisioner.Destroy(ctx, p, res); err != nil {
		return fmt.Errorf("destroy resource %q in pool %q: %w", res.ID, p.Name, err)
	}
	res.State = model.ResourceStateDestroyed
	res.UpdatedAt = m.clock.Now()
	if err := m.store.PutResource(ctx, res); err != nil {
		return fmt.Errorf("mark resource %q destroyed: %w", res.ID, err)
	}
	p.Inventory.Resources = removeInventoryResource(p.Inventory.Resources, res.ID)
	if err := m.store.PutPool(ctx, p); err != nil {
		return fmt.Errorf("put pool %q after destroying resource %q: %w", p.Name, res.ID, err)
	}
	if err := m.store.DeleteResource(ctx, res.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete resource %q: %w", res.ID, err)
	}
	return nil
}

func (m *Manager) lockPool(poolName model.PoolName) func() {
	m.locksMu.Lock()
	if m.poolLocks == nil {
		m.poolLocks = make(map[model.PoolName]*sync.Mutex)
	}
	lock := m.poolLocks[poolName]
	if lock == nil {
		lock = &sync.Mutex{}
		m.poolLocks[poolName] = lock
	}
	m.locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (m *Manager) reconcileLocked(ctx context.Context, poolName model.PoolName, minReadyOverride int, requireMinReady bool) error {
	if m == nil {
		return fmt.Errorf("pool manager is nil")
	}
	if m.store == nil {
		return fmt.Errorf("store is nil")
	}
	if m.provisioner == nil {
		return fmt.Errorf("provisioner is nil")
	}
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}

	type observed struct {
		pool      model.Pool
		resources []model.Resource
		now       time.Time
	}
	type plan struct {
		pool             model.Pool
		stale            []model.Resource
		drainResources   []model.Resource
		drain            bool
		now              time.Time
		toProvision      int
		inventoryChanged bool
		reason           string
	}

	ctrl := policycontroller.Controller[observed, plan]{
		Observer: policycontroller.ObserverFunc[observed](func(ctx context.Context) (observed, error) {
			p, err := m.store.GetPool(ctx, poolName)
			if err != nil {
				return observed{}, fmt.Errorf("get pool: %w", err)
			}
			resources, err := m.store.ListResources(ctx)
			if err != nil {
				return observed{}, fmt.Errorf("list resources: %w", err)
			}
			return observed{pool: p, resources: resources, now: m.clock.Now()}, nil
		}),
		Evaluator: policycontroller.EvaluatorFunc[observed, plan](func(ctx context.Context, obs observed) (policycontroller.Decision[plan], error) {
			_ = ctx
			p := obs.pool
			rebuilt, rebuildReport, err := RebuildReadyInventory(p, obs.resources, p.Inventory.Resources)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			p = rebuilt

			if p.EffectivelyDrained() {
				if requireMinReady {
					return policycontroller.Decision[plan]{}, &DrainedPoolError{
						PoolName:       p.Name,
						RequestedReady: minReadyOverride,
					}
				}
				toDrain := append([]model.Resource(nil), p.Inventory.Resources...)
				reason := fmt.Sprintf("drain inventory_rebuilt=%t ready=%d", rebuildReport.Changed, len(toDrain))
				return policycontroller.Decision[plan]{
					ShouldAct: rebuildReport.Changed || len(toDrain) > 0,
					Plan: plan{
						pool:             p,
						drainResources:   toDrain,
						drain:            true,
						now:              obs.now,
						inventoryChanged: rebuildReport.Changed,
						reason:           reason,
					},
					Reason: reason,
				}, nil
			}

			stale, kept, err := computeStale(p, obs.now)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			p.Inventory.Resources = kept

			readyCount := countReadyResources(p.Inventory.Resources)
			staleIDs := resourceIDSet(stale)
			totalCount := countTrackedResources(p.Name, obs.resources, p.Inventory.Resources, staleIDs)

			effectiveMinReady := p.Policies.Preheat.MinReady
			if minReadyOverride > effectiveMinReady {
				effectiveMinReady = minReadyOverride
			}

			if requireMinReady {
				if capErr := maxTotalShortfall(p.Name, p.Policies.Preheat.MaxTotal, totalCount, readyCount, effectiveMinReady); capErr != nil {
					return policycontroller.Decision[plan]{}, capErr
				}
			}

			toProv := computeToProvision(p, effectiveMinReady, totalCount)
			reason := "noop"
			should := false
			if rebuildReport.Changed || len(stale) > 0 || toProv > 0 {
				should = true
				reason = fmt.Sprintf("inventory_rebuilt=%t stale=%d provision=%d", rebuildReport.Changed, len(stale), toProv)
			}

			return policycontroller.Decision[plan]{
				ShouldAct: should,
				Plan: plan{
					pool:             p,
					stale:            stale,
					now:              obs.now,
					toProvision:      toProv,
					inventoryChanged: rebuildReport.Changed,
					reason:           reason,
				},
				Reason: reason,
			}, nil
		}),
		Actuator: policycontroller.ActuatorFunc[plan](func(ctx context.Context, pl plan) error {
			p := pl.pool
			if pl.drain {
				return m.applyDrain(ctx, p, pl.drainResources)
			}

			for _, res := range pl.stale {
				if err := m.provisioner.Destroy(ctx, p, res); err != nil {
					return fmt.Errorf("destroy stale resource %q in pool %q: %w", res.ID, p.Name, err)
				}
				res.State = model.ResourceStateDestroyed
				res.UpdatedAt = pl.now
				if err := m.store.PutResource(ctx, res); err != nil {
					return fmt.Errorf("mark stale resource %q destroyed: %w", res.ID, err)
				}
			}

			for i := 0; i < pl.toProvision; i++ {
				res, err := m.provisioner.Provision(ctx, p)
				if err != nil {
					return fmt.Errorf("provision resource for pool %q: %w", p.Name, err)
				}
				if res.OriginPool == "" {
					res.OriginPool = p.Name
				}
				if err := p.Inventory.Add(res); err != nil {
					return fmt.Errorf("add resource to pool %q inventory: %w", p.Name, err)
				}
				if err := m.store.PutResource(ctx, res); err != nil {
					return fmt.Errorf("put resource %q: %w", res.ID, err)
				}
			}

			if err := m.store.PutPool(ctx, p); err != nil {
				return fmt.Errorf("put pool: %w", err)
			}
			return nil
		}),
	}

	_, err := ctrl.Reconcile(ctx)
	if err != nil {
		var capErr *MaxTotalReachedError
		if errors.As(err, &capErr) {
			return capErr
		}
		var drainedErr *DrainedPoolError
		if errors.As(err, &drainedErr) {
			return drainedErr
		}
	}
	return err
}

func (m *Manager) applyDrain(ctx context.Context, p model.Pool, resources []model.Resource) error {
	if len(resources) == 0 {
		p.Inventory.Resources = nil
		if err := m.store.PutPool(ctx, p); err != nil {
			return fmt.Errorf("put drained pool: %w", err)
		}
		return nil
	}

	for _, res := range resources {
		if err := m.provisioner.Destroy(ctx, p, res); err != nil {
			return fmt.Errorf("destroy drained resource %q in pool %q: %w", res.ID, p.Name, err)
		}
		res.State = model.ResourceStateDestroyed
		if err := m.store.PutResource(ctx, res); err != nil {
			return fmt.Errorf("mark drained resource %q destroyed: %w", res.ID, err)
		}
		p.Inventory.Resources = removeInventoryResource(p.Inventory.Resources, res.ID)
		if err := m.store.PutPool(ctx, p); err != nil {
			return fmt.Errorf("put drained pool: %w", err)
		}
		if err := m.store.DeleteResource(ctx, res.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("delete drained resource %q: %w", res.ID, err)
		}
	}
	return nil
}

func removeInventoryResource(resources []model.Resource, id model.ResourceID) []model.Resource {
	out := resources[:0]
	for _, res := range resources {
		if res.ID == id {
			continue
		}
		out = append(out, res)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func computeStale(p model.Pool, now time.Time) (stale []model.Resource, kept []model.Resource, _ error) {
	maxAgeStr := p.Policies.Recycle.MaxAge
	if maxAgeStr == "" {
		return nil, p.Inventory.Resources, nil
	}

	maxAge, err := time.ParseDuration(maxAgeStr)
	if err != nil {
		return nil, nil, fmt.Errorf("pool %q recycle max_age invalid: %w", p.Name, err)
	}
	if maxAge <= 0 {
		return nil, p.Inventory.Resources, nil
	}

	stale, kept = partitionByMaxAge(
		p.Inventory.Resources,
		now,
		maxAge,
		func(res model.Resource) time.Time { return res.CreatedAt },
		func(res model.Resource) time.Time { return res.UpdatedAt },
	)
	return stale, kept, nil
}

func computeToProvision(p model.Pool, minReady int, totalCount int) int {
	readyCount := countReadyResources(p.Inventory.Resources)
	return computeToProvisionCount(
		model.PreheatPolicy{
			MinReady: minReady,
			MaxTotal: p.Policies.Preheat.MaxTotal,
		},
		readyCount,
		totalCount,
	)
}

func countReadyResources(resources []model.Resource) int {
	readyCount := 0
	for _, res := range resources {
		if res.State == model.ResourceStateReady {
			readyCount++
		}
	}
	return readyCount
}

func resourceIDSet(resources []model.Resource) map[model.ResourceID]struct{} {
	ids := make(map[model.ResourceID]struct{}, len(resources))
	for _, res := range resources {
		if res.ID == "" {
			continue
		}
		ids[res.ID] = struct{}{}
	}
	return ids
}

func countTrackedResources(
	poolName model.PoolName,
	resources []model.Resource,
	inventory []model.Resource,
	excludeIDs map[model.ResourceID]struct{},
) int {
	inventoryIDs := resourceIDSet(inventory)
	total := 0
	for _, res := range resources {
		if res.ID == "" {
			continue
		}
		if _, excluded := excludeIDs[res.ID]; excluded {
			continue
		}
		if res.State == model.ResourceStateDestroyed {
			continue
		}
		if res.OriginPool == poolName {
			total++
			continue
		}
		if res.OriginPool == "" {
			if _, ok := inventoryIDs[res.ID]; ok {
				total++
			}
		}
	}
	return total
}

func maxTotalShortfall(
	poolName model.PoolName,
	maxTotal int,
	totalCount int,
	readyCount int,
	requestedReady int,
) error {
	if maxTotal <= 0 || requestedReady <= 0 {
		return nil
	}
	if canSatisfyRequestedReady(
		maxTotal,
		readyCount,
		totalCount,
		requestedReady,
	) {
		return nil
	}
	return &MaxTotalReachedError{
		PoolName:       poolName,
		MaxTotal:       maxTotal,
		CurrentTotal:   totalCount,
		ReadyCount:     readyCount,
		RequestedReady: requestedReady,
	}
}

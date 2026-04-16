package pool

import (
	"context"
	"errors"
	"fmt"
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

// Manager reconciles a pool's inventory against its policies.
type Manager struct {
	store       store.Store
	provisioner Provisioner
	clock       Clock
}

func New(s store.Store, p Provisioner) *Manager {
	return &Manager{
		store:       s,
		provisioner: p,
		clock:       realClock{},
	}
}

func (m *Manager) SetClock(c Clock) {
	if c != nil {
		m.clock = c
	}
}

// Reconcile performs one reconciliation pass for the pool.
func (m *Manager) Reconcile(ctx context.Context, poolName model.PoolName) error {
	return m.reconcile(ctx, poolName, 0, false)
}

// EnsureReady ensures the pool has at least minReady resources available,
// without mutating the pool's configured preheat policy.
func (m *Manager) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	if minReady <= 0 {
		return nil
	}
	return m.reconcile(ctx, poolName, minReady, true)
}

func (m *Manager) reconcile(ctx context.Context, poolName model.PoolName, minReadyOverride int, requireMinReady bool) error {
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
		pool        model.Pool
		stale       []model.Resource
		now         time.Time
		toProvision int
		reason      string
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
			if len(stale) > 0 || toProv > 0 {
				should = true
				reason = fmt.Sprintf("stale=%d provision=%d", len(stale), toProv)
			}

			return policycontroller.Decision[plan]{
				ShouldAct: should,
				Plan: plan{
					pool:        p,
					stale:       stale,
					now:         obs.now,
					toProvision: toProv,
					reason:      reason,
				},
				Reason: reason,
			}, nil
		}),
		Actuator: policycontroller.ActuatorFunc[plan](func(ctx context.Context, pl plan) error {
			p := pl.pool

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
	}
	return err
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

	stale = make([]model.Resource, 0)
	kept = make([]model.Resource, 0, len(p.Inventory.Resources))
	for _, res := range p.Inventory.Resources {
		ageBase := res.CreatedAt
		if ageBase.IsZero() {
			ageBase = res.UpdatedAt
		}
		if ageBase.IsZero() || now.Sub(ageBase) <= maxAge {
			kept = append(kept, res)
			continue
		}
		stale = append(stale, res)
	}
	return stale, kept, nil
}

func computeToProvision(p model.Pool, minReady int, totalCount int) int {
	maxTotal := p.Policies.Preheat.MaxTotal
	if minReady <= 0 {
		return 0
	}

	readyCount := countReadyResources(p.Inventory.Resources)
	need := minReady - readyCount
	if need <= 0 {
		return 0
	}

	if maxTotal > 0 {
		avail := maxTotal - totalCount
		if avail <= 0 {
			return 0
		}
		if need > avail {
			need = avail
		}
	}

	return need
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
	availableToProvision := maxTotal - totalCount
	if availableToProvision < 0 {
		availableToProvision = 0
	}
	if readyCount+availableToProvision >= requestedReady {
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

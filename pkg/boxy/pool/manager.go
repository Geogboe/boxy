package pool

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/v2/pkg/boxy/model"
	"github.com/Geogboe/boxy/v2/pkg/boxy/store"
	"github.com/Geogboe/boxy/v2/pkg/policycontroller"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

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
		pool model.Pool
		now  time.Time
	}
	type plan struct {
		pool        model.Pool
		stale       []model.Resource
		toProvision int
		reason      string
	}

	ctrl := policycontroller.Controller[observed, plan]{
		Observer: policycontroller.ObserverFunc[observed](func(ctx context.Context) (observed, error) {
			p, err := m.store.GetPool(ctx, poolName)
			if err != nil {
				return observed{}, fmt.Errorf("get pool: %w", err)
			}
			return observed{pool: p, now: m.clock.Now()}, nil
		}),
		Evaluator: policycontroller.EvaluatorFunc[observed, plan](func(ctx context.Context, obs observed) (policycontroller.Decision[plan], error) {
			_ = ctx
			p := obs.pool

			stale, kept, err := computeStale(p, obs.now)
			if err != nil {
				return policycontroller.Decision[plan]{}, err
			}
			p.Inventory.Resources = kept

			toProv := computeToProvision(p)
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
			}

			for i := 0; i < pl.toProvision; i++ {
				res, err := m.provisioner.Provision(ctx, p)
				if err != nil {
					return fmt.Errorf("provision resource for pool %q: %w", p.Name, err)
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

func computeToProvision(p model.Pool) int {
	minReady := p.Policies.Preheat.MinReady
	maxTotal := p.Policies.Preheat.MaxTotal
	if minReady <= 0 {
		return 0
	}

	readyCount := 0
	for _, res := range p.Inventory.Resources {
		if res.State == model.ResourceStateReady {
			readyCount++
		}
	}

	need := minReady - readyCount
	if need <= 0 {
		return 0
	}

	if maxTotal > 0 {
		avail := maxTotal - len(p.Inventory.Resources)
		if avail <= 0 {
			return 0
		}
		if need > avail {
			need = avail
		}
	}

	return need
}

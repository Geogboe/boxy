package pool

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/v2/internal/core/model"
	"github.com/Geogboe/boxy/v2/internal/core/store"
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

	pool, err := m.store.GetPool(ctx, poolName)
	if err != nil {
		return fmt.Errorf("get pool: %w", err)
	}

	// 1) Recycle stale unused inventory.
	pool, err = m.recycle(ctx, pool)
	if err != nil {
		return err
	}

	// 2) Ensure preheat (MinReady ready resources).
	pool, err = m.ensurePreheat(ctx, pool)
	if err != nil {
		return err
	}

	if err := m.store.PutPool(ctx, pool); err != nil {
		return fmt.Errorf("put pool: %w", err)
	}
	return nil
}

func (m *Manager) recycle(ctx context.Context, pool model.Pool) (model.Pool, error) {
	maxAgeStr := pool.Policies.Recycle.MaxAge
	if maxAgeStr == "" {
		return pool, nil
	}

	maxAge, err := time.ParseDuration(maxAgeStr)
	if err != nil {
		return model.Pool{}, fmt.Errorf("pool %q recycle max_age invalid: %w", pool.Name, err)
	}
	if maxAge <= 0 {
		return pool, nil
	}

	now := m.clock.Now()
	kept := make([]model.Resource, 0, len(pool.Inventory.Resources))
	for _, res := range pool.Inventory.Resources {
		ageBase := res.CreatedAt
		if ageBase.IsZero() {
			ageBase = res.UpdatedAt
		}
		if ageBase.IsZero() {
			kept = append(kept, res)
			continue
		}
		if now.Sub(ageBase) <= maxAge {
			kept = append(kept, res)
			continue
		}
		// Stale: destroy then drop from inventory.
		if err := m.provisioner.Destroy(ctx, pool, res); err != nil {
			return model.Pool{}, fmt.Errorf("destroy stale resource %q in pool %q: %w", res.ID, pool.Name, err)
		}
	}
	pool.Inventory.Resources = kept
	return pool, nil
}

func (m *Manager) ensurePreheat(ctx context.Context, pool model.Pool) (model.Pool, error) {
	minReady := pool.Policies.Preheat.MinReady
	maxTotal := pool.Policies.Preheat.MaxTotal
	if minReady <= 0 {
		return pool, nil
	}

	readyCount := 0
	for _, res := range pool.Inventory.Resources {
		if res.State == model.ResourceStateReady {
			readyCount++
		}
	}

	need := minReady - readyCount
	for need > 0 {
		if maxTotal > 0 && len(pool.Inventory.Resources) >= maxTotal {
			break
		}
		res, err := m.provisioner.Provision(ctx, pool)
		if err != nil {
			return model.Pool{}, fmt.Errorf("provision resource for pool %q: %w", pool.Name, err)
		}
		if err := pool.Inventory.Add(res); err != nil {
			return model.Pool{}, fmt.Errorf("add resource to pool %q inventory: %w", pool.Name, err)
		}
		if err := m.store.PutResource(ctx, res); err != nil {
			return model.Pool{}, fmt.Errorf("put resource %q: %w", res.ID, err)
		}
		if res.State == model.ResourceStateReady {
			need--
		}
	}

	return pool, nil
}

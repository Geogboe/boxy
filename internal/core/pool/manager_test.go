package pool

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/v2/internal/core/model"
	"github.com/Geogboe/boxy/v2/internal/core/store"
)

type fakeProvisioner struct {
	n int
}

func (p *fakeProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	_ = ctx
	p.n++
	return model.Resource{
		ID:        model.ResourceID("res_" + string(rune('a'+p.n-1))),
		Type:      pool.Inventory.ExpectedType,
		Profile:   pool.Inventory.ExpectedProfile,
		Provider:  model.ProviderRef{ID: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1000+int64(p.n), 0).UTC(),
	}, nil
}

func (p *fakeProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = ctx
	_ = pool
	_ = res
	return nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestManager_Reconcile_PrefillMinReady(t *testing.T) {
	st := store.NewMemoryStore()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 2, MaxTotal: 5},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(context.Background(), pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(2000, 0).UTC()})

	if err := mgr.Reconcile(context.Background(), "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated, err := st.GetPool(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(updated.Inventory.Resources))
	}
}

func TestManager_Reconcile_RecycleStale(t *testing.T) {
	st := store.NewMemoryStore()
	old := model.Resource{
		ID:        "res_old",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{ID: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat:  model.PreheatPolicy{MinReady: 1, MaxTotal: 5},
			Recycle:  model.RecyclePolicy{MaxAge: "1h"},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{old}},
	}
	if err := st.PutPool(context.Background(), pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(context.Background(), old); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(7200, 0).UTC()}) // 2h later

	if err := mgr.Reconcile(context.Background(), "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated, err := st.GetPool(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 1 {
		t.Fatalf("expected 1 resource after recycle+preheat, got %d", len(updated.Inventory.Resources))
	}
	if updated.Inventory.Resources[0].ID == "res_old" {
		t.Fatalf("expected old resource to be recycled")
	}
}

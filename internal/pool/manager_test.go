package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeProvisioner struct {
	n          int
	destroyed  []model.ResourceID
	destroyErr error
}

func (p *fakeProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	_ = ctx
	p.n++
	return model.Resource{
		ID:        model.ResourceID("res_" + string(rune('a'+p.n-1))),
		Type:      pool.Inventory.ExpectedType,
		Profile:   pool.Inventory.ExpectedProfile,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1000+int64(p.n), 0).UTC(),
	}, nil
}

func (p *fakeProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = ctx
	_ = pool
	p.destroyed = append(p.destroyed, res.ID)
	if p.destroyErr != nil {
		return p.destroyErr
	}
	return nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type deleteFailStore struct {
	store.Store
	err error
}

func (s *deleteFailStore) DeleteResource(ctx context.Context, id model.ResourceID) error {
	_ = ctx
	_ = id
	return s.err
}

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

func TestManager_DestroyResource_DestroysAndDeletesWithoutReturningToInventory(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{
		ID:         "res-allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		State:      model.ResourceStateAllocated,
	}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "web",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{res},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	if err := mgr.DestroyResource(ctx, res); err != nil {
		t.Fatalf("DestroyResource: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != res.ID {
		t.Fatalf("destroyed = %v, want [%s]", prov.destroyed, res.ID)
	}
	if _, err := st.GetResource(ctx, res.ID); err != store.ErrNotFound {
		t.Fatalf("resource after destroy err = %v, want ErrNotFound", err)
	}
	p, err := st.GetPool(ctx, "web")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(p.Inventory.Resources) != 0 {
		t.Fatalf("inventory resources = %+v, want empty", p.Inventory.Resources)
	}
}

func TestManager_DestroyResource_MissingOriginPoolFailsBeforeDestroy(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{
		ID:         "res-allocated",
		OriginPool: "missing",
		State:      model.ResourceStateAllocated,
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	err := mgr.DestroyResource(ctx, res)
	if err == nil {
		t.Fatal("expected missing origin pool error")
	}
	if len(prov.destroyed) != 0 {
		t.Fatalf("destroyed = %v, want none", prov.destroyed)
	}
}

func TestManager_DestroyResource_IgnoresMissingResourceRecordAfterProviderDelete(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{
		ID:         "res-allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		State:      model.ResourceStateAllocated,
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "web",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	failingStore := &deleteFailStore{Store: st, err: store.ErrNotFound}
	prov := &fakeProvisioner{}
	if err := New(failingStore, prov).DestroyResource(ctx, res); err != nil {
		t.Fatalf("DestroyResource: %v", err)
	}
	if len(prov.destroyed) != 1 || prov.destroyed[0] != res.ID {
		t.Fatalf("destroyed = %v, want [%s]", prov.destroyed, res.ID)
	}
}

func TestManager_DestroyResource_ValidatesInputsBeforeProviderDelete(t *testing.T) {
	tests := []struct {
		name string
		mgr  *Manager
		res  model.Resource
	}{
		{name: "nil manager", mgr: nil, res: model.Resource{ID: "res-1", OriginPool: "web"}},
		{name: "nil store", mgr: New(nil, &fakeProvisioner{}), res: model.Resource{ID: "res-1", OriginPool: "web"}},
		{name: "nil provisioner", mgr: New(store.NewMemoryStore(), nil), res: model.Resource{ID: "res-1", OriginPool: "web"}},
		{name: "empty resource id", mgr: New(store.NewMemoryStore(), &fakeProvisioner{}), res: model.Resource{OriginPool: "web"}},
		{name: "empty origin pool", mgr: New(store.NewMemoryStore(), &fakeProvisioner{}), res: model.Resource{ID: "res-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mgr.DestroyResource(context.Background(), tt.res)
			if err == nil {
				t.Fatal("DestroyResource error = nil, want validation error")
			}
		})
	}
}

func TestManager_Reconcile_RecycleStale(t *testing.T) {
	st := store.NewMemoryStore()
	old := model.Resource{
		ID:        "res_old",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5},
			Recycle: model.RecyclePolicy{MaxAge: "1h"},
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

	oldAfter, err := st.GetResource(context.Background(), old.ID)
	if err != nil {
		t.Fatalf("get old resource: %v", err)
	}
	if oldAfter.State != model.ResourceStateDestroyed {
		t.Fatalf("old resource state = %q, want %q", oldAfter.State, model.ResourceStateDestroyed)
	}
}

func TestManager_EnsureReady_RespectsMaxTotalAcrossAllocatedResources(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	allocated := model.Resource{
		ID:         "res_allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateAllocated,
		CreatedAt:  time.Unix(1000, 0).UTC(),
		UpdatedAt:  time.Unix(1000, 0).UTC(),
	}
	if err := st.PutResource(ctx, allocated); err != nil {
		t.Fatalf("put allocated resource: %v", err)
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	err := mgr.EnsureReady(ctx, "p1", 1)
	if err == nil {
		t.Fatal("expected ensure ready to fail at max_total")
	}
	if err.Error() != `pool "p1" is at max_total 1 (1 total, 0 ready), cannot satisfy requested ready count 1` {
		t.Fatalf("ensure ready error = %v", err)
	}

	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 0 {
		t.Fatalf("inventory len = %d, want 0", len(updated.Inventory.Resources))
	}
	if prov.n != 0 {
		t.Fatalf("provision count = %d, want 0", prov.n)
	}
}

func TestManager_Reconcile_IgnoresDestroyedResourcesWhenApplyingMaxTotal(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	destroyed := model.Resource{
		ID:         "res_destroyed",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateDestroyed,
		CreatedAt:  time.Unix(1000, 0).UTC(),
		UpdatedAt:  time.Unix(1001, 0).UTC(),
	}
	if err := st.PutResource(ctx, destroyed); err != nil {
		t.Fatalf("put destroyed resource: %v", err)
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 1 {
		t.Fatalf("inventory len = %d, want 1", len(updated.Inventory.Resources))
	}
	if prov.n != 1 {
		t.Fatalf("provision count = %d, want 1", prov.n)
	}
}

func TestManager_Reconcile_RebuildsReadyInventoryFromPersistedResources(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	ready := model.Resource{
		ID:         "res_ready",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateReady,
		CreatedAt:  time.Unix(1000, 0).UTC(),
		UpdatedAt:  time.Unix(1000, 0).UTC(),
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put ready resource: %v", err)
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 1 || updated.Inventory.Resources[0].ID != ready.ID {
		t.Fatalf("inventory resources = %+v, want %q", updated.Inventory.Resources, ready.ID)
	}
	if prov.n != 0 {
		t.Fatalf("provision count = %d, want 0", prov.n)
	}
}

func TestManager_Reconcile_DrainsReadyInventory(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:         "res_ready",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		State:      model.ResourceStateReady,
	}
	allocated := model.Resource{
		ID:         "res_allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		State:      model.ResourceStateAllocated,
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put ready resource: %v", err)
	}
	if err := st.PutResource(ctx, allocated); err != nil {
		t.Fatalf("put allocated resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{ConfigDeclared: true},
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 0},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != ready.ID {
		t.Fatalf("destroyed = %v, want [%s]", prov.destroyed, ready.ID)
	}
	if prov.n != 0 {
		t.Fatalf("provision count = %d, want 0", prov.n)
	}
	if _, err := st.GetResource(ctx, ready.ID); err != store.ErrNotFound {
		t.Fatalf("ready resource after drain err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetResource(ctx, allocated.ID); err != nil {
		t.Fatalf("allocated resource should remain: %v", err)
	}
	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 0 {
		t.Fatalf("inventory len = %d, want 0", len(updated.Inventory.Resources))
	}
}

func TestManager_Reconcile_DrainProviderFailureKeepsStateForRetry(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:         "res_ready",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		State:      model.ResourceStateReady,
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put ready resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{destroyErr: errors.New("provider unavailable")}
	mgr := New(st, prov)
	err := mgr.Reconcile(ctx, "p1")
	if err == nil {
		t.Fatal("reconcile error = nil, want provider failure")
	}
	if _, getErr := st.GetResource(ctx, ready.ID); getErr != nil {
		t.Fatalf("ready resource should remain for retry: %v", getErr)
	}
	updated, getErr := st.GetPool(ctx, "p1")
	if getErr != nil {
		t.Fatalf("get pool: %v", getErr)
	}
	if len(updated.Inventory.Resources) != 1 || updated.Inventory.Resources[0].ID != ready.ID {
		t.Fatalf("inventory = %+v, want failed resource visible", updated.Inventory.Resources)
	}
}

func TestManager_Reconcile_DrainDeleteFailureMarksDestroyedForRetry(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:         "res_ready",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		State:      model.ResourceStateReady,
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put ready resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	deleteErr := errors.New("delete failed")
	failingStore := &deleteFailStore{Store: st, err: deleteErr}
	prov := &fakeProvisioner{}
	mgr := New(failingStore, prov)
	err := mgr.Reconcile(ctx, "p1")
	if err == nil {
		t.Fatal("reconcile error = nil, want delete failure")
	}

	stored, getErr := st.GetResource(ctx, ready.ID)
	if getErr != nil {
		t.Fatalf("get ready resource after delete failure: %v", getErr)
	}
	if stored.State != model.ResourceStateDestroyed {
		t.Fatalf("resource state = %q, want %q", stored.State, model.ResourceStateDestroyed)
	}
	updated, getErr := st.GetPool(ctx, "p1")
	if getErr != nil {
		t.Fatalf("get pool: %v", getErr)
	}
	if len(updated.Inventory.Resources) != 0 {
		t.Fatalf("inventory len = %d, want 0", len(updated.Inventory.Resources))
	}

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(prov.destroyed) != 1 {
		t.Fatalf("destroy calls = %v, want no retry after destroyed marker", prov.destroyed)
	}
}

func TestManager_Reconcile_DrainLegacyEmbeddedInventory(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	legacy := model.Resource{
		ID:      "res_legacy",
		Type:    model.ResourceTypeContainer,
		Profile: model.ResourceProfileDefault,
		State:   model.ResourceStateReady,
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{legacy},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(prov.destroyed) != 1 || prov.destroyed[0] != legacy.ID {
		t.Fatalf("destroyed = %v, want legacy resource", prov.destroyed)
	}
	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 0 {
		t.Fatalf("inventory len = %d, want 0", len(updated.Inventory.Resources))
	}
}

func TestManager_EnsureReady_FailsWhenPoolDrained(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	err := mgr.EnsureReady(ctx, "p1", 1)
	if err == nil {
		t.Fatal("EnsureReady error = nil, want drained pool error")
	}
	if got, want := err.Error(), `pool "p1" is drained; cannot satisfy requested ready count 1`; got != want {
		t.Fatalf("EnsureReady error = %q, want %q", got, want)
	}
	if prov.n != 0 {
		t.Fatalf("provision count = %d, want 0", prov.n)
	}
}

func TestManager_Fill_ConfigDeclaredDrainStillDrainsInventory(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:         "res_ready",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		State:      model.ResourceStateReady,
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put ready resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "p1",
		Drain: model.PoolDrainState{
			ConfigDeclared: true,
			Operator:       true,
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	filled, err := mgr.Fill(ctx, "p1")
	if err == nil {
		t.Fatal("Fill error = nil, want config-declared drain error")
	}
	var configErr *ConfigDeclaredDrainError
	if !errors.As(err, &configErr) {
		t.Fatalf("Fill error = %T %[1]v, want ConfigDeclaredDrainError", err)
	}
	if len(prov.destroyed) != 1 || prov.destroyed[0] != ready.ID {
		t.Fatalf("destroyed = %v, want [%s]", prov.destroyed, ready.ID)
	}
	if filled.Drain.Operator {
		t.Fatal("returned operator drain = true, want cleared")
	}
	if len(filled.Inventory.Resources) != 0 {
		t.Fatalf("returned inventory len = %d, want 0", len(filled.Inventory.Resources))
	}
	if _, err := st.GetResource(ctx, ready.ID); err != store.ErrNotFound {
		t.Fatalf("ready resource after fill err = %v, want ErrNotFound", err)
	}
	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if updated.Drain.Operator {
		t.Fatal("operator drain = true, want cleared")
	}
	if !updated.Drain.ConfigDeclared || !updated.EffectivelyDrained() {
		t.Fatalf("drain state = %+v, want config-declared effective drain", updated.Drain)
	}
	if len(updated.Inventory.Resources) != 0 {
		t.Fatalf("inventory len = %d, want 0", len(updated.Inventory.Resources))
	}
}

func TestManager_Reconcile_DrainEmptyInventoryIsIdempotent(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.PutPool(ctx, model.Pool{
		Name:  "p1",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(prov.destroyed) != 0 || prov.n != 0 {
		t.Fatalf("destroyed=%v provisioned=%d, want no operations", prov.destroyed, prov.n)
	}
}

package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeProvisioner struct {
	n              int
	provisionCalls int
	provisionErr   error
	// provisionResultOnErr, when provisionErr is set, is returned alongside
	// it instead of the zero Resource — mirrors DriverProvisioner/
	// AgentProvisioner's quarantine contract (see #174 Task 4).
	provisionResultOnErr model.Resource
	// failAfter, when > 0, makes Provision succeed for the first failAfter
	// calls and only start returning provisionErr from call failAfter+1
	// onward — used to simulate a bonus/background top-up failing only
	// after the caller's real request has already been satisfied (#249).
	// Zero (the default) preserves the existing always-fails-if-set
	// behavior for every test that doesn't set it.
	failAfter  int
	destroyed  []model.ResourceID
	destroyErr error
	// onDestroy, if set, is called at the top of Destroy — before the
	// (possibly failing) provider call — so a test can observe state
	// persisted just before teardown (e.g. via the store).
	onDestroy func(model.ResourceID)
}

func (p *fakeProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	_ = ctx
	p.provisionCalls++
	if p.provisionErr != nil && (p.failAfter == 0 || p.provisionCalls > p.failAfter) {
		return p.provisionResultOnErr, p.provisionErr
	}
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
	if p.onDestroy != nil {
		p.onDestroy(res.ID)
	}
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

type putResourceFailStore struct {
	store.Store
	err error
}

func (s *putResourceFailStore) PutResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	_ = res
	return s.err
}

// putResourceFailAfterStore fails PutResource on a specific call number
// (1-indexed), delegating to the wrapped store on every other call — used
// to simulate a persist failure for a specific (e.g. bonus-preheat)
// provisioning iteration while earlier iterations succeed and actually
// land in the store. See #249: a code review caught that the original
// bonus-vs-required tolerance keyed only on err != nil, which would also
// have downgraded this exact scenario (Create succeeded, persist failed —
// a real provider-side resource left with no store record at all) to a
// silent, best-effort log line.
type putResourceFailAfterStore struct {
	store.Store
	calls      int
	failOnCall int
	err        error
}

func (s *putResourceFailAfterStore) PutResource(ctx context.Context, res model.Resource) error {
	s.calls++
	if s.err != nil && s.calls == s.failOnCall {
		return s.err
	}
	return s.Store.PutResource(ctx, res)
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

// TestManager_Reconcile_InFlightResourcesCountTowardMinReady pins #258: a
// resource this pool already provisioned on a previous tick, but that
// hasn't reached ResourceStateReady yet (still going through
// AdmissionHandler, e.g. guest personalization), must count toward
// min_ready just like a Ready one does. Before the fix, computeToProvision
// measured the gap against ready count alone, so every tick before
// admission finished re-requested the full remaining gap -- overshooting
// min_ready by one extra resource per tick until max_total capped it.
func TestManager_Reconcile_InFlightResourcesCountTowardMinReady(t *testing.T) {
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
	inFlight := model.Resource{
		ID:         "res_provisioning",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateProvisioning,
		CreatedAt:  time.Unix(1001, 0).UTC(),
		UpdatedAt:  time.Unix(1001, 0).UTC(),
	}
	if err := st.PutResource(ctx, inFlight); err != nil {
		t.Fatalf("put in-flight resource: %v", err)
	}

	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 2, MaxTotal: 4},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			// Only the ready resource belongs here -- RebuildReadyInventory
			// only ever re-admits ResourceStateReady resources, so this
			// mirrors exactly what a prior tick would have left behind.
			Resources: []model.Resource{ready},
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

	if prov.provisionCalls != 0 {
		t.Fatalf("provisionCalls = %d, want 0: min_ready=2 was already covered by 1 ready + 1 in-flight resource", prov.provisionCalls)
	}

	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updated.Inventory.Resources) != 1 {
		t.Fatalf("inventory len = %d, want 1 (only the already-ready resource)", len(updated.Inventory.Resources))
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

func TestManager_DestroyResource_MarksDestroyingBeforeDestroy(t *testing.T) {
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

	var stateAtDestroy model.ResourceState
	prov := &fakeProvisioner{}
	prov.onDestroy = func(id model.ResourceID) {
		got, getErr := st.GetResource(ctx, id)
		if getErr != nil {
			t.Fatalf("get resource during destroy: %v", getErr)
		}
		stateAtDestroy = got.State
	}
	mgr := New(st, prov)
	if err := mgr.DestroyResource(ctx, res); err != nil {
		t.Fatalf("DestroyResource: %v", err)
	}

	if stateAtDestroy != model.ResourceStateDestroying {
		t.Fatalf("state observed at Destroy time = %q, want %q", stateAtDestroy, model.ResourceStateDestroying)
	}
}

func TestManager_DestroyResource_ProviderFailureLeavesDestroyingState(t *testing.T) {
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

	prov := &fakeProvisioner{destroyErr: errors.New("provider unavailable")}
	mgr := New(st, prov)
	if err := mgr.DestroyResource(ctx, res); err == nil {
		t.Fatal("DestroyResource error = nil, want provider failure")
	}

	got, err := st.GetResource(ctx, res.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if got.State != model.ResourceStateDestroying {
		t.Fatalf("state after destroy error = %q, want %q (left mid-transition for retry)", got.State, model.ResourceStateDestroying)
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

// forceOrphanProvisioner is a fakeProvisioner that also implements
// ForceOrphaner, recording ForceOrphan calls. Its embedded Destroy panics if
// invoked — ForceOrphanResource must never reach it.
type forceOrphanProvisioner struct {
	fakeProvisioner
	forceOrphaned  []model.ResourceID
	forceOrphanErr error
}

func (p *forceOrphanProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	panic("Destroy must never be called during force-orphan")
}

func (p *forceOrphanProvisioner) ForceOrphan(ctx context.Context, res model.Resource) error {
	_ = ctx
	p.forceOrphaned = append(p.forceOrphaned, res.ID)
	return p.forceOrphanErr
}

func TestManager_ForceOrphanResource_RemovesFromInventoryAndStoreWithoutDestroy(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{
		ID:         "res-orphan",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		State:      model.ResourceStateAllocated,
		Provider:   model.ProviderRef{AgentID: "agent-gone"},
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

	prov := &forceOrphanProvisioner{}
	mgr := New(st, prov)
	if err := mgr.ForceOrphanResource(ctx, res, "host decommissioned"); err != nil {
		t.Fatalf("ForceOrphanResource: %v", err)
	}

	if len(prov.forceOrphaned) != 1 || prov.forceOrphaned[0] != res.ID {
		t.Fatalf("forceOrphaned = %v, want [%s]", prov.forceOrphaned, res.ID)
	}
	if _, err := st.GetResource(ctx, res.ID); err != store.ErrNotFound {
		t.Fatalf("resource after force-orphan err = %v, want ErrNotFound", err)
	}
	p, err := st.GetPool(ctx, "web")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(p.Inventory.Resources) != 0 {
		t.Fatalf("inventory resources = %+v, want empty", p.Inventory.Resources)
	}
}

func TestManager_ForceOrphanResource_ProvisionerWithoutForceOrphanerSupportFails(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{ID: "res-1", OriginPool: "web"}
	if err := st.PutPool(ctx, model.Pool{Name: "web"}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	mgr := New(st, &fakeProvisioner{})
	if err := mgr.ForceOrphanResource(ctx, res, "reason"); err == nil {
		t.Fatal("ForceOrphanResource error = nil, want error because provisioner does not implement ForceOrphaner")
	}
}

func TestManager_ForceOrphanAgentResources_SweepsOnlyMatchingAgent(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()

	mk := func(id model.ResourceID, agentID string) model.Resource {
		return model.Resource{
			ID:         id,
			Type:       model.ResourceTypeContainer,
			Profile:    model.ResourceProfileDefault,
			OriginPool: "web",
			State:      model.ResourceStateAllocated,
			Provider:   model.ProviderRef{AgentID: agentID},
		}
	}
	gone1 := mk("res-gone-1", "agent-gone")
	gone2 := mk("res-gone-2", "agent-gone")
	other := mk("res-other", "agent-other")

	for _, res := range []model.Resource{gone1, gone2, other} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put resource %q: %v", res.ID, err)
		}
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "web",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{gone1, gone2, other},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &forceOrphanProvisioner{}
	mgr := New(st, prov)
	n, err := mgr.ForceOrphanAgentResources(ctx, "agent-gone", "host decommissioned")
	if err != nil {
		t.Fatalf("ForceOrphanAgentResources: %v", err)
	}
	if n != 2 {
		t.Fatalf("swept count = %d, want 2", n)
	}
	if len(prov.forceOrphaned) != 2 {
		t.Fatalf("forceOrphaned = %v, want 2 entries", prov.forceOrphaned)
	}

	if _, err := st.GetResource(ctx, gone1.ID); err != store.ErrNotFound {
		t.Fatalf("gone1 after sweep err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetResource(ctx, gone2.ID); err != store.ErrNotFound {
		t.Fatalf("gone2 after sweep err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetResource(ctx, other.ID); err != nil {
		t.Fatalf("res belonging to a different agent must be untouched: %v", err)
	}
}

// TestManager_ForceOrphanAgentResources_ContinuesPastPerResourceFailure
// guards against one bad resource blocking cleanup of every other resource
// a permanently-gone agent left behind. A resource with no OriginPool set
// (e.g. one adopted by pool.ReconcileAgent, see #133 — ForceOrphanResource
// requires OriginPool, same as DestroyResource) must not abort the sweep
// for resources that do have one.
func TestManager_ForceOrphanAgentResources_ContinuesPastPerResourceFailure(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()

	noOriginPool := model.Resource{
		ID:       "res-no-origin-pool",
		State:    model.ResourceStateUnknown,
		Provider: model.ProviderRef{AgentID: "agent-gone"},
	}
	sweepable := model.Resource{
		ID:         "res-sweepable",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		State:      model.ResourceStateAllocated,
		Provider:   model.ProviderRef{AgentID: "agent-gone"},
	}
	for _, res := range []model.Resource{noOriginPool, sweepable} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put resource %q: %v", res.ID, err)
		}
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "web",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{sweepable},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &forceOrphanProvisioner{}
	mgr := New(st, prov)
	n, err := mgr.ForceOrphanAgentResources(ctx, "agent-gone", "host decommissioned")
	if err == nil {
		t.Fatal("ForceOrphanAgentResources error = nil, want an error reporting the res-no-origin-pool failure")
	}
	if n != 1 {
		t.Fatalf("swept count = %d, want 1 (the resource without OriginPool must not block the other)", n)
	}
	if _, err := st.GetResource(ctx, sweepable.ID); err != store.ErrNotFound {
		t.Fatalf("sweepable resource err = %v, want ErrNotFound — it must still be force-orphaned despite the other resource's failure", err)
	}
	if _, err := st.GetResource(ctx, noOriginPool.ID); err != nil {
		t.Fatalf("resource without OriginPool should still exist (it could not be force-orphaned): %v", err)
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

func TestManager_Reconcile_RecycleStale_MarksRecyclingBeforeDestroy(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
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
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, old); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	var stateAtDestroy model.ResourceState
	prov := &fakeProvisioner{}
	prov.onDestroy = func(id model.ResourceID) {
		res, getErr := st.GetResource(ctx, id)
		if getErr != nil {
			t.Fatalf("get resource during destroy: %v", getErr)
		}
		stateAtDestroy = res.State
	}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(7200, 0).UTC()}) // 2h later

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if stateAtDestroy != model.ResourceStateRecycling {
		t.Fatalf("state observed at Destroy time = %q, want %q", stateAtDestroy, model.ResourceStateRecycling)
	}

	final, err := st.GetResource(ctx, old.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if final.State != model.ResourceStateDestroyed {
		t.Fatalf("final state = %q, want %q", final.State, model.ResourceStateDestroyed)
	}
}

func TestManager_Reconcile_RecycleStale_DestroyErrorLeavesRecyclingState(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
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
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, old); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{destroyErr: errors.New("provider unavailable")}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(7200, 0).UTC()})

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("reconcile error = nil, want provider destroy failure")
	}

	res, err := st.GetResource(ctx, old.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if res.State != model.ResourceStateRecycling {
		t.Fatalf("state after destroy error = %q, want %q (left mid-transition for retry, not reverted or marked destroyed)", res.State, model.ResourceStateRecycling)
	}
}

func TestManager_Reconcile_RecycleStale_OrphanSweepRetriesStuckResource(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	// Simulates a crash between the pre-destroy state write and the destroy
	// completing: the resource is already "recycling" but has dropped out of
	// the pool's inventory (RebuildReadyInventory only re-admits Ready
	// resources), so neither computeStale nor drain would ever see it again
	// without an explicit sweep.
	orphan := model.Resource{
		ID:         "res_orphan",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateRecycling,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, orphan); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != orphan.ID {
		t.Fatalf("destroyed = %v, want orphan resource %q retried", prov.destroyed, orphan.ID)
	}
	final, err := st.GetResource(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if final.State != model.ResourceStateDestroyed {
		t.Fatalf("final state = %q, want %q", final.State, model.ResourceStateDestroyed)
	}
}

func TestManager_Reconcile_RecycleStale_SweepsLegacyOrphanWithoutOriginPool(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	// Older state files can have resources embedded in pool inventory without
	// OriginPool. If teardown persisted the transient state before a crash, the
	// old inventory entry is the only durable ownership signal left.
	orphan := model.Resource{
		ID:        "res_legacy_orphan",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateDestroying,
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	p := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{orphan},
		},
	}
	if err := st.PutPool(ctx, p); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, orphan); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, p.Name); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != orphan.ID {
		t.Fatalf("destroyed = %v, want legacy orphan %q retried", prov.destroyed, orphan.ID)
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

// putAllocatedResources persists n already-allocated resources for poolName,
// simulating sandboxes that hold resources from this pool. They are not
// added to pool.Inventory.Resources (allocated resources never live there;
// only ready ones do) but are counted toward totalCount via the store.
func putAllocatedResources(t *testing.T, st store.Store, poolName model.PoolName, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		res := model.Resource{
			ID:         model.ResourceID(fmt.Sprintf("res_allocated_%d", i)),
			Type:       model.ResourceTypeContainer,
			Profile:    model.ResourceProfileDefault,
			OriginPool: poolName,
			Provider:   model.ProviderRef{Name: "prov_1"},
			State:      model.ResourceStateAllocated,
			CreatedAt:  time.Unix(1000, 0).UTC(),
			UpdatedAt:  time.Unix(1000, 0).UTC(),
		}
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put allocated resource %q: %v", res.ID, err)
		}
	}
}

// putReadyResource persists one ready resource for poolName and returns it,
// for the caller to also add to pool.Inventory.Resources.
func putReadyResource(t *testing.T, st store.Store, poolName model.PoolName, id model.ResourceID) model.Resource {
	t.Helper()
	res := model.Resource{
		ID:         id,
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: poolName,
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateReady,
		CreatedAt:  time.Unix(1000, 0).UTC(),
		UpdatedAt:  time.Unix(1000, 0).UTC(),
	}
	if err := st.PutResource(context.Background(), res); err != nil {
		t.Fatalf("put ready resource %q: %v", res.ID, err)
	}
	return res
}

// newManagerWith3AllocatedAnd1Ready builds a "p1" pool with 3 allocated
// resources + 1 ready resource already persisted (max_total 4 leaves zero
// headroom to provision more), under the given preheat policy, and returns a
// Manager over it. Shared fixture for the two #240 admission-gate tests
// below, which only differ in Preheat.MinReady and the EnsureReady count.
func newManagerWith3AllocatedAnd1Ready(t *testing.T, minReady, maxTotal int) *Manager {
	t.Helper()
	st := store.NewMemoryStore()
	ctx := context.Background()

	putAllocatedResources(t, st, "p1", 3)
	ready := putReadyResource(t, st, "p1", "res_ready_1")

	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: minReady, MaxTotal: maxTotal},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	return New(st, &fakeProvisioner{})
}

// TestManager_EnsureReady_SucceedsWhenRequestBelowMinReadyButWithinMaxTotal
// reproduces #240: a pool at max_total with min_ready configured higher than
// the caller's actual request must still satisfy the request from the ready
// resource already on hand, instead of treating min_ready as an admission
// floor. 3 allocated + 1 ready == max_total 4; requesting 1 ready must
// succeed even though min_ready is configured at 2.
func TestManager_EnsureReady_SucceedsWhenRequestBelowMinReadyButWithinMaxTotal(t *testing.T) {
	mgr := newManagerWith3AllocatedAnd1Ready(t, 2, 4)

	if err := mgr.EnsureReady(context.Background(), "p1", 1); err != nil {
		t.Fatalf("EnsureReady(1) = %v, want nil (1 ready resource should satisfy a request for 1)", err)
	}
}

// TestManager_EnsureReady_ErrorReportsCallersRequestedReadyNotConfiguredMinReady
// asserts that when a request genuinely cannot be satisfied, the resulting
// MaxTotalReachedError reports the caller's real requested count, not the
// preheat-widened min_ready target. MinReady is deliberately set higher (5)
// than the request (2) so this test would catch a regression: pre-fix the
// error reports "5" (max(2,5)); post-fix it reports the real "2".
func TestManager_EnsureReady_ErrorReportsCallersRequestedReadyNotConfiguredMinReady(t *testing.T) {
	mgr := newManagerWith3AllocatedAnd1Ready(t, 5, 4)

	err := mgr.EnsureReady(context.Background(), "p1", 2)
	if err == nil {
		t.Fatal("expected ensure ready to fail: only 1 ready resource can't satisfy 2 at max_total")
	}
	want := `pool "p1" is at max_total 4 (4 total, 1 ready), cannot satisfy requested ready count 2`
	if err.Error() != want {
		t.Fatalf("ensure ready error = %q, want %q", err.Error(), want)
	}
}

// TestManager_EnsureReady_BonusPreheatFailureAfterRequestSatisfiedIsNotFatal
// reproduces #249: EnsureReady's background top-up toward the pool's
// configured (wider) min_ready must not fail the caller once the caller's
// own request is already satisfiable from required provisioning alone.
// Policy MinReady=3 (widened target) vs a request for 1: the first
// provision (the caller's real requirement) succeeds, the next two (bonus
// preheat toward min_ready=3) fail — EnsureReady must still return nil,
// and the one required resource must actually be ready.
func TestManager_EnsureReady_BonusPreheatFailureAfterRequestSatisfiedIsNotFatal(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 3, MaxTotal: 10},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{failAfter: 1, provisionErr: errors.New("transient provider error")}
	mgr := New(st, prov)

	if err := mgr.EnsureReady(ctx, "p1", 1); err != nil {
		t.Fatalf("EnsureReady(1) = %v, want nil (the caller's own request was satisfiable; only bonus preheat provisioning failed)", err)
	}
	if prov.provisionCalls != 2 {
		t.Fatalf("provisionCalls = %d, want 2 (1 required success, then 1 bonus failure that stops further bonus attempts)", prov.provisionCalls)
	}

	updated, err := st.GetPool(ctx, "p1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if got := countReadyResources(updated.Inventory.Resources); got != 1 {
		t.Fatalf("ready resources = %d, want 1 (the required provision must have actually persisted)", got)
	}
}

// fakeAdmissionPublisher fails PublishResourceProvisioned on a specific
// call number (1-indexed), succeeding on every other call. Used to
// simulate the lifecycle event store's Append failing transiently for a
// bonus-preheat resource specifically (#249 follow-up: a code review
// caught that the admission-publish failure path had its own unconditional
// fatal return, uncovered by the Create/persist bonus tolerance above).
type fakeAdmissionPublisher struct {
	calls      int
	failOnCall int
	err        error
}

func (p *fakeAdmissionPublisher) PublishResourceProvisioned(_ context.Context, _ model.Resource) error {
	p.calls++
	if p.err != nil && p.calls == p.failOnCall {
		return p.err
	}
	return nil
}

// TestManager_EnsureReady_BonusAdmissionPublishFailureAfterRequestSatisfiedIsNotFatal
// extends the bonus-tolerance test above to the admission-publish step, not
// just the Create/persist step: with SetAdmissionPublisher configured, the
// caller's required resource (iteration 0) succeeds end to end, but the
// bonus resource's (iteration 1) PublishResourceProvisioned call fails.
// EnsureReady must still return nil — the failure doesn't strand the
// resource (it stays ResourceStateProvisioning and gets its admission
// event retried by the Observer's crash-recovery re-publish on the next
// pass), it just delays that one bonus resource's admission.
func TestManager_EnsureReady_BonusAdmissionPublishFailureAfterRequestSatisfiedIsNotFatal(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 3, MaxTotal: 10},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)
	publisher := &fakeAdmissionPublisher{failOnCall: 2, err: errors.New("event store append: disk contention")}
	mgr.SetAdmissionPublisher(publisher)

	if err := mgr.EnsureReady(ctx, "p1", 1); err != nil {
		t.Fatalf("EnsureReady(1) = %v, want nil (the caller's own request was satisfiable; only a bonus resource's admission publish failed)", err)
	}
	if prov.provisionCalls != 2 {
		t.Fatalf("provisionCalls = %d, want 2 (1 required, then 1 bonus that stops further attempts once its publish fails)", prov.provisionCalls)
	}
	if publisher.calls != 2 {
		t.Fatalf("admission publish calls = %d, want 2", publisher.calls)
	}
}

// TestManager_EnsureReady_BonusPersistFailureAfterCreateStaysFatal is the
// complement of the two bonus-tolerance tests above: when a bonus
// iteration's Create succeeds but the subsequent persist (store write)
// fails, EnsureReady must still return the fatal error rather than
// swallowing it as best-effort. Unlike a clean Create failure (nothing
// exists) or an admission-publish failure (the resource is durably
// persisted and gets retried), a persist failure after a successful
// Create leaves a real provider-side resource with no store record at
// all — that must stay loud, not become a masked orphan.
func TestManager_EnsureReady_BonusPersistFailureAfterCreateStaysFatal(t *testing.T) {
	ctx := context.Background()
	failStore := &putResourceFailAfterStore{
		Store:      store.NewMemoryStore(),
		failOnCall: 2,
		err:        errors.New("disk full"),
	}
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 3, MaxTotal: 10},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := failStore.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(failStore, prov)

	err := mgr.EnsureReady(ctx, "p1", 1)
	if err == nil {
		t.Fatal("expected EnsureReady to fail: a bonus iteration's Create succeeded but persist failed, leaving an untracked resource — must not be swallowed as best-effort")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("EnsureReady error = %q, want it to wrap the persist failure", err.Error())
	}
	if prov.provisionCalls != 2 {
		t.Fatalf("provisionCalls = %d, want 2 (required iteration succeeded, bonus iteration's Create also ran before its persist failed)", prov.provisionCalls)
	}
}

// TestManager_EnsureReady_RequiredProvisionFailureStaysFatal is the
// complement of the bonus-failure test above: when the caller's own
// request cannot be satisfied (the very first, required provision fails),
// EnsureReady must still return the fatal error exactly as before #249.
func TestManager_EnsureReady_RequiredProvisionFailureStaysFatal(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 10},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{provisionErr: errors.New("New-VHD failed: VMMS degraded")}
	mgr := New(st, prov)

	err := mgr.EnsureReady(ctx, "p1", 1)
	if err == nil {
		t.Fatal("expected EnsureReady to fail: the caller's own required provisioning failed, not just bonus preheat")
	}
	if !strings.Contains(err.Error(), "VMMS degraded") {
		t.Fatalf("EnsureReady error = %q, want it to wrap the underlying provisioning failure", err.Error())
	}
}

// TestManager_Reconcile_BackgroundProvisionFailureStaysFatal is a
// regression guard for #249: background Reconcile (requireMinReady=false)
// has no per-call requester to protect, so any provisioning failure toward
// the widened preheat target must remain fatal exactly as before — the new
// bonus/required split only applies to EnsureReady's synchronous path.
func TestManager_Reconcile_BackgroundProvisionFailureStaysFatal(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 3, MaxTotal: 10},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{failAfter: 1, provisionErr: errors.New("transient provider error")}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected background Reconcile to surface the provisioning failure, unlike EnsureReady's bonus-failure tolerance")
	}
}

// TestManager_EnsureReady_MaxTotalShortfallStillFatalWhenUnsatisfiable pins
// the invariant #249's fix depends on: requiredToProvision is only ever 0
// when the caller's request is already satisfiable. When it genuinely
// isn't (pool pinned at max_total, ready count below the request), the
// existing admission gate (maxTotalShortfall) must still fire and return
// *MaxTotalReachedError before any provisioning is even attempted — not
// silently succeed because every provision got misclassified as "bonus".
func TestManager_EnsureReady_MaxTotalShortfallStillFatalWhenUnsatisfiable(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	putAllocatedResources(t, st, "p1", 3)
	ready := putReadyResource(t, st, "p1", "res_ready_1")
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 5, MaxTotal: 4},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{provisionErr: errors.New("must never be called")}
	mgr := New(st, prov)

	err := mgr.EnsureReady(ctx, "p1", 2)
	var capErr *MaxTotalReachedError
	if !errors.As(err, &capErr) {
		t.Fatalf("EnsureReady error = %v, want *MaxTotalReachedError", err)
	}
	if prov.provisionCalls != 0 {
		t.Fatalf("provisionCalls = %d, want 0 (the admission gate must reject before attempting any provisioning)", prov.provisionCalls)
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

func TestManager_Reconcile_RecycleStale_SweepsDestroyingOrphanRegardlessOfOwner(t *testing.T) {
	// The stale/recycle path sweeps both transient states, not just
	// "recycling" — see orphanedTransientResources' doc comment for why a
	// "destroying" orphan can't be assumed to always be sandbox-owned (a
	// drain-orphaned resource needs this path to recover it once the pool
	// is un-drained). A resource that happens to be genuinely sandbox-owned
	// may occasionally get retried redundantly alongside
	// sandbox.DeletionReconciler's own retry; that's safe because every
	// driver's Delete is idempotent on an already-gone resource, so this
	// test asserts the sweep DOES act (the opposite of the old, narrower
	// design) rather than asserting it stays away.
	st := store.NewMemoryStore()
	ctx := context.Background()
	orphan := model.Resource{
		ID:         "res_destroying_orphan",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateDestroying,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, orphan); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != orphan.ID {
		t.Fatalf("destroyed = %v, want the destroying orphan %q retried", prov.destroyed, orphan.ID)
	}
	final, err := st.GetResource(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if final.State != model.ResourceStateDestroyed {
		t.Fatalf("state = %q, want %q", final.State, model.ResourceStateDestroyed)
	}
}

func TestManager_Reconcile_Drain_SweepsDestroyingOrphan(t *testing.T) {
	// Unlike the stale/recycle path, drain has no other retry mechanism for
	// a resource it left mid-teardown after a crash, so it must sweep both
	// transient states.
	st := store.NewMemoryStore()
	ctx := context.Background()
	orphan := model.Resource{
		ID:         "res_drain_orphan",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateDestroying,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Drain:     model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, orphan); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != orphan.ID {
		t.Fatalf("destroyed = %v, want drain to retry the orphaned resource %q", prov.destroyed, orphan.ID)
	}
	// applyDrain deletes the record entirely on success (unlike the
	// stale/recycle path, which leaves a "destroyed" record behind).
	if _, err := st.GetResource(ctx, orphan.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get resource after drain err = %v, want %v", err, store.ErrNotFound)
	}
}

func TestManager_Reconcile_RecycleStale_SweepsOrphanFromEarlierDrainAttempt(t *testing.T) {
	// A resource marked "destroying" by a crashed applyDrain, then left
	// behind after the operator clears the drain (fill), must still be
	// retried by the stale/recycle path: the drain branch won't run again
	// once the pool isn't drained, and this resource was never owned by a
	// sandbox, so sandbox.DeletionReconciler has no idea it exists. Without
	// this, the resource zombies forever — exactly what this feature exists
	// to prevent.
	st := store.NewMemoryStore()
	ctx := context.Background()
	orphan := model.Resource{
		ID:         "res_drain_then_filled",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateDestroying,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Drain:     model.PoolDrainState{Operator: false}, // drain has been cleared (fill)
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, orphan); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != orphan.ID {
		t.Fatalf("destroyed = %v, want the drain-orphaned resource %q retried after the pool was un-drained", prov.destroyed, orphan.ID)
	}
}

func TestManager_Reconcile_Drain_MarksDestroyingBeforeDestroy(t *testing.T) {
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

	var stateAtDestroy model.ResourceState
	prov := &fakeProvisioner{}
	prov.onDestroy = func(id model.ResourceID) {
		res, getErr := st.GetResource(ctx, id)
		if getErr != nil {
			t.Fatalf("get resource during destroy: %v", getErr)
		}
		stateAtDestroy = res.State
	}
	mgr := New(st, prov)
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if stateAtDestroy != model.ResourceStateDestroying {
		t.Fatalf("state observed at Destroy time = %q, want %q", stateAtDestroy, model.ResourceStateDestroying)
	}
}

func TestManager_Reconcile_Drain_SetsUpdatedAtBeforeDestroy(t *testing.T) {
	// DestroyResource and the stale/recycle path both stamp UpdatedAt when
	// persisting the transient state; applyDrain must match, or a resource
	// stuck mid-drain-teardown won't show when it actually entered that
	// state via .boxy/state.json — the exact observability gap this PR
	// exists to close, and one previously flagged (PR #119) for this
	// function's destroyed-marking path and never fixed.
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

	fixedTime := time.Unix(5000, 0).UTC()
	var updatedAtAtDestroy time.Time
	prov := &fakeProvisioner{}
	prov.onDestroy = func(id model.ResourceID) {
		res, getErr := st.GetResource(ctx, id)
		if getErr != nil {
			t.Fatalf("get resource during destroy: %v", getErr)
		}
		updatedAtAtDestroy = res.UpdatedAt
	}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: fixedTime})
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !updatedAtAtDestroy.Equal(fixedTime) {
		t.Fatalf("UpdatedAt at destroy time = %v, want %v (drain must stamp UpdatedAt like the other two destroy paths)", updatedAtAtDestroy, fixedTime)
	}
}

func TestManager_Reconcile_DrainProviderFailureLeavesDestroyingState(t *testing.T) {
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
	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("reconcile error = nil, want provider failure")
	}

	res, err := st.GetResource(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if res.State != model.ResourceStateDestroying {
		t.Fatalf("state after destroy error = %q, want %q (left mid-transition for retry)", res.State, model.ResourceStateDestroying)
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

func TestManager_Reconcile_ProvisionBackoffSkipsRetryUntilWindowElapses(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{provisionErr: errors.New("New-VHD failed: VMMS degraded")}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(2000, 0).UTC()})

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected first reconcile to surface provisioning failure")
	}
	if prov.provisionCalls != 1 {
		t.Fatalf("provisionCalls = %d, want 1", prov.provisionCalls)
	}

	// Immediately retrying should be skipped by backoff — no new provision attempt.
	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("second reconcile (should be a no-op skip): %v", err)
	}
	if prov.provisionCalls != 1 {
		t.Fatalf("provisionCalls after immediate retry = %d, want 1 (backoff should have skipped it)", prov.provisionCalls)
	}

	// Advance the clock past the backoff window; the next reconcile should retry.
	mgr.SetClock(fixedClock{t: time.Unix(2000, 0).Add(provisionBackoffBase + time.Second).UTC()})
	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected reconcile after backoff window to retry and surface the failure again")
	}
	if prov.provisionCalls != 2 {
		t.Fatalf("provisionCalls after backoff window elapsed = %d, want 2", prov.provisionCalls)
	}
}

func TestManager_ProvisionBackoff_SuccessClearsFailureState(t *testing.T) {
	mgr := New(store.NewMemoryStore(), &fakeProvisioner{})
	now := time.Unix(2000, 0).UTC()

	mgr.recordProvisionFailure("p1", now)
	if !mgr.provisionBackoffActive("p1", now) {
		t.Fatal("expected backoff to be active immediately after a failure")
	}

	mgr.recordProvisionSuccess("p1")
	if mgr.provisionBackoffActive("p1", now) {
		t.Fatal("expected success to clear backoff state")
	}

	// A fresh failure after a reset should back off from failCount=1 again
	// (base delay), not carry over the previous failure count into a longer
	// accumulated delay.
	mgr.recordProvisionFailure("p1", now)
	if mgr.provisionBackoffActive("p1", now.Add(provisionBackoffBase+time.Second)) {
		t.Fatal("expected backoff window to have elapsed using base delay, not an accumulated longer delay")
	}
}

func TestManager_ProvisionBackoff_EscalatesAndCaps(t *testing.T) {
	mgr := New(store.NewMemoryStore(), &fakeProvisioner{})
	now := time.Unix(2000, 0).UTC()

	mgr.recordProvisionFailure("p1", now) // failCount=1 -> base delay
	if mgr.provisionBackoffActive("p1", now.Add(provisionBackoffBase+time.Second)) {
		t.Fatal("expected first failure's backoff window to have elapsed")
	}

	mgr.recordProvisionFailure("p1", now) // failCount=2 -> 2x base delay
	if !mgr.provisionBackoffActive("p1", now.Add(provisionBackoffBase+time.Second)) {
		t.Fatal("expected second failure's backoff window to still be active after only base delay")
	}

	for range 10 {
		mgr.recordProvisionFailure("p1", now)
	}
	if mgr.provisionBackoffActive("p1", now.Add(provisionBackoffMax+time.Second)) {
		t.Fatal("expected backoff delay to be capped at provisionBackoffMax")
	}
}

func TestManager_EnsureReady_IgnoresProvisionBackoff(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name: "p1",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5},
		},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{provisionErr: errors.New("degraded host")}
	mgr := New(st, prov)
	mgr.SetClock(fixedClock{t: time.Unix(2000, 0).UTC()})

	// Trigger backoff via a background-style reconcile pass.
	if err := mgr.EnsureReady(ctx, "p1", 1); err == nil {
		t.Fatal("expected first EnsureReady to surface provisioning failure")
	}
	if prov.provisionCalls != 1 {
		t.Fatalf("provisionCalls = %d, want 1", prov.provisionCalls)
	}

	// EnsureReady is user/allocation-triggered and should always attempt,
	// even while background reconcile would be in a backoff window.
	if err := mgr.EnsureReady(ctx, "p1", 1); err == nil {
		t.Fatal("expected second EnsureReady to also attempt and surface the failure")
	}
	if prov.provisionCalls != 2 {
		t.Fatalf("provisionCalls = %d, want 2 (EnsureReady must not be skipped by backoff)", prov.provisionCalls)
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

func TestQuarantinedOrphans_MatchesErrorStateWithMatchingOriginPool(t *testing.T) {
	resources := []model.Resource{
		{ID: "q1", OriginPool: "p1", State: model.ResourceStateError},
		{ID: "ready1", OriginPool: "p1", State: model.ResourceStateReady},
		{ID: "q2", OriginPool: "p2", State: model.ResourceStateError}, // different pool
	}
	got := quarantinedOrphans("p1", resources)
	if len(got) != 1 || got[0].ID != "q1" {
		t.Fatalf("quarantinedOrphans = %+v, want only q1", got)
	}
}

func TestManager_Reconcile_ProvisionFailureWritesQuarantinedResource(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	prov := &fakeProvisioner{
		provisionErr:         errors.New("driver create for pool \"p1\": boom"),
		provisionResultOnErr: model.Resource{ID: "quarantine-1", OriginPool: "p1", Provider: model.ProviderRef{Name: "prov_1"}, State: model.ResourceStateError, Properties: map[string]any{"quarantine_reason": "boom"}},
	}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected Reconcile to surface the provision failure")
	}

	res, err := st.GetResource(ctx, "quarantine-1")
	if err != nil {
		t.Fatalf("expected the quarantined resource to be written to the store: %v", err)
	}
	if res.State != model.ResourceStateError || res.OriginPool != "p1" {
		t.Fatalf("quarantined resource = %+v, want State=Error OriginPool=p1", res)
	}
}

// TestManager_Reconcile_ProvisionFailureRecordsBackoffEvenIfQuarantineWriteFails
// covers a gap Copilot flagged in review: the quarantine-record PutResource
// call sits between Provision failing and recordProvisionFailure being
// called, so if that store write itself fails (e.g. transient I/O), the
// actuator returned before ever recording the failure — leaving backoff
// inactive and the pool free to retry provisioning on every reconcile tick
// instead of backing off.
func TestManager_Reconcile_ProvisionFailureRecordsBackoffEvenIfQuarantineWriteFails(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	putErr := errors.New("transient store I/O error")
	failingStore := &putResourceFailStore{Store: st, err: putErr}
	prov := &fakeProvisioner{
		provisionErr:         errors.New("driver create for pool \"p1\": boom"),
		provisionResultOnErr: model.Resource{ID: "quarantine-1", OriginPool: "p1", Provider: model.ProviderRef{Name: "prov_1"}, State: model.ResourceStateError},
	}
	mgr := New(failingStore, prov)
	now := time.Unix(2000, 0).UTC()
	mgr.SetClock(fixedClock{t: now})

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected Reconcile to surface the quarantine-record store failure")
	} else if !errors.Is(err, putErr) {
		t.Fatalf("Reconcile err = %v, want it to wrap the store's PutResource error", err)
	}

	if !mgr.provisionBackoffActive("p1", now) {
		t.Fatal("expected the provision failure to be recorded for backoff even though the quarantine-record store write failed")
	}
}

func TestManager_Reconcile_SweepsQuarantinedOrphan(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	quarantined := model.Resource{
		ID:         "quarantine-1",
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateError,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, quarantined); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(prov.destroyed) != 1 || prov.destroyed[0] != quarantined.ID {
		t.Fatalf("destroyed = %v, want quarantined resource %q swept", prov.destroyed, quarantined.ID)
	}
	final, err := st.GetResource(ctx, quarantined.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if final.State != model.ResourceStateDestroyed {
		t.Fatalf("final state = %q, want %q", final.State, model.ResourceStateDestroyed)
	}
}

// TestManager_Reconcile_PersistentQuarantineDestroyFailureBlocksProvisioning
// documents a known tradeoff, not desired behavior: the actuator's stale-
// destroy loop runs before its provision loop and returns on the first
// error (manager.go's reconcileLocked Actuator), a pre-existing property
// this plan's quarantinedOrphans sweep (#174 Task 5) inherits unchanged —
// see quarantinedOrphans' doc comment and the design spec's Fix 3. A
// quarantined resource is quarantined precisely because deleteBestEffort
// already retried and failed, so if the provider keeps failing to destroy
// it, every reconcile pass for that pool aborts here before provisioning
// anything, until either the destroy eventually succeeds or an operator
// intervenes. Not addressed by this plan; flagged here so a regression in
// this specific interaction is caught by CI rather than only in review.
func TestManager_Reconcile_PersistentQuarantineDestroyFailureBlocksProvisioning(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	quarantined := model.Resource{
		ID:         "quarantine-1",
		OriginPool: "p1",
		Provider:   model.ProviderRef{Name: "prov_1"},
		State:      model.ResourceStateError,
		CreatedAt:  time.Unix(0, 0).UTC(),
	}
	pool := model.Pool{
		Name:      "p1",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 5}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.PutResource(ctx, quarantined); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	prov := &fakeProvisioner{destroyErr: errors.New("hyperv cleanup: VM still present")}
	mgr := New(st, prov)

	if err := mgr.Reconcile(ctx, "p1"); err == nil {
		t.Fatal("expected reconcile to surface the persistent destroy failure")
	}
	if prov.provisionCalls != 0 {
		t.Fatalf("provisionCalls = %d, want 0 — the pool should not fill while the quarantined resource blocks the stale-destroy loop", prov.provisionCalls)
	}
}

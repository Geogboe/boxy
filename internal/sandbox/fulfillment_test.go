package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeAllocator struct{}

func (fakeAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (map[string]any, error) {
	_ = ctx
	_ = p
	return map[string]any{"allocated": true, "resource_id": string(r.ID)}, nil
}

type failingAllocator struct {
	failPool model.PoolName
}

func (a failingAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (map[string]any, error) {
	_ = ctx
	_ = r
	if p.Name == a.failPool {
		return nil, fmt.Errorf("allocator failed for pool %s", p.Name)
	}
	return map[string]any{"allocated": true}, nil
}

type fakeFulfillProvisioner struct {
	nextID   int
	failPool model.PoolName
}

func (p *fakeFulfillProvisioner) Provision(ctx context.Context, pl model.Pool) (model.Resource, error) {
	_ = ctx
	if pl.Name == p.failPool {
		return model.Resource{}, fmt.Errorf("provision %s: boom", pl.Name)
	}
	p.nextID++
	return model.Resource{
		ID:        model.ResourceID(fmt.Sprintf("res-%d", p.nextID)),
		Type:      pl.Inventory.ExpectedType,
		Profile:   pl.Inventory.ExpectedProfile,
		Provider:  model.ProviderRef{Name: "fake"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(int64(1000+p.nextID), 0).UTC(),
		UpdatedAt: time.Unix(int64(1000+p.nextID), 0).UTC(),
	}, nil
}

func (p *fakeFulfillProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = ctx
	_ = pool
	_ = res
	return nil
}

func TestFulfiller_ReconcilePending_MarksSandboxReady(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	ready := model.Resource{
		ID:        "res-ready",
		Type:      model.ResourceTypeContainer,
		Profile:   "kali",
		Provider:  model.ProviderRef{Name: "fake"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "kali",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "kali",
			Resources:       []model.Resource{ready},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}))
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status != model.SandboxStatusReady {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusReady)
	}
	if got.Error != "" {
		t.Fatalf("error = %q, want empty", got.Error)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources len = %d, want 1", len(got.Resources))
	}

	res, err := st.GetResource(ctx, got.Resources[0])
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if res.State != model.ResourceStateAllocated {
		t.Fatalf("resource state = %q, want %q", res.State, model.ResourceStateAllocated)
	}
}

func TestFulfiller_ReconcilePending_ProvisionsResourcesBeforeAllocation(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	if err := st.PutPool(ctx, model.Pool{
		Name: "kali",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MaxTotal: 2},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "kali",
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	prov := &fakeFulfillProvisioner{}
	f := NewFulfiller(st, pool.New(st, prov), New(st, fakeAllocator{}))
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status != model.SandboxStatusReady {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusReady)
	}
	if prov.nextID != 1 {
		t.Fatalf("provision count = %d, want 1", prov.nextID)
	}
}

func TestFulfiller_ReconcilePending_MarksSandboxFailedWhenNoMatchingPool(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "missing", Count: 1}},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}))
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status != model.SandboxStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusFailed)
	}
	if !strings.Contains(got.Error, "no pool matches request") {
		t.Fatalf("error = %q, want matching-pool failure", got.Error)
	}
	if len(got.Resources) != 0 {
		t.Fatalf("resources len = %d, want 0", len(got.Resources))
	}
}

func TestFulfiller_ReconcilePending_DoesNotPartiallyAllocateAcrossGroups(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	ready := model.Resource{
		ID:        "res-ready",
		Type:      model.ResourceTypeContainer,
		Profile:   "kali",
		Provider:  model.ProviderRef{Name: "fake"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(1, 0).UTC(),
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "kali",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "kali",
			Resources:       []model.Resource{ready},
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	sb := model.Sandbox{
		ID:     "sb-1",
		Name:   "lab",
		Status: model.SandboxStatusPending,
		Requests: []model.ResourceRequest{
			{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1},
			{Type: model.ResourceTypeVM, Profile: "missing", Count: 1},
		},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}))
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status != model.SandboxStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusFailed)
	}
	if len(got.Resources) != 0 {
		t.Fatalf("resources len = %d, want 0", len(got.Resources))
	}

	poolAfter, err := st.GetPool(ctx, "kali")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(poolAfter.Inventory.Resources) != 1 {
		t.Fatalf("pool inventory len = %d, want 1", len(poolAfter.Inventory.Resources))
	}

	resAfter, err := st.GetResource(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if resAfter.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want %q", resAfter.State, model.ResourceStateReady)
	}
}

func TestFulfiller_ReconcilePending_RollsBackEarlierAllocationsWhenLaterGroupFails(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	webRes := model.Resource{
		ID:        "res-web",
		Type:      model.ResourceTypeContainer,
		Profile:   "web",
		Provider:  model.ProviderRef{Name: "fake"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(1, 0).UTC(),
	}
	winRes := model.Resource{
		ID:        "res-win",
		Type:      model.ResourceTypeVM,
		Profile:   "win",
		Provider:  model.ProviderRef{Name: "fake"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(2, 0).UTC(),
		UpdatedAt: time.Unix(2, 0).UTC(),
	}
	for _, res := range []model.Resource{webRes, winRes} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put resource %q: %v", res.ID, err)
		}
	}
	for _, pl := range []model.Pool{
		{
			Name: "web",
			Inventory: model.ResourceCollection{
				ExpectedType:    model.ResourceTypeContainer,
				ExpectedProfile: "web",
				Resources:       []model.Resource{webRes},
			},
		},
		{
			Name: "win",
			Inventory: model.ResourceCollection{
				ExpectedType:    model.ResourceTypeVM,
				ExpectedProfile: "win",
				Resources:       []model.Resource{winRes},
			},
		},
	} {
		if err := st.PutPool(ctx, pl); err != nil {
			t.Fatalf("put pool %q: %v", pl.Name, err)
		}
	}

	sb := model.Sandbox{
		ID:     "sb-1",
		Name:   "lab",
		Status: model.SandboxStatusPending,
		Requests: []model.ResourceRequest{
			{Type: model.ResourceTypeContainer, Profile: "web", Count: 1},
			{Type: model.ResourceTypeVM, Profile: "win", Count: 1},
		},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, failingAllocator{failPool: "win"}))
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status != model.SandboxStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusFailed)
	}
	if len(got.Resources) != 0 {
		t.Fatalf("resources len = %d, want 0", len(got.Resources))
	}
	if !strings.Contains(got.Error, "allocator failed for pool win") {
		t.Fatalf("error = %q, want allocator failure", got.Error)
	}

	for _, poolName := range []model.PoolName{"web", "win"} {
		pl, err := st.GetPool(ctx, poolName)
		if err != nil {
			t.Fatalf("get pool %q: %v", poolName, err)
		}
		if len(pl.Inventory.Resources) != 1 {
			t.Fatalf("pool %q inventory len = %d, want 1", poolName, len(pl.Inventory.Resources))
		}
		if pl.Inventory.Resources[0].State != model.ResourceStateReady {
			t.Fatalf("pool %q resource state = %q, want %q", poolName, pl.Inventory.Resources[0].State, model.ResourceStateReady)
		}
	}

	for _, resID := range []model.ResourceID{webRes.ID, winRes.ID} {
		res, err := st.GetResource(ctx, resID)
		if err != nil {
			t.Fatalf("get resource %q: %v", resID, err)
		}
		if res.State != model.ResourceStateReady {
			t.Fatalf("resource %q state = %q, want %q", resID, res.State, model.ResourceStateReady)
		}
	}
}

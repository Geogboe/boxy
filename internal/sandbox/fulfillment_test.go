package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeAllocator struct{}

func (fakeAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (providersdk.AllocationResult, error) {
	_ = ctx
	_ = p
	return providersdk.AllocationResult{Properties: map[string]any{"allocated": true, "resource_id": string(r.ID)}}, nil
}

type failingAllocator struct {
	failPool model.PoolName
}

func (a failingAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (providersdk.AllocationResult, error) {
	_ = ctx
	_ = r
	if p.Name == a.failPool {
		return providersdk.AllocationResult{}, fmt.Errorf("allocator failed for pool %s", p.Name)
	}
	return providersdk.AllocationResult{Properties: map[string]any{"allocated": true}}, nil
}

// personalizeTimeoutAllocator simulates an allocation-time
// PersonalizeGuest timeout (#333) for one specific pool/resource, returning
// the same typed error AgentProvisioner.Allocate returns in that case so the
// fulfiller's rollback-quarantine handling can be exercised without a real
// agent.
type personalizeTimeoutAllocator struct {
	failPool model.PoolName
}

func (a personalizeTimeoutAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (providersdk.AllocationResult, error) {
	_ = ctx
	if p.Name != a.failPool {
		return providersdk.AllocationResult{Properties: map[string]any{"allocated": true}}, nil
	}
	return providersdk.AllocationResult{}, &pool.GuestPersonalizationTimeoutError{
		ResourceID:        r.ID,
		PoolName:          p.Name,
		AgentID:           "agent-1",
		Timeout:           50 * time.Millisecond,
		Elapsed:           60 * time.Millisecond,
		CredentialDeleted: true,
	}
}

type fakeFulfillProvisioner struct {
	nextID   int
	failPool model.PoolName
}

type deletingReadyEnsurer struct {
	store store.Store
}

type deletingFailingReadyEnsurer struct {
	store store.Store
}

func (e deletingReadyEnsurer) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	_ = poolName
	_ = minReady
	sb, err := e.store.GetSandbox(ctx, "sb-1")
	if err != nil {
		return err
	}
	sb.Status = model.SandboxStatusDeleting
	return e.store.PutSandbox(ctx, sb)
}

func (e deletingFailingReadyEnsurer) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	_ = poolName
	_ = minReady
	sb, err := e.store.GetSandbox(ctx, "sb-1")
	if err != nil {
		return err
	}
	sb.Status = model.SandboxStatusDeleting
	if err := e.store.PutSandbox(ctx, sb); err != nil {
		return err
	}
	return fmt.Errorf("ensure failed after delete request")
}

func (p *fakeFulfillProvisioner) Provision(ctx context.Context, pl model.Pool) (model.Resource, error) {
	_ = ctx
	if pl.Name == p.failPool {
		return model.Resource{}, fmt.Errorf("provision %s: boom", pl.Name)
	}
	p.nextID++
	return model.Resource{
		ID:         model.ResourceID(fmt.Sprintf("res-%d", p.nextID)),
		Type:       pl.Inventory.ExpectedType,
		Profile:    pl.Inventory.ExpectedProfile,
		OriginPool: pl.Name,
		Provider:   model.ProviderRef{Name: "fake"},
		State:      model.ResourceStateReady,
		CreatedAt:  time.Unix(int64(1000+p.nextID), 0).UTC(),
		UpdatedAt:  time.Unix(int64(1000+p.nextID), 0).UTC(),
	}, nil
}

func (p *fakeFulfillProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = ctx
	_ = pool
	_ = res
	return nil
}

type deletingFailingAllocator struct {
	store    store.Store
	failPool model.PoolName
}

func (a deletingFailingAllocator) Allocate(ctx context.Context, p model.Pool, r model.Resource) (providersdk.AllocationResult, error) {
	_ = r
	if p.Name != a.failPool {
		return providersdk.AllocationResult{Properties: map[string]any{"allocated": true}}, nil
	}
	sb, err := a.store.GetSandbox(ctx, "sb-1")
	if err != nil {
		return providersdk.AllocationResult{}, err
	}
	sb.Status = model.SandboxStatusDeleting
	if err := a.store.PutSandbox(ctx, sb); err != nil {
		return providersdk.AllocationResult{}, err
	}
	return providersdk.AllocationResult{}, fmt.Errorf("allocator failed after delete request")
}

// poolNameDispatchEnsurer routes EnsureReady to a different fake/real
// implementation per pool name — used to give one sandbox's pool a
// hung/blocking ensurer while another sandbox's pool behaves normally in the
// same Reconcile pass (#333, Part B).
type poolNameDispatchEnsurer struct {
	ensurers map[model.PoolName]readyEnsurer
}

func (e poolNameDispatchEnsurer) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	if inner, ok := e.ensurers[poolName]; ok {
		return inner.EnsureReady(ctx, poolName, minReady)
	}
	return nil
}

// blockUntilCtxDoneEnsurer simulates a hung downstream agent operation
// (#333's motivating scenario) that only returns once its context is
// cancelled/expired — modeling the post-Part-A behavior where every
// agent-backed call is itself bounded and therefore actually respects ctx,
// rather than hanging forever unconditionally.
type blockUntilCtxDoneEnsurer struct{}

func (blockUntilCtxDoneEnsurer) EnsureReady(ctx context.Context, poolName model.PoolName, minReady int) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestFulfiller_Reconcile_StuckFirstSandboxDoesNotBlockSecond(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	if err := st.PutPool(ctx, model.Pool{
		Name:      "stuck",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: "stuck"},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-1",
		Name:     "stuck-sandbox",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "stuck", Count: 1}},
	}); err != nil {
		t.Fatalf("create sandbox sb-1: %v", err)
	}

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
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-2",
		Name:     "ok-sandbox",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}); err != nil {
		t.Fatalf("create sandbox sb-2: %v", err)
	}

	ensurer := poolNameDispatchEnsurer{ensurers: map[model.PoolName]readyEnsurer{
		"stuck": blockUntilCtxDoneEnsurer{},
		"kali":  pool.New(st, &fakeFulfillProvisioner{}),
	}}

	f := NewFulfiller(st, ensurer, New(st, fakeAllocator{}), 50*time.Millisecond)

	start := time.Now()
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("Reconcile took %v, want it bounded near the 50ms per-sandbox timeout, not blocked on sb-1", elapsed)
	}

	sb2, err := st.GetSandbox(ctx, "sb-2")
	if err != nil {
		t.Fatalf("get sandbox sb-2: %v", err)
	}
	if sb2.Status != model.SandboxStatusReady {
		t.Fatalf("sb-2 status = %q, want ready — a stuck sb-1 must not block sb-2 in the same Reconcile pass", sb2.Status)
	}

	sb1, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox sb-1: %v", err)
	}
	if sb1.Status == model.SandboxStatusPending || sb1.Status == model.SandboxStatusProvisioning {
		t.Fatalf("sb-1 status = %q, want it resolved (not left hanging) after its per-sandbox timeout", sb1.Status)
	}
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

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}), 0)
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

func TestFulfiller_ReconcileDeletingSandboxDoesNotAllocate(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:      "res-ready",
		Type:    model.ResourceTypeContainer,
		Profile: "kali",
		State:   model.ResourceStateReady,
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
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusDeleting,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}), 0)
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusDeleting || len(sb.Resources) != 0 {
		t.Fatalf("sandbox = %+v, want deleting without resources", sb)
	}
	res, err := st.GetResource(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if res.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want ready", res.State)
	}
}

func TestFulfiller_ReconcileStopsIfSandboxStartsDeletingAfterEnsureReady(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	ready := model.Resource{
		ID:      "res-ready",
		Type:    model.ResourceTypeContainer,
		Profile: "kali",
		State:   model.ResourceStateReady,
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
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, deletingReadyEnsurer{store: st}, New(st, fakeAllocator{}), 0)
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusDeleting || len(sb.Resources) != 0 {
		t.Fatalf("sandbox = %+v, want deletion to win race before allocation", sb)
	}
	res, err := st.GetResource(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if res.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want ready", res.State)
	}
}

func TestFulfiller_ReconcilePreservesDeletingWhenEnsureReadyFailsAfterDelete(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.PutPool(ctx, model.Pool{
		Name: "kali",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "kali",
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:       "sb-1",
		Name:     "lab",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "kali", Count: 1}},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, deletingFailingReadyEnsurer{store: st}, New(st, fakeAllocator{}), 0)
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusDeleting {
		t.Fatalf("status = %q, want deleting", sb.Status)
	}
	if sb.Error != "" {
		t.Fatalf("error = %q, want empty", sb.Error)
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
	f := NewFulfiller(st, pool.New(st, prov), New(st, fakeAllocator{}), 0)
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

func TestFulfiller_ReconcilePending_FailsWhenPoolHitsMaxTotal(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	ctx := context.Background()

	allocated := model.Resource{
		ID:         "res-allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    "kali",
		OriginPool: "kali",
		Provider:   model.ProviderRef{Name: "fake"},
		State:      model.ResourceStateAllocated,
		CreatedAt:  time.Unix(1, 0).UTC(),
		UpdatedAt:  time.Unix(1, 0).UTC(),
	}
	if err := st.PutResource(ctx, allocated); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "kali",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MaxTotal: 1},
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
	f := NewFulfiller(st, pool.New(st, prov), New(st, fakeAllocator{}), 0)
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
	if !strings.Contains(got.Error, `pool "kali" is at max_total 1`) {
		t.Fatalf("error = %q, want max_total failure", got.Error)
	}
	if prov.nextID != 0 {
		t.Fatalf("provision count = %d, want 0", prov.nextID)
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

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}), 0)
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

// TestFulfiller_Rollback_PersonalizeGuestTimeoutQuarantinesInsteadOfRestoring
// is the key behavioral/safety test for #333's allocation-time path: a
// PersonalizeGuest timeout must leave the affected resource marked Error
// (removed from ready pool inventory) rather than rolled back to Ready —
// handing a possibly-rotated-credential resource to the next caller would be
// unsafe (ADR-0010). Every other resource/pool in the same rollback is still
// restored normally.
func TestFulfiller_Rollback_PersonalizeGuestTimeoutQuarantinesInsteadOfRestoring(t *testing.T) {
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

	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:     "sb-1",
		Name:   "lab",
		Status: model.SandboxStatusPending,
		Requests: []model.ResourceRequest{
			{Type: model.ResourceTypeContainer, Profile: "web", Count: 1},
			{Type: model.ResourceTypeVM, Profile: "win", Count: 1},
		},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, personalizeTimeoutAllocator{failPool: "win"}), 0)
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusFailed {
		t.Fatalf("status = %q, want %q", sb.Status, model.SandboxStatusFailed)
	}
	if len(sb.Resources) != 0 {
		t.Fatalf("sandbox resources = %v, want none", sb.Resources)
	}

	// "web" is unaffected by the timeout: normal rollback restores it fully
	// to Ready inventory.
	webPool, err := st.GetPool(ctx, "web")
	if err != nil {
		t.Fatalf("get pool web: %v", err)
	}
	if len(webPool.Inventory.Resources) != 1 || webPool.Inventory.Resources[0].State != model.ResourceStateReady {
		t.Fatalf("web pool inventory = %+v, want res-web restored Ready", webPool.Inventory.Resources)
	}
	webAfter, err := st.GetResource(ctx, webRes.ID)
	if err != nil {
		t.Fatalf("get resource web: %v", err)
	}
	if webAfter.State != model.ResourceStateReady {
		t.Fatalf("res-web state = %q, want ready", webAfter.State)
	}

	// "win" is the resource the timeout named: it must be quarantined
	// (Error, excluded from ready inventory), NOT rolled back to Ready.
	winPool, err := st.GetPool(ctx, "win")
	if err != nil {
		t.Fatalf("get pool win: %v", err)
	}
	if len(winPool.Inventory.Resources) != 0 {
		t.Fatalf("win pool inventory = %+v, want res-win excluded (quarantined, not ready)", winPool.Inventory.Resources)
	}
	winAfter, err := st.GetResource(ctx, winRes.ID)
	if err != nil {
		t.Fatalf("get resource win: %v", err)
	}
	if winAfter.State != model.ResourceStateError {
		t.Fatalf("res-win state = %q, want %q (quarantined, not rolled back to ready)", winAfter.State, model.ResourceStateError)
	}
}

func TestFulfiller_RollbackPreservesDeletingAndRestoresInventory(t *testing.T) {
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

	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:     "sb-1",
		Name:   "lab",
		Status: model.SandboxStatusPending,
		Requests: []model.ResourceRequest{
			{Type: model.ResourceTypeContainer, Profile: "web", Count: 1},
			{Type: model.ResourceTypeVM, Profile: "win", Count: 1},
		},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, deletingFailingAllocator{store: st, failPool: "win"}), 0)
	if err := f.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusDeleting {
		t.Fatalf("status = %q, want deleting", sb.Status)
	}
	if len(sb.Resources) != 0 {
		t.Fatalf("sandbox resources = %v, want rollback to pre-allocation resources", sb.Resources)
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
			t.Fatalf("pool %q resource state = %q, want ready", poolName, pl.Inventory.Resources[0].State)
		}
	}
	for _, resID := range []model.ResourceID{webRes.ID, winRes.ID} {
		res, err := st.GetResource(ctx, resID)
		if err != nil {
			t.Fatalf("get resource %q: %v", resID, err)
		}
		if res.State != model.ResourceStateReady {
			t.Fatalf("resource %q state = %q, want ready", res.ID, res.State)
		}
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

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, fakeAllocator{}), 0)
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

	f := NewFulfiller(st, pool.New(st, &fakeFulfillProvisioner{}), New(st, failingAllocator{failPool: "win"}), 0)
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

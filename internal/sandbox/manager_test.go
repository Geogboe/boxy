package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestManager_CreateFromPool_ConsumesReadyResource(t *testing.T) {
	st := store.NewMemoryStore()

	r1 := model.Resource{
		ID:        "res_1",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	r2 := model.Resource{
		ID:        "res_2",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(2, 0).UTC(),
	}
	if err := st.PutResource(context.Background(), r1); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutResource(context.Background(), r2); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	pool := model.Pool{
		Name:      "docker-containers",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{r1, r2}},
	}
	if err := st.PutPool(context.Background(), pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	mgr := New(st, nil)
	sb, err := mgr.CreateFromPool(context.Background(), "docker-containers", 1, "demo", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.ID == "" {
		t.Fatalf("expected sandbox id")
	}
	if len(sb.Resources) != 1 {
		t.Fatalf("expected 1 resource id in sandbox, got %d", len(sb.Resources))
	}

	updatedPool, err := st.GetPool(context.Background(), "docker-containers")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(updatedPool.Inventory.Resources) != 1 {
		t.Fatalf("expected pool inventory size 1, got %d", len(updatedPool.Inventory.Resources))
	}

	updatedRes, err := st.GetResource(context.Background(), sb.Resources[0])
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if updatedRes.State != model.ResourceStateAllocated {
		t.Fatalf("expected resource allocated, got %q", updatedRes.State)
	}
}

func TestManager_AddFromPool_PreservesSandboxStatusUntilCallerFinalizes(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	ready := model.Resource{
		ID:        "res_1",
		Type:      model.ResourceTypeContainer,
		Profile:   model.ResourceProfileDefault,
		Provider:  model.ProviderRef{Name: "prov_1"},
		State:     model.ResourceStateReady,
		CreatedAt: time.Unix(1, 0).UTC(),
	}

	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	pool := model.Pool{
		Name:      "docker-containers",
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 0}},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{ready}},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}

	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "demo",
		Status:   model.SandboxStatusProvisioning,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: model.ResourceProfileDefault, Count: 1}},
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	mgr := New(st, nil)
	got, err := mgr.AddFromPool(ctx, sb.ID, pool.Name, 1)
	if err != nil {
		t.Fatalf("add from pool: %v", err)
	}

	if got.Status != model.SandboxStatusProvisioning {
		t.Fatalf("status = %q, want %q", got.Status, model.SandboxStatusProvisioning)
	}

	stored, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if stored.Status != model.SandboxStatusProvisioning {
		t.Fatalf("stored status = %q, want %q", stored.Status, model.SandboxStatusProvisioning)
	}
	if len(stored.Resources) != 1 {
		t.Fatalf("resources len = %d, want 1", len(stored.Resources))
	}
}

func TestManager_AddFromPool_RejectsDeletingSandboxBeforeConsumingResource(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	ready := model.Resource{
		ID:      "res-1",
		Type:    model.ResourceTypeContainer,
		Profile: model.ResourceProfileDefault,
		State:   model.ResourceStateReady,
	}
	pool := model.Pool{
		Name: "docker-containers",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       []model.Resource{ready},
		},
	}
	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "demo",
		Status:   model.SandboxStatusDeleting,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: model.ResourceProfileDefault, Count: 1}},
	}
	if err := st.PutResource(ctx, ready); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	_, err := New(st, nil).AddFromPool(ctx, sb.ID, pool.Name, 1)
	if err != ErrSandboxDeleting {
		t.Fatalf("AddFromPool error = %v, want ErrSandboxDeleting", err)
	}

	storedSandbox, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if len(storedSandbox.Resources) != 0 {
		t.Fatalf("sandbox resources = %v, want empty", storedSandbox.Resources)
	}
	storedPool, err := st.GetPool(ctx, pool.Name)
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(storedPool.Inventory.Resources) != 1 || storedPool.Inventory.Resources[0].ID != ready.ID {
		t.Fatalf("pool inventory = %+v, want ready resource still present", storedPool.Inventory.Resources)
	}
	storedResource, err := st.GetResource(ctx, ready.ID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if storedResource.State != model.ResourceStateReady {
		t.Fatalf("resource state = %q, want ready", storedResource.State)
	}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestManager_CreateRequested_ComputesExpiryFromAutoDestroyAfter(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	mgr := New(st, nil)
	now := time.Unix(1000, 0).UTC()
	mgr.SetClock(fixedClock{t: now})

	sb, err := mgr.CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "30m"}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}
	if sb.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	want := now.Add(30 * time.Minute)
	if !sb.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", sb.ExpiresAt, want)
	}

	stored, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(want) {
		t.Fatalf("stored ExpiresAt = %v, want %v", stored.ExpiresAt, want)
	}
}

func TestManager_CreateRequested_NoAutoDestroyAfterMeansNoExpiry(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	sb, err := New(st, nil).CreateRequested(ctx, "demo", model.SandboxPolicies{}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}
	if sb.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", sb.ExpiresAt)
	}
}

func TestManager_CreateRequested_RejectsInvalidAutoDestroyAfter(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	_, err := New(st, nil).CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "not-a-duration"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid auto_destroy_after")
	}
}

func TestManager_CreateRequested_RejectsNonPositiveAutoDestroyAfter(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	_, err := New(st, nil).CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "-5m"}, nil)
	if err == nil {
		t.Fatal("expected error for non-positive auto_destroy_after")
	}
}

func TestManager_RequestExtend_PushesExpiryFromCurrentDeadline(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	mgr := New(st, nil)
	now := time.Unix(1000, 0).UTC()
	mgr.SetClock(fixedClock{t: now})

	sb, err := mgr.CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "10m"}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}
	originalExpiry := *sb.ExpiresAt

	extended, err := mgr.RequestExtend(ctx, sb.ID, 15*time.Minute)
	if err != nil {
		t.Fatalf("RequestExtend: %v", err)
	}
	want := originalExpiry.Add(15 * time.Minute)
	if extended.ExpiresAt == nil || !extended.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", extended.ExpiresAt, want)
	}

	stored, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(want) {
		t.Fatalf("stored ExpiresAt = %v, want %v", stored.ExpiresAt, want)
	}
}

func TestManager_RequestExtend_FailsWithoutExistingExpiry(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	sb, err := New(st, nil).CreateRequested(ctx, "demo", model.SandboxPolicies{}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}

	_, err = New(st, nil).RequestExtend(ctx, sb.ID, 10*time.Minute)
	if err != ErrNoExpiry {
		t.Fatalf("RequestExtend error = %v, want ErrNoExpiry", err)
	}
}

func TestManager_RequestExtend_RejectsDeletingSandbox(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	mgr := New(st, nil)
	sb, err := mgr.CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "10m"}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}
	if _, err := mgr.RequestDelete(ctx, sb.ID); err != nil {
		t.Fatalf("RequestDelete: %v", err)
	}

	_, err = mgr.RequestExtend(ctx, sb.ID, 10*time.Minute)
	if err != ErrSandboxDeleting {
		t.Fatalf("RequestExtend error = %v, want ErrSandboxDeleting", err)
	}
}

func TestManager_RequestExtend_RejectsNonPositiveExtension(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	mgr := New(st, nil)
	sb, err := mgr.CreateRequested(ctx, "demo", model.SandboxPolicies{AutoDestroyAfter: "10m"}, nil)
	if err != nil {
		t.Fatalf("CreateRequested: %v", err)
	}

	if _, err := mgr.RequestExtend(ctx, sb.ID, 0); err == nil {
		t.Fatal("expected error for zero extension")
	}
	if _, err := mgr.RequestExtend(ctx, sb.ID, -time.Minute); err == nil {
		t.Fatal("expected error for negative extension")
	}
}

func TestManager_RequestDelete_MarksDeletingAndIsIdempotent(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	sb := model.Sandbox{ID: "sb-1", Name: "demo", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}}
	if err := st.CreateSandbox(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	mgr := New(st, nil)
	first, err := mgr.RequestDelete(ctx, sb.ID)
	if err != nil {
		t.Fatalf("RequestDelete: %v", err)
	}
	if first.Status != model.SandboxStatusDeleting {
		t.Fatalf("status = %q, want deleting", first.Status)
	}

	second, err := mgr.RequestDelete(ctx, sb.ID)
	if err != nil {
		t.Fatalf("RequestDelete second: %v", err)
	}
	if second.Status != model.SandboxStatusDeleting || len(second.Resources) != 1 {
		t.Fatalf("second sandbox = %+v, want deleting with resource", second)
	}
}

// fakeSegmentTrackingAllocator adds NetworkIsolatingAllocator on top of a
// plain SandboxAllocator, mirroring the "capability on top of a base" shape
// used throughout this codebase's other optional-capability tests.
type fakeSegmentTrackingAllocator struct {
	createRef    providersdk.SegmentRef
	createType   providersdk.Type
	createErr    error
	attachErr    error
	createCalls  int
	attachCalls  int
	gotSandboxID model.SandboxID
}

func (f *fakeSegmentTrackingAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{}, nil
}
func (f *fakeSegmentTrackingAllocator) CreateSegment(_ context.Context, _ model.Pool, _ model.Resource, sandboxID model.SandboxID) (providersdk.SegmentRef, providersdk.Type, error) {
	f.createCalls++
	f.gotSandboxID = sandboxID
	return f.createRef, f.createType, f.createErr
}
func (f *fakeSegmentTrackingAllocator) AttachToSegment(context.Context, model.Pool, model.Resource, providersdk.SegmentRef) error {
	f.attachCalls++
	return f.attachErr
}

func TestManager_CreateFromPool_CreatesAndRecordsSegmentOnce(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	for _, res := range []model.Resource{
		{ID: "res-1", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
		{ID: "res-2", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
	} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("PutResource: %v", err)
		}
	}
	pool, _ := st.GetPool(ctx, "pool-a")
	pool.Inventory.Resources = []model.Resource{
		{ID: "res-1", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
		{ID: "res-2", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("PutPool: %v", err)
	}

	allocator := &fakeSegmentTrackingAllocator{createRef: "boxy-sb-sb-1", createType: "hyperv"}
	m := New(st, allocator)

	sb, err := m.CreateFromPool(ctx, "pool-a", 2, "test-sandbox", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if allocator.createCalls != 1 {
		t.Fatalf("CreateSegment called %d times, want exactly 1 (same sandbox, same agent, must not create twice)", allocator.createCalls)
	}
	if allocator.attachCalls != 2 {
		t.Fatalf("AttachToSegment called %d times, want 2 (once per resource)", allocator.attachCalls)
	}
	if len(sb.NetworkSegments) != 1 || sb.NetworkSegments[0].Ref != "boxy-sb-sb-1" || sb.NetworkSegments[0].ProviderType != "hyperv" || sb.NetworkSegments[0].AgentID != "agent-1" {
		t.Fatalf("NetworkSegments = %+v, want exactly one entry for agent-1", sb.NetworkSegments)
	}

	// Persisted, not just returned.
	persisted, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if len(persisted.NetworkSegments) != 1 {
		t.Fatalf("persisted sandbox NetworkSegments = %+v, want one entry", persisted.NetworkSegments)
	}
}

func TestManager_AddFromPool_ReusesExistingSegmentForSameAgent(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:              "sb-1",
		Status:          model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"}},
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-3", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-3", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{AgentID: "agent-1"}}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	allocator := &fakeSegmentTrackingAllocator{createRef: "should-not-be-used", createType: "hyperv"}
	m := New(st, allocator)

	sb, err := m.AddFromPool(ctx, "sb-1", "pool-a", 1)
	if err != nil {
		t.Fatalf("AddFromPool: %v", err)
	}
	if allocator.createCalls != 0 {
		t.Fatalf("CreateSegment called %d times, want 0 (must reuse the sandbox's existing segment for agent-1)", allocator.createCalls)
	}
	if allocator.attachCalls != 1 {
		t.Fatalf("AttachToSegment called %d times, want 1", allocator.attachCalls)
	}
	if len(sb.NetworkSegments) != 1 {
		t.Fatalf("NetworkSegments = %+v, want the original single entry unchanged", sb.NetworkSegments)
	}
}

func TestManager_CreateFromPool_PlainAllocatorSkipsSegmentsEntirely(t *testing.T) {
	// A SandboxAllocator that does NOT implement NetworkIsolatingAllocator
	// (e.g. devfactory-backed) must allocate exactly as it does today --
	// no error, no segment, matching this plan's Global Constraints.
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:      "pool-a",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-4", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-4", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	m := New(st, fakeAllocator{})

	sb, err := m.CreateFromPool(ctx, "pool-a", 1, "plain-sandbox", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if len(sb.NetworkSegments) != 0 {
		t.Fatalf("NetworkSegments = %+v, want none for a plain allocator", sb.NetworkSegments)
	}
}

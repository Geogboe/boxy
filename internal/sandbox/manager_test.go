package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

// segmentCall records one CreateSegment/AttachToSegment dispatch: which
// provider type it was made for, and which segment ref it used. Counts alone
// cannot show a segment being reused across the wrong provider type -- the
// exact defect the mixed-provider test below exists for.
type segmentCall struct {
	providerType providersdk.Type
	ref          providersdk.SegmentRef
}

// fakeSegmentTrackingAllocator adds NetworkIsolatingAllocator on top of a
// plain SandboxAllocator, mirroring the "capability on top of a base" shape
// used throughout this codebase's other optional-capability tests.
//
// CreateSegment derives the segment's provider type from the resource's own
// recorded Provider.Name, exactly like the real
// pool.AgentProvisioner.CreateSegment does (it returns the pool's resolved
// driver type, which is the same value ProvisionLocked stamped onto the
// resource). A fake that returned one fixed type regardless of the resource
// could not represent a sandbox spanning two provider types at all.
type fakeSegmentTrackingAllocator struct {
	createRef providersdk.SegmentRef
	// refsByProvider gives each provider type its own segment ref, the way
	// separate drivers on one agent really do hand back separate host
	// objects. A nil map means every provider gets createRef.
	refsByProvider map[providersdk.Type]providersdk.SegmentRef
	createErr      error
	attachErr      error
	createCalls    int
	attachCalls    int
	gotSandboxID   model.SandboxID
	attached       []segmentCall
}

func (f *fakeSegmentTrackingAllocator) Allocate(context.Context, model.Pool, model.Resource) (providersdk.AllocationResult, error) {
	return providersdk.AllocationResult{}, nil
}
func (f *fakeSegmentTrackingAllocator) CreateSegment(_ context.Context, _ model.Pool, res model.Resource, sandboxID model.SandboxID, cidr string) (providersdk.SegmentRef, providersdk.Type, error) {
	f.createCalls++
	f.gotSandboxID = sandboxID
	providerType := providersdk.Type(res.Provider.Name)
	ref := f.createRef
	if f.refsByProvider != nil {
		ref = f.refsByProvider[providerType]
	}
	return ref, providerType, f.createErr
}
func (f *fakeSegmentTrackingAllocator) AttachToSegment(_ context.Context, _ model.Pool, res model.Resource, ref providersdk.SegmentRef) error {
	f.attachCalls++
	f.attached = append(f.attached, segmentCall{providerType: providersdk.Type(res.Provider.Name), ref: ref})
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
	// Provider.Name is set on every fixture resource here because
	// pool.AgentProvisioner.ProvisionLocked stamps it onto every resource it
	// creates: it IS the resource's resolved provider type, and
	// ensureNetworkSegment keys segment reuse on it (see its doc comment).
	for _, res := range []model.Resource{
		{ID: "res-1", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}},
		{ID: "res-2", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}},
	} {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("PutResource: %v", err)
		}
	}
	pool, _ := st.GetPool(ctx, "pool-a")
	pool.Inventory.Resources = []model.Resource{
		{ID: "res-1", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}},
		{ID: "res-2", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}},
	}
	if err := st.PutPool(ctx, pool); err != nil {
		t.Fatalf("PutPool: %v", err)
	}

	allocator := &fakeSegmentTrackingAllocator{createRef: "boxy-sb-sb-1"}
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
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{{ID: "res-3", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}}}},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{ID: "res-3", OriginPool: "pool-a", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	allocator := &fakeSegmentTrackingAllocator{createRef: "should-not-be-used"}
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

// TestManager_AddFromPool_MixedProviderTypesOnOneAgentGetSeparateSegments is
// the regression test for the combined re-review's Critical finding: segment
// reuse keyed on AgentID alone, ignoring the segment's ProviderType.
//
// This is the DEFAULT daemon shape, not an exotic one -- internal/cli/serve.go
// builds a single embedded agent over every configured driver, so a sandbox
// that draws from a docker pool and a hyperv pool sees one agent ID for both.
// Keyed on AgentID alone, the second resource "reused" the first's docker
// network ref and handed it to the hyperv driver, which cannot address it:
// a hard error that fails the whole sandbox allocation. Each provider type on
// one agent must get its own segment.
func TestManager_AddFromPool_MixedProviderTypesOnOneAgentGetSeparateSegments(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	// Two pools of different provider types whose resources were provisioned
	// by the SAME agent -- what one embedded agent over both drivers produces.
	dockerRes := model.Resource{ID: "res-docker", Type: model.ResourceTypeContainer, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}}
	hypervRes := model.Resource{ID: "res-hyperv", Type: model.ResourceTypeVM, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}}
	for _, p := range []model.Pool{
		{Name: "pool-docker", Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{dockerRes}}},
		{Name: "pool-hyperv", Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault, Resources: []model.Resource{hypervRes}}},
	} {
		if err := st.PutPool(ctx, p); err != nil {
			t.Fatalf("PutPool %q: %v", p.Name, err)
		}
	}
	for poolName, res := range map[model.PoolName]model.Resource{"pool-docker": dockerRes, "pool-hyperv": hypervRes} {
		stored := res
		stored.OriginPool = poolName
		if err := st.PutResource(ctx, stored); err != nil {
			t.Fatalf("PutResource %q: %v", res.ID, err)
		}
	}

	allocator := &fakeSegmentTrackingAllocator{refsByProvider: map[providersdk.Type]providersdk.SegmentRef{
		"docker": "docker-net-1",
		"hyperv": "boxy-sb-sb-1",
	}}
	m := New(st, allocator)

	if _, err := m.AddFromPool(ctx, "sb-1", "pool-docker", 1); err != nil {
		t.Fatalf("AddFromPool(pool-docker): %v", err)
	}
	sb, err := m.AddFromPool(ctx, "sb-1", "pool-hyperv", 1)
	if err != nil {
		t.Fatalf("AddFromPool(pool-hyperv): %v", err)
	}

	if allocator.createCalls != 2 {
		t.Fatalf("CreateSegment called %d times, want 2 (one segment per provider type, even on one agent)", allocator.createCalls)
	}
	wantRefs := map[string]string{"docker": "docker-net-1", "hyperv": "boxy-sb-sb-1"}
	if len(sb.NetworkSegments) != len(wantRefs) {
		t.Fatalf("NetworkSegments = %+v, want one per provider type: %+v", sb.NetworkSegments, wantRefs)
	}
	seenCIDRs := map[string]bool{}
	for _, seg := range sb.NetworkSegments {
		wantRef, ok := wantRefs[seg.ProviderType]
		if !ok || seg.Ref != wantRef || seg.AgentID != "agent-1" {
			t.Fatalf("segment %+v is not one of the expected per-provider segments %+v", seg, wantRefs)
		}
		// Every segment carries its allocated range, and no two share one:
		// two segments on the same agent overlapping is the collision class
		// that breaks cross-host mesh peering (#370).
		if seg.CIDR == "" {
			t.Fatalf("segment %+v has no CIDR recorded", seg)
		}
		if seenCIDRs[seg.CIDR] {
			t.Fatalf("two segments share CIDR %q: %+v", seg.CIDR, sb.NetworkSegments)
		}
		seenCIDRs[seg.CIDR] = true
	}

	// The load-bearing assertion: each resource must have been attached to
	// its OWN provider's segment. Reusing by AgentID alone attaches the
	// hyperv resource to "docker-net-1", which is what really breaks.
	wantAttached := []segmentCall{
		{providerType: "docker", ref: "docker-net-1"},
		{providerType: "hyperv", ref: "boxy-sb-sb-1"},
	}
	if len(allocator.attached) != len(wantAttached) {
		t.Fatalf("AttachToSegment calls = %+v, want %+v", allocator.attached, wantAttached)
	}
	for i, got := range allocator.attached {
		if got != wantAttached[i] {
			t.Fatalf("attach %d: provider %q got segment %q, want %q -- a segment created for a different provider type cannot be addressed by this one",
				i, got.providerType, got.ref, wantAttached[i].ref)
		}
	}

	persisted, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if len(persisted.NetworkSegments) != len(wantRefs) {
		t.Fatalf("persisted NetworkSegments = %+v, want both recorded so deletion tears both down", persisted.NetworkSegments)
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

// fakeMeshPeeringAllocator adds MeshPeeringAllocator on top of
// fakeSegmentTrackingAllocator, mirroring this file's existing
// "capability on top of a base" fake shape.
type fakeMeshPeeringAllocator struct {
	*fakeSegmentTrackingAllocator
	identities map[string]struct{ pub, endpoint, cidr string } // keyed by agentID
	peerCalls  []struct{ toAgentID, peerPublicKey, peerEndpoint, peerCIDR string }
	// identityErr, when set, makes every MeshIdentity call fail, as a host
	// that can't create a WireGuard device does.
	identityErr error
}

func (f *fakeMeshPeeringAllocator) MeshIdentity(_ context.Context, _ providersdk.Type, agentID string, _ providersdk.SegmentRef) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	id := f.identities[agentID]
	return id.pub, id.endpoint, id.cidr, nil
}
func (f *fakeMeshPeeringAllocator) AddMeshPeer(_ context.Context, _ providersdk.Type, agentID string, _ providersdk.SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error {
	f.peerCalls = append(f.peerCalls, struct{ toAgentID, peerPublicKey, peerEndpoint, peerCIDR string }{agentID, peerPublicKey, peerEndpoint, peerCIDR})
	return nil
}

func TestManager_EnsureNetworkSegment_PeersWhenSandboxSpansTwoAgents(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{
		ID:     "sb-1",
		Status: model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"},
		},
	}

	allocator := &fakeMeshPeeringAllocator{
		fakeSegmentTrackingAllocator: &fakeSegmentTrackingAllocator{createRef: "boxy-sb-sb-1"},
		identities: map[string]struct{ pub, endpoint, cidr string }{
			"agent-1": {"pubkey-1", "203.0.113.1:51820", "10.250.0.0/29"},
			"agent-2": {"pubkey-2", "203.0.113.2:51820", "10.250.0.8/29"},
		},
	}
	m := New(st, allocator)

	res := model.Resource{ID: "res-2", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-2"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-b"}, res); err != nil {
		t.Fatalf("ensureNetworkSegment: %v", err)
	}

	if len(sb.NetworkSegments) != 2 {
		t.Fatalf("NetworkSegments = %+v, want 2 entries (sandbox now spans agent-1 and agent-2)", sb.NetworkSegments)
	}
	if len(allocator.peerCalls) != 2 {
		t.Fatalf("expected exactly 2 AddMeshPeer calls (agent-1<-agent-2's identity, agent-2<-agent-1's identity), got %d: %+v", len(allocator.peerCalls), allocator.peerCalls)
	}
}

// TestManager_EnsureNetworkSegment_MeshFailureDoesNotFailSandbox: until
// cross-host overlay traffic works (#379), a mesh setup error must not fail
// a sandbox whose resources are each usable on their own host.
func TestManager_EnsureNetworkSegment_MeshFailureDoesNotFailSandbox(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{
		ID:     "sb-1",
		Status: model.SandboxStatusReady,
		NetworkSegments: []model.NetworkSegment{
			{AgentID: "agent-1", ProviderType: "docker", Ref: "boxy-sb-sb-1"},
		},
	}
	allocator := &fakeMeshPeeringAllocator{
		fakeSegmentTrackingAllocator: &fakeSegmentTrackingAllocator{createRef: "boxy-sb-sb-1"},
		identityErr:                  errors.New(`create TUN device "wg-sb-1": operation not permitted`),
	}
	m := New(st, allocator)

	// Capture the warning: the manager logs through slog.Default.
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	res := model.Resource{ID: "res-2", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-2"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-b"}, res); err != nil {
		t.Fatalf("ensureNetworkSegment returned the mesh error, want it logged only: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(logged.Bytes(), &rec); err != nil {
		t.Fatalf("expected exactly one JSON log record, got %q: %v", logged.String(), err)
	}
	for key, want := range map[string]string{
		"level":      "WARN",
		"operation":  "mesh_peering",
		"agent_id":   "agent-2",
		"error_code": "mesh_device_unavailable",
	} {
		if rec[key] != want {
			t.Fatalf("log record %s = %v, want %q (record: %v)", key, rec[key], want, rec)
		}
	}
	if len(sb.NetworkSegments) != 2 {
		t.Fatalf("NetworkSegments = %+v, want agent-2's segment recorded despite the mesh failure", sb.NetworkSegments)
	}
}

func TestManager_EnsureNetworkSegment_SingleHostSandboxNeverTriggersMesh(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	sb := model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady}

	allocator := &fakeMeshPeeringAllocator{
		fakeSegmentTrackingAllocator: &fakeSegmentTrackingAllocator{createRef: "boxy-sb-sb-1"},
		identities:                   map[string]struct{ pub, endpoint, cidr string }{},
	}
	m := New(st, allocator)

	res := model.Resource{ID: "res-1", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-a"}, res); err != nil {
		t.Fatalf("ensureNetworkSegment: %v", err)
	}
	// A second resource from the SAME agent must not trigger mesh either.
	res2 := model.Resource{ID: "res-2", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}}
	if err := m.ensureNetworkSegment(ctx, &sb, model.Pool{Name: "pool-a"}, res2); err != nil {
		t.Fatalf("ensureNetworkSegment (2nd resource, same agent): %v", err)
	}

	if len(allocator.peerCalls) != 0 {
		t.Fatalf("expected 0 AddMeshPeer calls for a single-host sandbox, got %d: %+v", len(allocator.peerCalls), allocator.peerCalls)
	}
}

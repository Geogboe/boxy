package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
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

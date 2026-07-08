package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeDestroyer struct {
	destroyed []model.ResourceID
	err       error
	errOn     model.ResourceID
}

func (d *fakeDestroyer) DestroyResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	d.destroyed = append(d.destroyed, res.ID)
	if d.errOn != "" && res.ID == d.errOn {
		return d.err
	}
	if d.errOn != "" {
		return nil
	}
	return d.err
}

func TestDeletionReconciler_CleansResourcesAndDeletesSandbox(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{ID: "res-1", OriginPool: "web", State: model.ResourceStateAllocated}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusDeleting,
		Resources: []model.ResourceID{res.ID},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	destroyer := &fakeDestroyer{}
	if err := NewDeletionReconciler(st, destroyer).Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(destroyer.destroyed) != 1 || destroyer.destroyed[0] != res.ID {
		t.Fatalf("destroyed = %v, want [%s]", destroyer.destroyed, res.ID)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err != store.ErrNotFound {
		t.Fatalf("sandbox err = %v, want ErrNotFound", err)
	}
}

func TestDeletionReconciler_DestroyFailurePreservesDeletingSandboxForRetry(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{ID: "res-1", OriginPool: "web", State: model.ResourceStateAllocated}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusDeleting,
		Resources: []model.ResourceID{res.ID},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	errBoom := errors.New("boom")
	err := NewDeletionReconciler(st, &fakeDestroyer{err: errBoom}).Reconcile(ctx)
	if err == nil {
		t.Fatal("expected destroy error")
	}
	sb, getErr := st.GetSandbox(ctx, "sb-1")
	if getErr != nil {
		t.Fatalf("get sandbox: %v", getErr)
	}
	if sb.Status != model.SandboxStatusDeleting || len(sb.Resources) != 1 || sb.Resources[0] != res.ID {
		t.Fatalf("sandbox = %+v, want deleting with unresolved resource", sb)
	}
}

func TestDeletionReconciler_RecordsProgressBeforeRetryableFailure(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	resources := []model.Resource{
		{ID: "res-1", OriginPool: "web", State: model.ResourceStateAllocated},
		{ID: "res-2", OriginPool: "web", State: model.ResourceStateAllocated},
	}
	for _, res := range resources {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put resource %q: %v", res.ID, err)
		}
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusDeleting,
		Resources: []model.ResourceID{"res-1", "res-2"},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	errBoom := errors.New("boom")
	destroyer := &fakeDestroyer{err: errBoom, errOn: "res-2"}
	err := NewDeletionReconciler(st, destroyer).Reconcile(ctx)
	if err == nil {
		t.Fatal("expected destroy error")
	}
	if len(destroyer.destroyed) != 2 {
		t.Fatalf("destroyed = %v, want res-1 then res-2", destroyer.destroyed)
	}
	sb, getErr := st.GetSandbox(ctx, "sb-1")
	if getErr != nil {
		t.Fatalf("get sandbox: %v", getErr)
	}
	if sb.Status != model.SandboxStatusDeleting || len(sb.Resources) != 1 || sb.Resources[0] != "res-2" {
		t.Fatalf("sandbox = %+v, want only failed resource left for retry", sb)
	}

	destroyer.err = nil
	if err := NewDeletionReconciler(st, destroyer).Reconcile(ctx); err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err != store.ErrNotFound {
		t.Fatalf("sandbox err = %v, want ErrNotFound", err)
	}
	if got, want := destroyer.destroyed, []model.ResourceID{"res-1", "res-2", "res-2"}; len(got) != len(want) {
		t.Fatalf("destroyed = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("destroyed = %v, want %v", got, want)
			}
		}
	}
}

func TestDeletionReconciler_MissingResourceRecordRemovesIDAndContinues(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusDeleting,
		Resources: []model.ResourceID{"missing"},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := NewDeletionReconciler(st, &fakeDestroyer{}).Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err != store.ErrNotFound {
		t.Fatalf("sandbox err = %v, want ErrNotFound", err)
	}
}

type deletionProvisioner struct {
	n         int
	destroyed []model.ResourceID
}

func (p *deletionProvisioner) Provision(ctx context.Context, pl model.Pool) (model.Resource, error) {
	_ = ctx
	p.n++
	return model.Resource{
		ID:         model.ResourceID("new-ready"),
		Type:       pl.Inventory.ExpectedType,
		Profile:    pl.Inventory.ExpectedProfile,
		OriginPool: pl.Name,
		State:      model.ResourceStateReady,
	}, nil
}

func (p *deletionProvisioner) Destroy(ctx context.Context, pl model.Pool, res model.Resource) error {
	_ = ctx
	_ = pl
	p.destroyed = append(p.destroyed, res.ID)
	return nil
}

func TestDeletionReconciler_DestroysExpiredSandbox(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{ID: "res-1", OriginPool: "web", State: model.ResourceStateAllocated}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	past := time.Unix(1000, 0).UTC()
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusReady,
		Resources: []model.ResourceID{res.ID},
		ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	rec := NewDeletionReconciler(st, &fakeDestroyer{})
	rec.SetClock(fixedClock{t: past.Add(time.Second)})

	if err := rec.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err != store.ErrNotFound {
		t.Fatalf("sandbox err = %v, want ErrNotFound", err)
	}
}

func TestDeletionReconciler_LeavesUnexpiredSandboxAlone(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	res := model.Resource{ID: "res-1", OriginPool: "web", State: model.ResourceStateAllocated}
	if err := st.PutResource(ctx, res); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	now := time.Unix(1000, 0).UTC()
	future := now.Add(time.Hour)
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusReady,
		Resources: []model.ResourceID{res.ID},
		ExpiresAt: &future,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	destroyer := &fakeDestroyer{}
	rec := NewDeletionReconciler(st, destroyer)
	rec.SetClock(fixedClock{t: now})

	if err := rec.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(destroyer.destroyed) != 0 {
		t.Fatalf("destroyed = %v, want none", destroyer.destroyed)
	}
	sb, err := st.GetSandbox(ctx, "sb-1")
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status != model.SandboxStatusReady {
		t.Fatalf("status = %q, want ready", sb.Status)
	}
}

func TestDeletionReconciler_RemovesAllocatedResourceBeforePoolReplacesIt(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	allocated := model.Resource{
		ID:         "allocated",
		Type:       model.ResourceTypeContainer,
		Profile:    model.ResourceProfileDefault,
		OriginPool: "web",
		State:      model.ResourceStateAllocated,
	}
	if err := st.PutResource(ctx, allocated); err != nil {
		t.Fatalf("put resource: %v", err)
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "web",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: model.ResourceProfileDefault,
		},
	}); err != nil {
		t.Fatalf("put pool: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-1",
		Status:    model.SandboxStatusDeleting,
		Resources: []model.ResourceID{allocated.ID},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	prov := &deletionProvisioner{}
	poolMgr := pool.New(st, prov)
	if err := poolMgr.Reconcile(ctx, "web"); err != nil {
		t.Fatalf("pre-delete reconcile: %v", err)
	}
	if prov.n != 0 {
		t.Fatalf("provisions before cleanup = %d, want 0", prov.n)
	}

	if err := NewDeletionReconciler(st, poolMgr).Reconcile(ctx); err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if err := poolMgr.Reconcile(ctx, "web"); err != nil {
		t.Fatalf("post-delete reconcile: %v", err)
	}
	if prov.n != 1 {
		t.Fatalf("provisions after cleanup = %d, want 1", prov.n)
	}
}

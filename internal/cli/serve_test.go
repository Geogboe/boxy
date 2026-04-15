package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

type fakeServePoolReconciler struct {
	calls []model.PoolName
}

func (r *fakeServePoolReconciler) Reconcile(ctx context.Context, poolName model.PoolName) error {
	_ = ctx
	r.calls = append(r.calls, poolName)
	return nil
}

type fakeServeSandboxReconciler struct {
	calls int
}

func (r *fakeServeSandboxReconciler) Reconcile(ctx context.Context) error {
	_ = ctx
	r.calls++
	return nil
}

func TestServeReconcilePass_ReconcilesPoolsBeforeAndAfterSandboxFulfillment(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sandboxes := &fakeServeSandboxReconciler{}

	serveReconcilePass(context.Background(), pools, sandboxes, []model.PoolName{"web", "win"}, newServeUI(false))

	if sandboxes.calls != 1 {
		t.Fatalf("sandbox reconcile calls = %d, want 1", sandboxes.calls)
	}

	want := []model.PoolName{"web", "win", "web", "win"}
	if len(pools.calls) != len(want) {
		t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
	}
	for i := range want {
		if pools.calls[i] != want[i] {
			t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
		}
	}
}

func TestServeReconcilePass_RunsPostFulfillmentPoolReconcileEvenAfterSandboxError(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sandboxes := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		return fmt.Errorf("boom")
	})

	serveReconcilePass(context.Background(), pools, sandboxes, []model.PoolName{"web"}, newServeUI(false))

	want := []model.PoolName{"web", "web"}
	if len(pools.calls) != len(want) {
		t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
	}
	for i := range want {
		if pools.calls[i] != want[i] {
			t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
		}
	}
}

func TestOpenServeStore_PersistsStateAcrossReopen(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "boxy.yaml")

	first, statePath, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore(first): %v", err)
	}
	if want := filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json"); statePath != want {
		t.Fatalf("state path = %q, want %q", statePath, want)
	}

	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "persisted",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "web", Count: 1}},
	}
	if err := first.CreateSandbox(context.Background(), sb); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	second, statePath2, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore(second): %v", err)
	}
	if statePath2 != statePath {
		t.Fatalf("second state path = %q, want %q", statePath2, statePath)
	}

	got, err := second.GetSandbox(context.Background(), sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if got.ID != sb.ID || got.Status != model.SandboxStatusPending {
		t.Fatalf("sandbox = %+v, want pending sandbox %q", got, sb.ID)
	}
}

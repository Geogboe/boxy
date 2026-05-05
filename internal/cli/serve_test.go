package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
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

func TestPoolSpecToModel_invalid_pool_type(t *testing.T) {
	t.Parallel()

	_, err := poolSpecToModel(boxyconfig.PoolSpec{Name: "test", Type: "badtype"})
	if err == nil {
		t.Fatal("poolSpecToModel() error = nil, want invalid pool type")
	}
	if got, want := err.Error(), `pool "test" type invalid: unsupported pool type "badtype"`; got != want {
		t.Fatalf("poolSpecToModel() error = %q, want %q", got, want)
	}
}

// TestServeStartup_ConfigFailureShowsFailStep runs boxy serve with a config
// file that contains an unknown field. It verifies that the "Loading config"
// startup step emits its failure output synchronously (before runServe returns)
// so the user always sees which step failed rather than a bare error message.
func TestServeStartup_ConfigFailureShowsFailStep(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(cfgPath, []byte("not_a_valid_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"serve", "--config", cfgPath})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
	// The failConfig callback must have written the "Loading config" fail step
	// to stdout before runServe returned the error. Without the fail callback
	// this would be empty and the user would see only a bare error message.
	if !strings.Contains(output, "Loading config") {
		t.Fatalf("output = %q, want fail step label for config load error", output)
	}
}

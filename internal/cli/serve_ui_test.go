package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/diagnostics"
)

// TestServeUI_ReconcileError_QuarantineExhausted proves the #328 wiring:
// serveReconcilePass's periodic call to poolMgr.Reconcile can return
// *pool.BlockedPoolError (see internal/pool's own
// TestManager_ReconcileReportsBlockedWhenFailuresExhaustMaxTotal for why that
// reproduces on every tick once quarantined resources exhaust max_total),
// and reconcileError must label that with the stable "quarantine_exhausted"
// error_code rather than diagnostics.DescribeError's generic fallback —
// otherwise a wedged pool's diagnostics events look identical to any other
// transient reconcile error.
func TestServeUI_ReconcileError_QuarantineExhausted(t *testing.T) {
	logs := diagnostics.NewMemoryStore()
	previous := slog.Default()
	slog.SetDefault(slog.New(diagnostics.NewHandler(slog.NewTextHandler(io.Discard, nil), logs)))
	defer slog.SetDefault(previous)

	ui := newServeUI(false)
	blocked := &pool.BlockedPoolError{PoolName: "p1", MaxTotal: 4, ReadyCount: 0, FailedCount: 4}
	ui.reconcileError("p1", blocked)

	page, err := logs.Query(context.Background(), diagnostics.Query{Pool: "p1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %+v, want exactly one", page.Events)
	}
	event := page.Events[0]
	if event.Operation != "pool_reconcile" {
		t.Fatalf("operation = %q, want pool_reconcile", event.Operation)
	}
	if event.ErrorCode != "quarantine_exhausted" {
		t.Fatalf("error_code = %q, want quarantine_exhausted", event.ErrorCode)
	}
	if event.ErrorSummary == "" {
		t.Fatal("error_summary is empty, want the BlockedPoolError message")
	}
}

// TestServeUI_ReconcileError_FallsBackForGenericError proves the
// quarantine-specific classification does not swallow every other reconcile
// error: an error unrelated to pool.DescribeJobError's known types must keep
// going through diagnostics.DescribeError's own classification.
func TestServeUI_ReconcileError_FallsBackForGenericError(t *testing.T) {
	logs := diagnostics.NewMemoryStore()
	previous := slog.Default()
	slog.SetDefault(slog.New(diagnostics.NewHandler(slog.NewTextHandler(io.Discard, nil), logs)))
	defer slog.SetDefault(previous)

	ui := newServeUI(false)
	generic := errors.New("hyperv query available memory: exit status 1")
	ui.reconcileError("p1", generic)

	page, err := logs.Query(context.Background(), diagnostics.Query{Pool: "p1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %+v, want exactly one", page.Events)
	}
	wantCode, _ := diagnostics.DescribeError(generic)
	if page.Events[0].ErrorCode != wantCode {
		t.Fatalf("error_code = %q, want %q", page.Events[0].ErrorCode, wantCode)
	}
	if page.Events[0].ErrorCode == "quarantine_exhausted" {
		t.Fatal("generic error must not be misclassified as quarantine_exhausted")
	}
}

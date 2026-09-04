package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// TestAPI_FillPoolJobStepsReachDiagnosticsStore proves the jobs->diagnostics
// bridge: a pool job's start/success steps must reach the diagnostics store
// through the same structured slog attributes AdmissionHandler and the
// hyperv driver use, correlated by the job's own ID. boxy serve installs a
// diagnostics.Handler as the process-wide slog default (see
// internal/cli/serve.go); this test does the same for its duration to
// observe that internal/server/api_pools.go's logPoolJobStep reaches it with
// no direct dependency between the two packages.
func TestAPI_FillPoolJobStepsReachDiagnosticsStore(t *testing.T) {
	logs := diagnostics.NewMemoryStore()
	previous := slog.Default()
	slog.SetDefault(slog.New(diagnostics.NewHandler(slog.NewTextHandler(io.Discard, nil), logs)))
	defer slog.SetDefault(previous)

	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{Name: "pool-a", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 2}}}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), &poolConfigurationMaintenance{}, nil)

	fill := httptest.NewRecorder()
	mux.ServeHTTP(fill, httptest.NewRequest(http.MethodPost, "/api/v1/pools/pool-a/fill", nil))
	if fill.Code != http.StatusAccepted {
		t.Fatalf("fill status = %d, want 202; body=%s", fill.Code, fill.Body.String())
	}
	var submitted jobs.Job
	if err := json.Unmarshal(fill.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("unmarshal job: %v", err)
	}
	completed := waitForAPIJob(t, mux, submitted.ID)
	if completed.Status != jobs.StatusSucceeded {
		t.Fatalf("fill job = %+v, want succeeded", completed)
	}

	page, err := logs.Query(ctx, diagnostics.Query{Job: string(submitted.ID)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var sawStarted, sawSucceeded bool
	for _, event := range page.Events {
		if event.Component != "pool" || event.Operation != "pool.fill" || event.Pool != "pool-a" {
			t.Fatalf("event = %+v, want component=pool operation=pool.fill pool=pool-a", event)
		}
		switch event.Status {
		case "started":
			sawStarted = true
		case "succeeded":
			sawSucceeded = true
		}
	}
	if !sawStarted || !sawSucceeded {
		t.Fatalf("events for job %s = %+v, want started and succeeded steps", submitted.ID, page.Events)
	}
}

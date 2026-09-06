package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type poolConfigurationMaintenance struct{ reconcileErr error }

type blockingPoolConfigurationMaintenance struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *poolConfigurationMaintenance) Drain(context.Context, model.PoolName) (model.Pool, error) {
	return model.Pool{}, nil
}

func TestAPIUpdatePoolConfigurationRejectsWhilePoolJobActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{Name: "windows", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MaxTotal: 2}}}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.Put(ctx, jobs.Job{ID: "fill-job", Kind: "pool.fill", Target: "pool:windows", Status: jobs.StatusRunning}); err != nil {
		t.Fatalf("Put job: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false, &poolConfigurationMaintenance{})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/pools/windows/configuration", strings.NewReader(`{"min_ready":1,"max_total":3}`)))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	stored, _ := st.GetPool(ctx, "windows")
	if stored.Policies.Preheat.MaxTotal != 2 {
		t.Fatalf("active job update mutated pool: %+v", stored)
	}
}
func (m *poolConfigurationMaintenance) Fill(context.Context, model.PoolName) (model.Pool, error) {
	return model.Pool{}, nil
}
func (m *poolConfigurationMaintenance) Reconcile(context.Context, model.PoolName) error {
	return m.reconcileErr
}

func (m *blockingPoolConfigurationMaintenance) Drain(context.Context, model.PoolName) (model.Pool, error) {
	return model.Pool{}, nil
}
func (m *blockingPoolConfigurationMaintenance) Fill(context.Context, model.PoolName) (model.Pool, error) {
	return model.Pool{}, nil
}
func (m *blockingPoolConfigurationMaintenance) Reconcile(context.Context, model.PoolName) error {
	m.once.Do(func() {
		close(m.started)
		<-m.release
	})
	return nil
}

func TestAPIUpdatePoolConfigurationPersistsWhenProviderOffline(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	if err := st.PutPool(context.Background(), model.Pool{Name: "windows", Template: "server-2025", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 2}}, Configuration: model.PoolConfigurationState{Provenance: "local", LocalRevision: "revision-a"}}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false, &poolConfigurationMaintenance{reconcileErr: errors.New("provider offline")})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/pools/windows/configuration", strings.NewReader(`{"min_ready":2,"max_total":3,"max_age":"24h"}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	updated, err := st.GetPool(context.Background(), "windows")
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if updated.Template != "server-2025" || updated.Policies.Preheat.MinReady != 2 || updated.Policies.Preheat.MaxTotal != 3 || updated.Policies.Recycle.MaxAge != "24h0m0s" {
		t.Fatalf("updated pool = %+v", updated)
	}
	if updated.Configuration.Provenance != "web" || !updated.Configuration.Pending || updated.Configuration.LocalRevision != "revision-a" {
		t.Fatalf("configuration state = %+v, want pending web edit retaining local revision", updated.Configuration)
	}
}

func TestAPIUpdatePoolConfigurationRejectsInvalidWithoutMutation(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	original := model.Pool{Name: "windows", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 2}}}
	if err := st.PutPool(context.Background(), original); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false, &poolConfigurationMaintenance{})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/pools/windows/configuration", strings.NewReader(`{"min_ready":2,"max_total":0}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	stored, _ := st.GetPool(context.Background(), "windows")
	if stored.Policies != original.Policies || stored.Configuration.Provenance != "" {
		t.Fatalf("invalid update mutated pool: %+v", stored)
	}
}

func TestAPIUpdatePoolConfigurationUsesLastWriteWins(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{Name: "windows", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MaxTotal: 1}}}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	maintenance := &blockingPoolConfigurationMaintenance{started: make(chan struct{}), release: make(chan struct{})}
	mux := server.NewTestMux(st, sandbox.New(st, nil), false, maintenance)
	responses := make(chan *httptest.ResponseRecorder, 2)
	update := func(body string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/pools/windows/configuration", strings.NewReader(body)))
		responses <- w
	}
	go update(`{"min_ready":1,"max_total":2}`)
	<-maintenance.started
	go update(`{"min_ready":2,"max_total":4}`)
	close(maintenance.release)
	for range 2 {
		if w := <-responses; w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
	}
	stored, _ := st.GetPool(ctx, "windows")
	if stored.Policies.Preheat.MinReady != 2 || stored.Policies.Preheat.MaxTotal != 4 {
		t.Fatalf("pool = %+v, want second save to win", stored)
	}
}

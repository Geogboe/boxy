package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeResourceCleanup struct {
	requests []pool.CleanupRequest
	report   pool.CleanupReport
}

func (f *fakeResourceCleanup) Purge(_ context.Context, request pool.CleanupRequest) (pool.CleanupReport, error) {
	f.requests = append(f.requests, request)
	return f.report, nil
}

func TestAPI_PurgeResourcesRequiresAdminAndForceForMutation(t *testing.T) {
	st := store.NewMemoryStore()
	cleanup := &fakeResourceCleanup{report: pool.CleanupReport{DryRun: true, CandidateCount: 2}}
	mux := server.NewTestMuxWithResourceCleanup(st, sandbox.New(st, nil), cleanup)

	t.Run("defaults to dry-run", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/purge", strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if got := cleanup.requests[len(cleanup.requests)-1]; !got.DryRun || got.Force {
			t.Fatalf("request = %+v, want dry-run", got)
		}
	})

	t.Run("rejects unconfirmed mutation", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/purge", strings.NewReader(`{"dry_run":false}`))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("accepts forced mutation", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/purge", strings.NewReader(`{"dry_run":false,"force":true}`))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if got := cleanup.requests[len(cleanup.requests)-1]; got.DryRun || !got.Force {
			t.Fatalf("request = %+v, want forced mutation", got)
		}
		var report pool.CleanupReport
		if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		if report.CandidateCount != 2 {
			t.Fatalf("report = %+v", report)
		}
	})
}

func TestAPI_PurgeResourcesRejectsUnknownFieldsAndNonPost(t *testing.T) {
	st := store.NewMemoryStore()
	cleanup := &fakeResourceCleanup{}
	mux := server.NewTestMuxWithResourceCleanup(st, sandbox.New(st, nil), cleanup)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/purge", strings.NewReader(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources/purge", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404 without a purge GET route", response.Code)
	}
}

func TestAPI_PurgeResourcesUsesAdminAuthorization(t *testing.T) {
	st := store.NewMemoryStore()
	cleanup := &fakeResourceCleanup{}
	userRaw, userHash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if err := st.PutAPIKey(context.Background(), model.APIKey{ID: "user-key", Hash: userHash, Role: model.APIKeyRoleUser}); err != nil {
		t.Fatalf("PutAPIKey: %v", err)
	}
	mux := server.NewTestMuxWithResourceCleanupAuth(st, sandbox.New(st, nil), cleanup)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/purge", strings.NewReader(`{"force":true,"dry_run":false}`))
	request.Header.Set("Authorization", "Bearer "+userRaw)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("user status = %d, want 403", response.Code)
	}
}

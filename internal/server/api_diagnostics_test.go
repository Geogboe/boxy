package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type captureDiagnosticsAudit struct {
	queries []diagnostics.QueryAudit
}

func (a *captureDiagnosticsAudit) RecordDiagnosticsQuery(_ context.Context, query diagnostics.QueryAudit) error {
	a.queries = append(a.queries, query)
	return nil
}

func TestAPI_DiagnosticsLogsFiltersAndAudits(t *testing.T) {
	st := store.NewMemoryStore()
	logs := diagnostics.NewMemoryStore()
	audit := &captureDiagnosticsAudit{}
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	for _, event := range []diagnostics.Event{
		{ID: "log-1", Timestamp: now.Add(-time.Minute), Level: "WARN", Component: "reconcile", Message: "pool warning", Pool: "pool-a"},
		{ID: "log-2", Timestamp: now, Level: "ERROR", Component: "reconcile", Message: "agent failure", Pool: "pool-a", Agent: "agent-a"},
	} {
		if err := logs.Append(context.Background(), event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), logs, audit, false, false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs?level=ERROR&pool=pool-a&limit=1", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var page struct {
		Events     []diagnostics.Event `json:"events"`
		NextCursor string              `json:"next_cursor,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != "log-2" {
		t.Fatalf("page = %+v, want log-2", page)
	}
	if len(audit.queries) != 1 || audit.queries[0].ResultCount != 1 {
		t.Fatalf("audit = %+v, want one query with result count 1", audit.queries)
	}
}

func TestAPI_DiagnosticsLogsRequiresAdmin(t *testing.T) {
	st := store.NewMemoryStore()
	credentials := map[model.APIKeyRole]string{}
	for i, role := range []model.APIKeyRole{model.APIKeyRoleUser, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin} {
		raw, hash, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey[%d]: %v", i, err)
		}
		credentials[role] = raw
		if err := st.PutAPIKey(context.Background(), model.APIKey{ID: model.APIKeyID(fmt.Sprintf("diagnostics-%s", role)), Hash: hash, Role: role, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("PutAPIKey[%s]: %v", role, err)
		}
	}
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), diagnostics.NewMemoryStore(), nil, false, true)
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing status = %d, want 401", missing.Code)
	}
	for _, role := range []model.APIKeyRole{model.APIKeyRoleUser, model.APIKeyRoleAuditor} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs", nil)
		r.Header.Set("Authorization", "Bearer "+credentials[role])
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", role, w.Code)
		}
	}
	admin := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/logs", nil)
	r.Header.Set("Authorization", "Bearer "+credentials[model.APIKeyRoleAdmin])
	mux.ServeHTTP(admin, r)
	if admin.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200; body=%s", admin.Code, admin.Body.String())
	}
}

func TestUI_DiagnosticsRendersRedactedEvents(t *testing.T) {
	st := store.NewMemoryStore()
	logs := diagnostics.NewMemoryStore()
	if err := logs.Append(context.Background(), diagnostics.Event{
		ID: "log-ui", Timestamp: time.Now().UTC(), Level: "WARN", Component: "reconcile", Message: "safe warning", Pool: "pool-a",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), logs, nil, true, false)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics", nil))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !containsAll(body, "Diagnostics", "safe warning", "pool-a") {
		t.Fatalf("diagnostics page missing expected content: %s", body)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

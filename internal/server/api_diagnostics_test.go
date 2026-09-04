package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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

func TestAPI_DiagnosticsExportIsSanitizedAndBounded(t *testing.T) {
	st := store.NewMemoryStore()
	logs := diagnostics.NewMemoryStore()
	if err := logs.Append(context.Background(), diagnostics.Event{
		Timestamp: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Level:     "ERROR",
		Component: "agent",
		Message:   "host=worker.example.test password=secret ip=203.0.113.40",
		Agent:     "agent-1",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), logs, nil, false, false)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/export?limit=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); got == "" {
		t.Fatal("missing export content disposition")
	}
	var archive diagnostics.Export
	if err := json.Unmarshal(w.Body.Bytes(), &archive); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if !archive.Sanitized || len(archive.Events) != 1 {
		t.Fatalf("archive = %+v, want sanitized single event", archive)
	}
	for _, secret := range []string{"worker.example.test", "secret", "203.0.113.40", "agent-1"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("export contains unsanitized value %q: %s", secret, w.Body.String())
		}
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
	if body := w.Body.String(); !containsAll(body, "Diagnostics", "safe warning", "pool-a", "Export current query", "View agent logs", "/ui/diagnostics/export?limit=100", "component=agent") {
		t.Fatalf("diagnostics page missing expected content: %s", body)
	}
}

func TestUI_DiagnosticsFiltersAgentAndExportsCurrentQuery(t *testing.T) {
	st := store.NewMemoryStore()
	logs := diagnostics.NewMemoryStore()
	for _, event := range []diagnostics.Event{
		{ID: "agent-a", Timestamp: time.Now().UTC(), Level: "ERROR", Message: "agent a failure", Agent: "agent-a"},
		{ID: "agent-b", Timestamp: time.Now().UTC(), Level: "ERROR", Message: "agent b failure", Agent: "agent-b"},
	} {
		if err := logs.Append(context.Background(), event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), logs, nil, true, false)
	page := httptest.NewRecorder()
	mux.ServeHTTP(page, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics?agent=agent-a", nil)))
	if page.Code != http.StatusOK || !containsAll(page.Body.String(), "agent a failure", "agent-a") || strings.Contains(page.Body.String(), "agent b failure") {
		t.Fatalf("filtered page status=%d body=%s", page.Code, page.Body.String())
	}

	export := httptest.NewRecorder()
	mux.ServeHTTP(export, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics/export?agent=agent-a", nil)))
	if export.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", export.Code, export.Body.String())
	}
	var archive diagnostics.Export
	if err := json.Unmarshal(export.Body.Bytes(), &archive); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(archive.Events) != 1 || archive.Events[0].Message != "agent a failure" {
		t.Fatalf("archive events = %+v, want only agent-a event", archive.Events)
	}

	filtered := httptest.NewRecorder()
	mux.ServeHTTP(filtered, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics?component=agent", nil)))
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "View all diagnostics") || strings.Contains(filtered.Body.String(), `href="/ui/diagnostics?component=agent`) {
		t.Fatalf("agent-filtered page has non-functional navigation: status=%d body=%s", filtered.Code, filtered.Body.String())
	}
}

func TestUI_DiagnosticsPaginatesAndPreservesFilters(t *testing.T) {
	st := store.NewMemoryStore()
	logs := diagnostics.NewMemoryStore()
	now := time.Now().UTC()
	for _, event := range []diagnostics.Event{
		{ID: "newer", Timestamp: now, Level: "ERROR", Component: "agent", Message: "newer event", Agent: "agent-a"},
		{ID: "older", Timestamp: now.Add(-time.Minute), Level: "ERROR", Component: "agent", Message: "older event", Agent: "agent-a"},
	} {
		if err := logs.Append(context.Background(), event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), logs, nil, true, false)

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics?agent=agent-a&limit=1", nil)))
	if first.Code != http.StatusOK || !containsAll(first.Body.String(), "newer event", "Next page") || strings.Contains(first.Body.String(), "older event") {
		t.Fatalf("first diagnostics page status=%d body=%s", first.Code, first.Body.String())
	}

	match := regexp.MustCompile(`href="([^"]+)"[^>]*>Next page</a>`).FindStringSubmatch(first.Body.String())
	if len(match) != 2 {
		t.Fatalf("first diagnostics page missing next-page link: %s", first.Body.String())
	}
	next, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		t.Fatalf("parse next-page URL: %v", err)
	}
	if next.Query().Get("agent") != "agent-a" || next.Query().Get("limit") != "1" || next.Query().Get("cursor") == "" {
		t.Fatalf("next-page query = %v, want agent, limit, and cursor", next.Query())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, server.AuthedRequest(httptest.NewRequest(http.MethodGet, next.String(), nil)))
	if second.Code != http.StatusOK || !containsAll(second.Body.String(), "older event", "agent-a") || strings.Contains(second.Body.String(), "newer event") || strings.Contains(second.Body.String(), "Next page") {
		t.Fatalf("second diagnostics page status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestUI_DiagnosticsPullAgentLogs(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	admin := &fakeAgentAdmin{}
	mux := server.NewTestMuxWithAgentAdminUI(st, sandbox.New(st, nil), admin, true)

	get := httptest.NewRecorder()
	mux.ServeHTTP(get, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/diagnostics?agent=agent-a", nil)))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Pull agent logs") || !strings.Contains(get.Body.String(), `action="/ui/diagnostics/agents/agent-a/logs"`) {
		t.Fatalf("diagnostics page status=%d, missing pull action: %q", get.Code, get.Body.String())
	}
	csrf := csrfCookieFromResponse(t, get)

	post := httptest.NewRecorder()
	form := url.Values{"csrf_token": {csrf.Value}, "since": {"2026-09-03T18:30:00Z"}}
	r := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/diagnostics/agents/agent-a/logs", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(csrf)
	mux.ServeHTTP(post, r)
	if post.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect (body: %s)", post.Code, post.Body.String())
	}
	if !strings.Contains(post.Header().Get("Location"), "log_request=pull-1") || len(admin.logPulls) != 1 {
		t.Fatalf("location=%q log pulls=%v, want request id and one pull", post.Header().Get("Location"), admin.logPulls)
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

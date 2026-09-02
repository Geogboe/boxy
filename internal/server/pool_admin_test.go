package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type poolAdminCleanup struct {
	requests []pool.CleanupRequest
	report   pool.CleanupReport
}

func (c *poolAdminCleanup) Purge(_ context.Context, request pool.CleanupRequest) (pool.CleanupReport, error) {
	c.requests = append(c.requests, request)
	return c.report, nil
}

func csrfCookieFromResponse(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "boxy_csrf" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("response did not set a CSRF cookie")
	return nil
}

func TestUI_poolsShowsCapacityDrainStateAndResourceRows(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name: "pool-a", Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 3}},
		Drain:     model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	for _, resource := range []model.Resource{
		{ID: "ready-1", OriginPool: "pool-a", Type: model.ResourceTypeContainer, Profile: model.ResourceProfileDefault, State: model.ResourceStateReady, Provider: model.ProviderRef{Name: "docker"}},
		{ID: "allocated-1", OriginPool: "pool-a", Type: model.ResourceTypeContainer, Profile: model.ResourceProfileDefault, State: model.ResourceStateAllocated, Provider: model.ProviderRef{Name: "docker"}},
	} {
		if err := st.PutResource(ctx, resource); err != nil {
			t.Fatalf("PutResource: %v", err)
		}
	}
	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), &fakePoolMaintenance{}, &poolAdminCleanup{})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools", nil)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{"1 ready / 2 total", "Drained", "ready-1", "allocated-1", "docker", "View diagnostics", "Force cleanup"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("Pools page missing %q; body = %q", want, response.Body.String())
		}
	}
	if csrfCookieFromResponse(t, response).Value == "" {
		t.Fatal("empty CSRF cookie")
	}

	fragment := httptest.NewRecorder()
	mux.ServeHTTP(fragment, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/pools-table", nil)))
	if !strings.Contains(fragment.Body.String(), "ready-1") {
		t.Fatalf("admin pools fragment missing resource inventory: %q", fragment.Body.String())
	}
}

func TestUI_poolDetailShowsPolicyDrainResourcesProviderAndCapacity(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name: "pool-detail", Template: "windows-2025", Source: "golden-image",
		Packages:  []string{"go", "git"},
		Policies:  model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: 2, MaxTotal: 5}},
		Drain:     model.PoolDrainState{ConfigDeclared: true, Operator: true},
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeVM, ExpectedProfile: model.ResourceProfileDefault},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.PutResource(ctx, model.Resource{
		ID: "detail-resource", OriginPool: "pool-detail", Type: model.ResourceTypeVM,
		Profile: model.ResourceProfileDefault, State: model.ResourceStateReady,
		Provider: model.ProviderRef{Name: "hyperv"},
	}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), &fakePoolMaintenance{}, nil)
	index := httptest.NewRecorder()
	mux.ServeHTTP(index, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools", nil)))
	if !strings.Contains(index.Body.String(), `href="/ui/pools/pool-detail"`) {
		t.Fatal("pool index missing detail link")
	}

	detail := httptest.NewRecorder()
	mux.ServeHTTP(detail, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools/pool-detail", nil)))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}
	for _, want := range []string{
		"pool-detail", "windows-2025", "golden-image", "go", "git",
		"min_ready=2", "max_total=5", "Config drain", "Operator drain",
		"1 ready / 1 total", "detail-resource", "hyperv", "Back to pools",
	} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("pool detail missing %q: %q", want, detail.Body.String())
		}
	}
}

func TestUI_poolsHidesResourceInventoryFromNonAdmin(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	_ = st.PutPool(ctx, model.Pool{Name: "pool-a"})
	_ = st.PutResource(ctx, model.Resource{
		ID: "secret-resource-id", OriginPool: "pool-a", Type: model.ResourceTypeContainer,
		Profile: model.ResourceProfileDefault, State: model.ResourceStateReady,
		Provider: model.ProviderRef{Name: "docker"},
	})
	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), &fakePoolMaintenance{}, nil)
	request := server.OIDCAuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools", nil), st, "viewer", model.APIKeyRoleUser)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"secret-resource-id", "docker", "Operator controls", "Force cleanup"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("non-admin pools page leaked %q: %q", forbidden, body)
		}
	}
	if !strings.Contains(body, "Pool inventory is restricted to administrators.") {
		t.Fatalf("non-admin pools page missing restriction notice: %q", body)
	}

	fragment := httptest.NewRecorder()
	fragmentRequest := server.OIDCAuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/pools-table", nil), st, "viewer", model.APIKeyRoleUser)
	mux.ServeHTTP(fragment, fragmentRequest)
	if strings.Contains(fragment.Body.String(), "secret-resource-id") {
		t.Fatalf("non-admin pools fragment leaked resource inventory: %q", fragment.Body.String())
	}
}

func TestUI_poolMutationsRequireCSRFAndRedirectWithBanner(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "pool-a"})
	maintenance := &fakePoolMaintenance{drainPool: model.Pool{Name: "pool-a"}, fillPool: model.Pool{Name: "pool-a"}}
	cleanup := &poolAdminCleanup{report: pool.CleanupReport{DryRun: true, CandidateCount: 2, SkippedIDs: []pool.CleanupSkipped{{ID: "ready", Reason: "protected"}}}}
	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), maintenance, cleanup)

	get := httptest.NewRecorder()
	mux.ServeHTTP(get, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools", nil)))
	csrf := csrfCookieFromResponse(t, get)

	withoutCSRF := httptest.NewRecorder()
	missing := server.AuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/pools/pool-a/drain", strings.NewReader("")))
	mux.ServeHTTP(withoutCSRF, missing)
	if withoutCSRF.Code != http.StatusForbidden || len(maintenance.drained) != 0 {
		t.Fatalf("missing CSRF status=%d drained=%v", withoutCSRF.Code, maintenance.drained)
	}

	post := func(path string, values url.Values) *httptest.ResponseRecorder {
		values.Set("csrf_token", csrf.Value)
		request := server.AuthedRequest(httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode())))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(csrf)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	drain := post("/ui/pools/pool-a/drain", url.Values{})
	if drain.Code != http.StatusSeeOther || len(maintenance.drained) != 1 || maintenance.drained[0] != "pool-a" {
		t.Fatalf("drain status=%d calls=%v location=%q", drain.Code, maintenance.drained, drain.Header().Get("Location"))
	}
	if !strings.Contains(drain.Header().Get("Location"), "result=drain") {
		t.Fatalf("drain redirect = %q", drain.Header().Get("Location"))
	}

	preview := post("/ui/resources/purge", url.Values{"action": {"preview"}})
	if preview.Code != http.StatusSeeOther || len(cleanup.requests) != 1 || cleanup.requests[0].DryRun == false || cleanup.requests[0].Force {
		t.Fatalf("preview status=%d requests=%+v location=%q", preview.Code, cleanup.requests, preview.Header().Get("Location"))
	}

	cleanup.report = pool.CleanupReport{CandidateCount: 2, CleanedIDs: []model.ResourceID{"destroyed"}, SkippedIDs: []pool.CleanupSkipped{{ID: "ready", Reason: "protected"}}}
	force := post("/ui/resources/purge", url.Values{"action": {"force"}})
	if force.Code != http.StatusSeeOther || len(cleanup.requests) != 2 || cleanup.requests[1].DryRun || !cleanup.requests[1].Force {
		t.Fatalf("force status=%d requests=%+v location=%q", force.Code, cleanup.requests, force.Header().Get("Location"))
	}
}

func TestUI_poolMutationsRejectNonAdminSession(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "pool-a"})
	maintenance := &fakePoolMaintenance{}
	mux := server.NewTestMuxWithPoolAdmin(st, sandbox.New(st, nil), maintenance, nil)
	request := server.OIDCAuthedRequest(httptest.NewRequest(http.MethodPost, "/ui/pools/pool-a/fill", strings.NewReader("csrf_token=ignored")), st, "operator", model.APIKeyRoleUser)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

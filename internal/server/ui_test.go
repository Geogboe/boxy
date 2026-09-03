package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/humanize"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/providers/devfactory"
	"github.com/Geogboe/boxy/pkg/store"
)

// listErrStore wraps a store.Store and fails every List* call, letting tests
// exercise the UI's data-loader-error paths without a real backend outage.
type listErrStore struct {
	store.Store
	err error
}

func (s *listErrStore) ListPools(ctx context.Context) ([]model.Pool, error) {
	return nil, s.err
}

func (s *listErrStore) ListSandboxes(ctx context.Context) ([]model.Sandbox, error) {
	return nil, s.err
}

func (s *listErrStore) ListResources(ctx context.Context) ([]model.Resource, error) {
	return nil, s.err
}

func TestUI_home_renders(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Boxy") {
		t.Fatal("home page missing 'Boxy' brand")
	}
	if !strings.Contains(body, "Overview") {
		t.Fatal("home page missing 'Overview' heading")
	}
}

func TestUI_layoutRendersBrandingVersionThemeAndAdminLinks(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithDiagnostics(st, sandbox.New(st, nil), diagnostics.NewMemoryStore(), nil, true, false)

	for _, path := range []string{"/", "/ui/diagnostics", "/ui/service-keys"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, path, nil)))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, w.Code)
		}
		body := w.Body.String()
		for _, want := range []string{"/static/favicon.svg", "Repository", "dev", "data-theme-toggle", `href="/ui/diagnostics"`, `href="/ui/service-keys"`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s missing %q", path, want)
			}
		}
	}
}

func TestUI_poolsHistoryFilterSeparatesTerminalResources(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.PutPool(ctx, model.Pool{Name: "pool-a"}); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []model.Resource{
		{ID: "active-id", OriginPool: "pool-a", State: model.ResourceStateReady},
		{ID: "history-id", OriginPool: "pool-a", State: model.ResourceStateDestroyed},
	} {
		if err := st.PutResource(ctx, resource); err != nil {
			t.Fatal(err)
		}
	}
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools?view=history", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "history-id") || strings.Contains(body, "active-id") {
		t.Fatalf("history filter body = %q", body)
	}
}

func TestUI_help_rendersPackageGuidanceAndNavbarLink(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/help", nil)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Boxy package help",
		"packages: [chocolatey@1.0.0, windows-tools@1.0.0]",
		"Chocolatey.Chocolatey",
		`href="/ui/help" class="active" aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("help page missing %q, body = %q", want, body)
		}
	}
}

func TestUI_pools_renders(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "test-pool"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/pools", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test-pool") {
		t.Fatal("pools page missing pool name")
	}
}

func TestUI_sandboxes_renders(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-test", Name: "my-sandbox", Status: model.SandboxStatusReady})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "my-sandbox") {
		t.Fatal("sandboxes page missing sandbox name")
	}
	if !strings.Contains(body, `class="badge badge-ready"`) {
		t.Fatalf("sandboxes page missing status badge, body = %q", body)
	}
	// A settled terminal state must not get the "actively happening" pulse.
	if strings.Contains(body, `class="badge badge-ready badge-transient"`) {
		t.Fatalf("ready sandbox should not be marked badge-transient, body = %q", body)
	}
}

// TestUI_sandboxes_resourceDetailExpands closes #255: the sandboxes table
// previously rendered only a resource count (len .Resources), even though
// the underlying model.Sandbox.Resources is just a list of IDs the operator
// has no way to inspect from the dashboard. This asserts the expandable
// per-row detail actually surfaces resource identity/state plus the other
// sandbox-level fields (owner, expiry, security profile) the table also
// didn't show before.
func TestUI_sandboxes_resourceDetailExpands(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	expires := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	if err := st.PutResource(ctx, model.Resource{
		ID:         "res-1",
		Type:       model.ResourceTypeContainer,
		Profile:    "ubuntu-2204",
		OriginPool: "pool-a",
		Provider:   model.ProviderRef{Name: "docker"},
		State:      model.ResourceStateReady,
	}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-detail",
		Name:      "detailed-sandbox",
		Status:    model.SandboxStatusReady,
		OwnerID:   "owner-1",
		Policies:  model.SandboxPolicies{SecurityProfile: "lab"},
		Resources: []model.ResourceID{"res-1"},
		ExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<details", "res-1", "docker", "ubuntu-2204", "pool-a",
		"owner-1", "lab", "2026-08-28 12:00:00 UTC",
		`class="badge badge-ready"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sandboxes page missing %q; body = %q", want, body)
		}
	}
}

// TestUI_sandboxes_resourceDetailHandlesMissingResourceRecord guards a real
// edge case: Sandbox.Resources is a list of IDs, so it can reference a
// resource the store no longer holds a record for (e.g. purged after
// destroy). The detail view must still render that ID rather than panicking
// on a missing map lookup or silently dropping the row.
func TestUI_sandboxes_resourceDetailHandlesMissingResourceRecord(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()

	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:        "sb-missing-res",
		Name:      "orphan-ref-sandbox",
		Status:    model.SandboxStatusReady,
		Resources: []model.ResourceID{"res-gone"},
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "res-gone") {
		t.Fatalf("sandboxes page missing orphaned resource id, body = %q", body)
	}
	if !strings.Contains(body, "badge-unknown") || !strings.Contains(body, "unknown · unknown · pool unknown · unknown") {
		t.Fatalf("sandboxes page did not render \"unknown\" placeholders for an orphaned resource, body = %q", body)
	}
}

func TestUI_sandboxes_pendingStatusGetsTransientBadge(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-pending", Name: "still-provisioning", Status: model.SandboxStatusPending})
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-deleting", Name: "tearing-down", Status: model.SandboxStatusDeleting})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil))
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `class="badge badge-pending badge-transient"`) {
		t.Fatalf("pending sandbox missing badge-transient, body = %q", body)
	}
	if !strings.Contains(body, `class="badge badge-deleting badge-transient"`) {
		t.Fatalf("deleting sandbox missing badge-transient, body = %q", body)
	}
}

func TestUI_refreshButtons_targetTheirFragment(t *testing.T) {
	t.Parallel()
	admin := &fakeAgentAdmin{}
	mux := server.NewTestMuxWithAgentAdminUI(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), admin, true)

	cases := []struct {
		path        string
		fragmentID  string
		fragmentGet string
	}{
		{"/", "stats-fragment", "/ui/fragments/stats"},
		{"/ui/pools", "pools-fragment", "/ui/fragments/pools-table"},
		{"/ui/sandboxes", "sandboxes-fragment", "/ui/fragments/sandboxes-table"},
		{"/ui/agents", "agents-fragment", "/ui/fragments/agents-table"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, tc.path, nil)))
		body := w.Body.String()
		if !strings.Contains(body, `id="`+tc.fragmentID+`"`) {
			t.Errorf("%s: missing fragment container id %q, body = %q", tc.path, tc.fragmentID, body)
		}
		wantBtn := `class="refresh-btn" hx-get="` + tc.fragmentGet + `" hx-target="#` + tc.fragmentID + `" hx-swap="innerHTML" hx-sync="#` + tc.fragmentID + `:replace"`
		if !strings.Contains(body, wantBtn) {
			t.Errorf("%s: missing refresh button wired to its fragment, want substring %q, body = %q", tc.path, wantBtn, body)
		}
		// Must be "replace", not the htmx default "drop": a manual click
		// racing the 5s poll must preempt it, not be silently swallowed.
		if strings.Contains(body, `hx-sync="#`+tc.fragmentID+`:drop"`) {
			t.Errorf("%s: refresh button still uses hx-sync:drop, which silently discards a click that races the poll", tc.path)
		}
		// The 5s poll must still be present and unchanged alongside the button.
		if !strings.Contains(body, `hx-trigger="every 5s"`) {
			t.Errorf("%s: missing unchanged 5s poll trigger, body = %q", tc.path, body)
		}
	}
}

func TestUI_fragment_stats(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	_ = st.PutPool(context.Background(), model.Pool{Name: "p1"})

	mux := server.NewTestMux(st, sandbox.New(st, nil), true)
	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/stats", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "stat-card") {
		t.Fatal("stats fragment missing stat-card")
	}
}

func TestUI_fragment_pools_table(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/pools-table", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No pools configured") {
		t.Fatal("empty pools fragment missing empty state")
	}
}

func TestUI_agents_rendersStatusesCapacityAndPolling(t *testing.T) {
	t.Parallel()
	lastSeen := time.Date(2026, time.August, 21, 14, 30, 0, 0, time.UTC)
	availabilityAt := lastSeen.Add(2 * time.Second)
	admin := &fakeAgentAdmin{agents: []pool.AgentSummary{
		{ID: "embedded", Name: "Embedded Agent", Providers: []providersdk.Type{"docker"}, Connected: true, Available: true},
		{ID: "remote-1", Name: "Lab Hypervisor", Providers: []providersdk.Type{"hyperv", "docker"}, Connected: false, Available: false,
			LastSeen: &lastSeen, Availability: map[providersdk.Type]providersdk.ResourceAvailability{"hyperv": {MemoryMB: 4096}}, AvailabilityAt: &availabilityAt},
	}}
	mux := server.NewTestMuxWithAgentAdminUI(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), admin, true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/agents", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Agents", "Embedded Agent", "Lab Hypervisor", "remote-1", "Connected", "Disconnected",
		"Available", "Unavailable", "hyperv", "4,096 MB free", "No heartbeat sample", "No capacity sample",
		"2026-08-21 14:30:00 UTC", `hx-get="/ui/fragments/agents-table"`, `hx-trigger="every 5s"`,
		`href="/ui/agents" class="active" aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("agents page missing %q; body = %q", want, body)
		}
	}
}

// TestUI_agents_devfactoryAvailabilityRendersSanely closes the loop on
// #181's Availability() sentinel fix: it feeds the dashboard the *actual*
// values devfactory.Driver.Availability reports (not hand-picked numbers)
// and asserts the rendered page reflects them sanely. Before that fix, an
// unconfigured devfactory pool's Availability reported math.MaxInt64, which
// humanize.CommaInt rendered as "9,223,372,036,854,775,807 MB free" — this
// guards against that regression without needing a real mTLS remote agent
// (the dashboard's Availability data is agent-transport-agnostic; see
// TestUI_agents_rendersStatusesCapacityAndPolling for the same pattern with
// hand-picked values).
func TestUI_agents_devfactoryAvailabilityRendersSanely(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	unconfigured := devfactory.New(&devfactory.Config{DataDir: t.TempDir()})
	unlimitedAvail, err := unconfigured.Availability(ctx)
	if err != nil {
		t.Fatalf("Availability (unconfigured): %v", err)
	}

	zeroed := devfactory.New(&devfactory.Config{AvailableMemoryZero: true, DataDir: t.TempDir()})
	zeroAvail, err := zeroed.Availability(ctx)
	if err != nil {
		t.Fatalf("Availability (AvailableMemoryZero): %v", err)
	}
	if zeroAvail.MemoryMB != 0 {
		t.Fatalf("AvailableMemoryZero: MemoryMB = %d, want 0", zeroAvail.MemoryMB)
	}

	lastSeen := time.Date(2026, time.August, 21, 14, 30, 0, 0, time.UTC)
	admin := &fakeAgentAdmin{agents: []pool.AgentSummary{
		{ID: "devfactory-unconfigured", Name: "Devfactory (unconfigured)", Providers: []providersdk.Type{devfactory.ProviderType},
			Connected: true, Available: true, LastSeen: &lastSeen,
			Availability:   map[providersdk.Type]providersdk.ResourceAvailability{devfactory.ProviderType: *unlimitedAvail},
			AvailabilityAt: &lastSeen},
		{ID: "devfactory-zeroed", Name: "Devfactory (zeroed)", Providers: []providersdk.Type{devfactory.ProviderType},
			Connected: true, Available: false, LastSeen: &lastSeen,
			Availability:   map[providersdk.Type]providersdk.ResourceAvailability{devfactory.ProviderType: *zeroAvail},
			AvailabilityAt: &lastSeen},
	}}
	mux := server.NewTestMuxWithAgentAdminUI(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), admin, true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/agents", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "0 MB free") {
		t.Fatalf("expected \"0 MB free\" for AvailableMemoryZero; body = %q", body)
	}
	if strings.Contains(body, "9,223,372,036,854,775,807") {
		t.Fatal("dashboard rendered the old math.MaxInt64 sentinel — Availability() regressed")
	}
	wantUnlimited := humanize.CommaInt(devfactory.UnlimitedMemoryMB) + " MB free"
	if !strings.Contains(body, wantUnlimited) {
		t.Fatalf("expected the finite unlimited sentinel rendered with comma grouping (%q); body = %q", wantUnlimited, body)
	}
}

func TestUI_agents_emptyInventory(t *testing.T) {
	t.Parallel()
	admin := &fakeAgentAdmin{}
	mux := server.NewTestMuxWithAgentAdminUI(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), admin, true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/agents-table", nil)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No agents registered") {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
}

func TestUI_agents_withoutTransportShowsError(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/agents-table", nil)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
}

func TestUI_fragment_dataError_returns200WithBanner(t *testing.T) {
	t.Parallel()
	failing := &listErrStore{Store: store.NewMemoryStore(), err: errors.New("store unavailable")}
	mux := server.NewTestMux(failing, sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/fragments/sandboxes-table", nil))
	mux.ServeHTTP(w, r)

	// HTMX only swaps 2xx responses by default; a non-2xx status here means
	// a failing 5s poll goes silently unnoticed with no visible indication
	// anything is wrong.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (HTMX won't swap a non-2xx fragment response)", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("body = %q, want an error banner", w.Body.String())
	}
}

func TestUI_page_dataError_returns500Branded(t *testing.T) {
	t.Parallel()
	failing := &listErrStore{Store: store.NewMemoryStore(), err: errors.New("store unavailable")}
	mux := server.NewTestMux(failing, sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Boxy") {
		t.Fatalf("body = %q, want branded HTML (layout), not a plain-text error", body)
	}
	if !strings.Contains(body, "error") {
		t.Fatalf("body = %q, want it to mention an error", body)
	}
}

func TestUI_disabled_returns_404(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	mux.ServeHTTP(w, r)

	// When UI is disabled, / should not match any route (404).
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (UI disabled)", w.Code, http.StatusNotFound)
	}
}

func TestUI_static_css(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "--bg:") {
		t.Fatal("CSS missing expected variable")
	}
}

func TestUI_static_htmx(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.Len() < 1000 {
		t.Fatalf("htmx.min.js too small: %d bytes", w.Body.Len())
	}
}

func TestUI_favicon_isEmbeddedAndLinked(t *testing.T) {
	t.Parallel()
	mux := server.NewTestMux(store.NewMemoryStore(), sandbox.New(store.NewMemoryStore(), nil), true)

	static := httptest.NewRecorder()
	mux.ServeHTTP(static, httptest.NewRequest(http.MethodGet, "/static/favicon.svg", nil))
	if static.Code != http.StatusOK {
		t.Fatalf("favicon status = %d, want %d", static.Code, http.StatusOK)
	}
	if got := static.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Fatalf("favicon Content-Type = %q, want image/svg+xml", got)
	}
	if body := static.Body.String(); !strings.Contains(body, "<svg") || !strings.Contains(body, "#6c8cff") {
		t.Fatalf("favicon body = %q, want an SVG using the Boxy accent", body)
	}

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/", nil)))
	if !strings.Contains(page.Body.String(), `<link rel="icon" type="image/svg+xml" href="/static/favicon.svg">`) {
		t.Fatalf("home page missing favicon link; body = %q", page.Body.String())
	}
}

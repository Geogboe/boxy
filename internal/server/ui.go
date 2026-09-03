package server

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/internal/buildcfg"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/humanize"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pageData is the top-level data passed to every template.
type pageData struct {
	Nav string
	// User is the logged-in session's subject (e.g. "admin" for the local
	// admin account), shown in the sidebar with a logout link. Set by
	// uiHandler (full-page routes) from the request's session; left empty
	// by fragmentHandler (HTMX polling routes), whose fragment templates
	// never reference .User — the sidebar itself is only ever rendered by
	// a full-page response.
	User                 string
	Version              string
	RepositoryURL        string
	PoolCount            int
	SandboxCount         int
	ResourceCount        int
	Pools                []model.Pool
	PoolViews            []poolView
	PoolDetail           *poolView
	PoolHistory          bool
	ResourceLimitHit     bool
	CSRFToken            string
	CanManagePools       bool
	PoolResult           string
	PoolError            string
	Sandboxes            []sandboxView
	Resources            []model.Resource
	Agents               []agentView
	Profile              profileData
	Catalog              catalogPageData
	CanManageServiceKeys bool
	CanViewDiagnostics   bool
	ServiceKeys          []apiKeySummary
	ServiceKeyError      string
	MintedServiceKey     string
	MintedServiceKeyName string
	Diagnostics          []diagnostics.Event
	DiagnosticsError     string
	DiagnosticsQuery     diagnostics.Query
	DiagnosticsSince     string
	DiagnosticsExportURL string
	DiagnosticsAgentURL  string
}

// sandboxView is the dashboard's per-sandbox row, joining the sandbox record
// with the full resource details (model.Sandbox.Resources is only a list of
// IDs) so the table can expand a row to real detail instead of a bare count.
// See #255.
type sandboxView struct {
	ID               string
	Name             string
	Status           model.SandboxStatus
	OwnerID          string
	Error            string
	SecurityProfile  string
	AutoDestroyAfter string
	ExpiresAt        string
	Resources        []resourceView
}

// resourceView is one resource's detail within an expanded sandbox row.
type resourceView struct {
	ID         string
	Type       string
	Profile    string
	State      model.ResourceState
	OriginPool string
	Provider   string
}

type poolView struct {
	Name                string
	DetailPath          string
	Type                model.ResourceType
	ExpectedProfile     model.ResourceProfile
	Template            string
	Source              string
	Packages            []string
	MinReady            int
	MaxTotal            int
	ReadyCount          int
	TotalCount          int
	EffectivelyDrained  bool
	ConfigDrain         bool
	OperatorDrain       bool
	ProviderNames       []string
	Resources           []poolResourceView
	HistoricalResources []poolResourceView
	HistoricalCount     int
}

type poolResourceView struct {
	ID       string
	Type     model.ResourceType
	Profile  model.ResourceProfile
	State    model.ResourceState
	Provider string
}

type agentView struct {
	ID        string
	Name      string
	Connected bool
	Available bool
	LastSeen  string
	Providers []providerView
}

type providerView struct {
	Name        string
	Capacity    string
	HasCapacity bool
	SampleAt    string
	HasSample   bool
}

// pageTemplate parses the layout together with a single page template so that
// each page's {{define "content"}} block overrides the layout's {{block "content"}}.
func pageTemplate(page string) *template.Template {
	return template.Must(template.ParseFS(templateFS,
		"templates/layout.html",
		"templates/"+page,
	))
}

// registerUIRoutes wires the web dashboard routes into the mux.
// staticHandler serves CSS/JS assets. Registered unauthenticated (outside
// requireSession) so the login page itself can load /static/style.css.
func (s *Server) staticHandler() http.Handler {
	staticContent, _ := fs.Sub(staticFS, "static")
	return http.StripPrefix("/static/", http.FileServer(http.FS(staticContent)))
}

func (s *Server) registerUIRoutes(mux *http.ServeMux) {
	homeTmpl := pageTemplate("index.html")
	poolsTmpl := pageTemplate("pools.html")
	sandboxesTmpl := pageTemplate("sandboxes.html")
	agentsTmpl := pageTemplate("agents.html")
	profileTmpl := pageTemplate("profile.html")
	catalogTmpl := pageTemplate("catalog.html")
	serviceKeysTmpl := pageTemplate("service_keys.html")
	diagnosticsTmpl := pageTemplate("diagnostics.html")
	helpTmpl := pageTemplate("help.html")

	// Full-page routes.
	mux.HandleFunc("GET /{$}", s.uiHandler(homeTmpl, "home", s.homeData))
	mux.HandleFunc("GET /ui/pools", s.uiHandler(poolsTmpl, "pools", s.poolsData))
	mux.HandleFunc("GET /ui/pools/{name}", s.uiHandler(poolsTmpl, "pools", s.poolsData))
	mux.HandleFunc("GET /ui/sandboxes", s.uiHandler(sandboxesTmpl, "sandboxes", s.sandboxesData))
	mux.HandleFunc("GET /ui/agents", s.uiHandler(agentsTmpl, "agents", s.agentsData))
	mux.HandleFunc("GET /ui/profile", s.uiHandler(profileTmpl, "profile", s.profileData))
	mux.HandleFunc("GET /ui/service-keys", s.serviceKeysHandler(serviceKeysTmpl))
	mux.HandleFunc("POST /ui/service-keys", s.handleCreateServiceKey(serviceKeysTmpl))
	mux.HandleFunc("POST /ui/service-keys/{id}/revoke", s.handleRevokeServiceKey)
	mux.HandleFunc("POST /ui/pools/{name}/drain", s.handleDrainPoolUI)
	mux.HandleFunc("POST /ui/pools/{name}/fill", s.handleFillPoolUI)
	mux.HandleFunc("POST /ui/resources/purge", s.handlePurgeResourcesUI)
	mux.HandleFunc("GET /ui/catalog", s.uiHandler(catalogTmpl, "catalog", s.catalogData))
	mux.HandleFunc("GET /ui/help", s.uiHandler(helpTmpl, "help", func(*http.Request) (pageData, error) { return pageData{}, nil }))
	mux.HandleFunc("GET /ui/diagnostics", s.diagnosticsHandler(diagnosticsTmpl))
	mux.HandleFunc("GET /ui/diagnostics/export", s.diagnosticsExportHandler)
	mux.HandleFunc("POST /ui/profile/personal-key", s.handleMintPersonalKey(profileTmpl))

	// HTMX fragment routes.
	mux.HandleFunc("GET /ui/fragments/stats", s.fragmentHandler(homeTmpl, "stats_fragment", s.homeData))
	mux.HandleFunc("GET /ui/fragments/pools-table", s.fragmentHandler(poolsTmpl, "pools_table_fragment", s.poolsData))
	mux.HandleFunc("GET /ui/fragments/sandboxes-table", s.fragmentHandler(sandboxesTmpl, "sandboxes_table_fragment", s.sandboxesData))
	mux.HandleFunc("GET /ui/fragments/agents-table", s.fragmentHandler(agentsTmpl, "agents_table_fragment", s.agentsData))
}

func (s *Server) catalogData(r *http.Request) (pageData, error) {
	if s.catalog == nil {
		return pageData{Catalog: catalogPage(CatalogSnapshot{})}, nil
	}
	snapshot, err := s.catalog.LoadCatalog(r.Context())
	if err != nil {
		// Do not expose the source error: a config-backed source may contain
		// provider paths or other operator-controlled values.
		slog.Error("catalog load failed")
		return pageData{Catalog: catalogPageData{LoadError: "Catalog is temporarily unavailable. Please retry shortly."}}, nil
	}
	return pageData{Catalog: catalogPage(snapshot)}, nil
}

// dataFn loads data from the store into a pageData.
type dataFn func(r *http.Request) (pageData, error)

var errPoolNotFound = errors.New("pool not found")

// uiHandler returns a handler that renders a full page (layout + content).
func (s *Server) uiHandler(tmpl *template.Template, nav string, data dataFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := data(r)
		if err != nil {
			if errors.Is(err, errPoolNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("ui data", "err", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			if err := tmpl.ExecuteTemplate(w, "error_page", nil); err != nil {
				slog.Error("ui error page render", "err", err)
			}
			return
		}
		d = s.decoratePageData(w, r, d, nav)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "layout.html", d); err != nil {
			slog.Error("ui render", "err", err)
		}
	}
}

func (s *Server) decoratePageData(w http.ResponseWriter, r *http.Request, d pageData, nav string) pageData {
	d.Nav = nav
	d.Version = s.version
	d.RepositoryURL = s.repositoryURL
	if d.Version == "" {
		d.Version = "dev"
	}
	if d.RepositoryURL == "" {
		d.RepositoryURL = "https://github.com/" + buildcfg.Repo
	}
	if principal, ok := sessionPrincipalFromRequest(r); ok {
		d.User = principal.Subject
		isAdmin := principal.Role == model.APIKeyRoleAdmin
		d.CanManageServiceKeys = isAdmin
		d.CanViewDiagnostics = isAdmin
		d.CanManagePools = isAdmin
		d.CSRFToken = ensureCSRFCookie(w, r, s.insecureHTTP)
	}
	return d
}

func (s *Server) diagnosticsHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sessionPrincipalFromRequest(r)
		if !ok {
			redirectToLogin(w, r)
			return
		}
		if principal.Role != model.APIKeyRoleAdmin {
			http.Error(w, "diagnostics requires an administrator", http.StatusForbidden)
			return
		}
		d := s.decoratePageData(w, r, pageData{}, "diagnostics")
		query, audit, err := parseDiagnosticsQuery(r)
		if err != nil {
			d.DiagnosticsError = err.Error()
		} else {
			audit.Actor = principal.Subject
			d.DiagnosticsQuery = query
			d.DiagnosticsSince = r.URL.Query().Get("since")
			d.DiagnosticsExportURL = "/ui/diagnostics/export?" + diagnosticsQueryValues(query).Encode()
			agentQuery := query
			if agentQuery.Agent == "" {
				agentQuery.Component = "agent"
			}
			d.DiagnosticsAgentURL = "/ui/diagnostics?" + diagnosticsQueryValues(agentQuery).Encode()
		}
		if err == nil {
			if s.diagnostics == nil {
				d.DiagnosticsError = "Diagnostics are temporarily unavailable."
			} else {
				page, err := s.diagnostics.Query(r.Context(), query)
				if err != nil {
					slog.Error("diagnostics query failed")
					d.DiagnosticsError = "Diagnostics are temporarily unavailable."
				} else {
					d.Diagnostics = page.Events
					audit.ResultCount = len(page.Events)
					if s.audit != nil {
						if err := s.audit.RecordDiagnosticsQuery(r.Context(), audit); err != nil {
							slog.Error("diagnostics audit failed")
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "layout.html", d); err != nil {
			slog.Error("diagnostics render", "err", err)
		}
	}
}

func (s *Server) diagnosticsExportHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := sessionPrincipalFromRequest(r)
	if !ok {
		redirectToLogin(w, r)
		return
	}
	if principal.Role != model.APIKeyRoleAdmin {
		http.Error(w, "diagnostics requires an administrator", http.StatusForbidden)
		return
	}
	if s.diagnostics == nil {
		http.Error(w, "diagnostics are temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	query, audit, err := parseDiagnosticsQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	archive, resultCount, err := s.buildDiagnosticsExport(r.Context(), query)
	if err != nil {
		http.Error(w, "failed to build diagnostics export", http.StatusInternalServerError)
		return
	}
	audit.Actor = principal.Subject
	audit.ResultCount = resultCount
	if s.audit != nil {
		if err := s.audit.RecordDiagnosticsQuery(r.Context(), audit); err != nil {
			http.Error(w, "failed to record diagnostics query", http.StatusInternalServerError)
			return
		}
	}
	var body bytes.Buffer
	if err := diagnostics.WriteExport(&body, archive); err != nil {
		http.Error(w, "failed to encode diagnostics export", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="boxy-diagnostics.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func (s *Server) serviceKeysHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sessionPrincipalFromRequest(r)
		if !ok {
			redirectToLogin(w, r)
			return
		}
		if principal.Role != model.APIKeyRoleAdmin {
			http.Error(w, "service-key management requires an administrator", http.StatusForbidden)
			return
		}
		keys, err := s.serviceAPIKeySummaries(r.Context())
		if err != nil {
			slog.Error("service key list failed")
			http.Error(w, "service-key management is temporarily unavailable", http.StatusInternalServerError)
			return
		}
		d := s.decoratePageData(w, r, pageData{}, "service-keys")
		d.ServiceKeys = keys
		s.renderServiceKeys(w, tmpl, d)
	}
}

func (s *Server) handleCreateServiceKey(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sessionPrincipalFromRequest(r)
		if !ok {
			redirectToLogin(w, r)
			return
		}
		if principal.Role != model.APIKeyRoleAdmin {
			http.Error(w, "service-key management requires an administrator", http.StatusForbidden)
			return
		}
		d := s.decoratePageData(w, r, pageData{}, "service-keys")
		if err := r.ParseForm(); err != nil {
			d.ServiceKeyError = "Invalid service-key request."
		} else {
			response, err := s.createAPIKey(r, createAPIKeyRequest{
				Name: r.FormValue("name"), Role: model.APIKeyRole(r.FormValue("role")), Expires: r.FormValue("expires"),
			})
			if err != nil {
				d.ServiceKeyError = err.Error()
			} else {
				d.MintedServiceKey = response.Key
				d.MintedServiceKeyName = response.Name
			}
		}
		d.ServiceKeys, _ = s.serviceAPIKeySummaries(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		s.renderServiceKeys(w, tmpl, d)
	}
}

func (s *Server) handleRevokeServiceKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := sessionPrincipalFromRequest(r)
	if !ok {
		redirectToLogin(w, r)
		return
	}
	if principal.Role != model.APIKeyRoleAdmin {
		http.Error(w, "service-key management requires an administrator", http.StatusForbidden)
		return
	}
	id := model.APIKeyID(r.PathValue("id"))
	key, err := s.store.GetAPIKey(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to find service key", http.StatusInternalServerError)
		return
	}
	if key.EffectiveKind() != model.APIKeyKindService {
		http.NotFound(w, r)
		return
	}
	if err := s.revokeAPIKey(r.Context(), id); err != nil {
		slog.Error("service key revoke failed")
		http.Error(w, "failed to revoke service key", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/service-keys", http.StatusSeeOther)
}

func (s *Server) renderServiceKeys(w http.ResponseWriter, tmpl *template.Template, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", d); err != nil {
		slog.Error("service key page render")
	}
}

// fragmentHandler returns a handler that renders only a named template fragment.
// Used for HTMX polling updates.
func (s *Server) fragmentHandler(tmpl *template.Template, fragment string, data dataFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := data(r)
		if err != nil {
			slog.Error("ui fragment data", "err", err)
			// HTMX only swaps 2xx responses by default, so returning an
			// error status here (as this used to) means a failing 5s poll
			// does nothing visible — the page just goes stale silently.
			// Returning 200 with a banner keeps the failure visible.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := tmpl.ExecuteTemplate(w, "error_banner", nil); err != nil {
				slog.Error("ui fragment error banner render", "err", err)
			}
			return
		}
		if principal, ok := sessionPrincipalFromRequest(r); ok {
			isAdmin := principal.Role == model.APIKeyRoleAdmin
			d.CanManagePools = isAdmin
			d.CanViewDiagnostics = isAdmin
			d.CanManageServiceKeys = isAdmin
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, fragment, d); err != nil {
			slog.Error("ui fragment render", "err", err)
		}
	}
}

// Data loaders — each queries the store and builds a pageData.

func (s *Server) homeData(r *http.Request) (pageData, error) {
	ctx := r.Context()
	pools, err := s.store.ListPools(ctx)
	if err != nil {
		return pageData{}, err
	}
	sandboxes, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return pageData{}, err
	}
	resources, err := s.store.ListResources(ctx)
	if err != nil {
		return pageData{}, err
	}
	return pageData{
		PoolCount:     len(pools),
		SandboxCount:  len(sandboxes),
		ResourceCount: len(resources),
	}, nil
}

func (s *Server) poolsData(r *http.Request) (pageData, error) {
	pools, err := s.store.ListPools(r.Context())
	if err != nil {
		return pageData{}, err
	}
	resources, err := s.store.ListResources(r.Context())
	if err != nil {
		return pageData{}, err
	}
	resourceLimitHit := len(resources) > 1000
	if resourceLimitHit {
		resources = resources[:1000]
	}
	views := buildPoolViews(pools, resources)
	data := pageData{
		Pools:            pools,
		PoolViews:        views,
		PoolResult:       poolResultFromQuery(r),
		PoolHistory:      r.URL.Query().Get("view") == "history",
		ResourceLimitHit: resourceLimitHit,
	}
	if name := r.PathValue("name"); name != "" {
		for i := range views {
			if views[i].Name == name {
				data.PoolDetail = &views[i]
				return data, nil
			}
		}
		return pageData{}, fmt.Errorf("%w: %q", errPoolNotFound, name)
	}
	return data, nil
}

func (s *Server) sandboxesData(r *http.Request) (pageData, error) {
	ctx := r.Context()
	sandboxes, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return pageData{}, err
	}
	resources, err := s.store.ListResources(ctx)
	if err != nil {
		return pageData{}, err
	}
	byID := make(map[model.ResourceID]model.Resource, len(resources))
	for _, res := range resources {
		byID[res.ID] = res
	}

	views := make([]sandboxView, 0, len(sandboxes))
	for _, sb := range sandboxes {
		view := sandboxView{
			ID:               string(sb.ID),
			Name:             sb.Name,
			Status:           sb.Status,
			OwnerID:          sb.OwnerID,
			Error:            sb.Error,
			SecurityProfile:  sb.Policies.SecurityProfile,
			AutoDestroyAfter: sb.Policies.AutoDestroyAfter,
			Resources:        make([]resourceView, 0, len(sb.Resources)),
		}
		if sb.ExpiresAt != nil {
			view.ExpiresAt = dashboardTime(*sb.ExpiresAt)
		}
		for _, id := range sb.Resources {
			res, ok := byID[id]
			if !ok {
				// The store no longer holds a record for this ID (e.g.
				// destroyed and purged). Still show the ID rather than
				// silently dropping the row, so the resource count in the
				// summary and the expanded detail agree -- with explicit
				// "unknown" placeholders rather than zero-valued fields,
				// which would otherwise render as a blank, broken-looking
				// badge and an empty " · · pool  · " meta line.
				view.Resources = append(view.Resources, resourceView{
					ID:         string(id),
					Type:       "unknown",
					Profile:    "unknown",
					State:      model.ResourceStateUnknown,
					OriginPool: "unknown",
					Provider:   "unknown",
				})
				continue
			}
			view.Resources = append(view.Resources, resourceView{
				ID:         string(res.ID),
				Type:       string(res.Type),
				Profile:    string(res.Profile),
				State:      res.State,
				OriginPool: string(res.OriginPool),
				Provider:   res.Provider.Name,
			})
		}
		views = append(views, view)
	}
	return pageData{Sandboxes: views}, nil
}

func (s *Server) agentsData(_ *http.Request) (pageData, error) {
	if s.agentAdmin == nil {
		return pageData{}, errors.New("agent transport not available")
	}
	summaries := s.agentAdmin.ListAgents()
	view := make([]agentView, 0, len(summaries))
	for _, summary := range summaries {
		agent := agentView{
			ID:        summary.ID,
			Name:      summary.Name,
			Connected: summary.Connected,
			Available: summary.Available,
			LastSeen:  "No heartbeat sample",
			Providers: make([]providerView, 0, len(summary.Providers)),
		}
		if summary.LastSeen != nil {
			agent.LastSeen = dashboardTime(*summary.LastSeen)
		}
		for _, provider := range summary.Providers {
			availability, hasCapacity := summary.Availability[provider]
			providerRow := providerView{
				Name:        string(provider),
				HasCapacity: hasCapacity,
			}
			if hasCapacity {
				providerRow.Capacity = humanize.CommaInt(availability.MemoryMB) + " MB free"
			}
			if summary.AvailabilityAt != nil {
				providerRow.HasSample = true
				providerRow.SampleAt = dashboardTime(*summary.AvailabilityAt)
			}
			agent.Providers = append(agent.Providers, providerRow)
		}
		view = append(view, agent)
	}
	return pageData{Agents: view}, nil
}

func dashboardTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

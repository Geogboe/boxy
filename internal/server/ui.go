package server

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/pkg/humanize"
	"github.com/Geogboe/boxy/pkg/model"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pageData is the top-level data passed to every template.
type pageData struct {
	Nav           string
	PoolCount     int
	SandboxCount  int
	ResourceCount int
	Pools         []model.Pool
	Sandboxes     []model.Sandbox
	Resources     []model.Resource
	Agents        []agentView
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
func (s *Server) registerUIRoutes(mux *http.ServeMux) {
	homeTmpl := pageTemplate("index.html")
	poolsTmpl := pageTemplate("pools.html")
	sandboxesTmpl := pageTemplate("sandboxes.html")
	agentsTmpl := pageTemplate("agents.html")

	// Static assets (CSS, JS).
	staticContent, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	// Full-page routes.
	mux.HandleFunc("GET /{$}", s.uiHandler(homeTmpl, "home", s.homeData))
	mux.HandleFunc("GET /ui/pools", s.uiHandler(poolsTmpl, "pools", s.poolsData))
	mux.HandleFunc("GET /ui/sandboxes", s.uiHandler(sandboxesTmpl, "sandboxes", s.sandboxesData))
	mux.HandleFunc("GET /ui/agents", s.uiHandler(agentsTmpl, "agents", s.agentsData))

	// HTMX fragment routes.
	mux.HandleFunc("GET /ui/fragments/stats", s.fragmentHandler(homeTmpl, "stats_fragment", s.homeData))
	mux.HandleFunc("GET /ui/fragments/pools-table", s.fragmentHandler(poolsTmpl, "pools_table_fragment", s.poolsData))
	mux.HandleFunc("GET /ui/fragments/sandboxes-table", s.fragmentHandler(sandboxesTmpl, "sandboxes_table_fragment", s.sandboxesData))
	mux.HandleFunc("GET /ui/fragments/agents-table", s.fragmentHandler(agentsTmpl, "agents_table_fragment", s.agentsData))
}

// dataFn loads data from the store into a pageData.
type dataFn func(r *http.Request) (pageData, error)

// uiHandler returns a handler that renders a full page (layout + content).
func (s *Server) uiHandler(tmpl *template.Template, nav string, data dataFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := data(r)
		if err != nil {
			slog.Error("ui data", "err", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			if err := tmpl.ExecuteTemplate(w, "error_page", nil); err != nil {
				slog.Error("ui error page render", "err", err)
			}
			return
		}
		d.Nav = nav

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "layout.html", d); err != nil {
			slog.Error("ui render", "err", err)
		}
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
	return pageData{Pools: pools}, nil
}

func (s *Server) sandboxesData(r *http.Request) (pageData, error) {
	sandboxes, err := s.store.ListSandboxes(r.Context())
	if err != nil {
		return pageData{}, err
	}
	return pageData{Sandboxes: sandboxes}, nil
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

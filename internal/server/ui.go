package server

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

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

	// Static assets (CSS, JS).
	staticContent, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	// Full-page routes.
	mux.HandleFunc("GET /{$}", s.uiHandler(homeTmpl, "home", s.homeData))
	mux.HandleFunc("GET /ui/pools", s.uiHandler(poolsTmpl, "pools", s.poolsData))
	mux.HandleFunc("GET /ui/sandboxes", s.uiHandler(sandboxesTmpl, "sandboxes", s.sandboxesData))

	// HTMX fragment routes.
	mux.HandleFunc("GET /ui/fragments/stats", s.fragmentHandler(homeTmpl, "stats_fragment", s.homeData))
	mux.HandleFunc("GET /ui/fragments/pools-table", s.fragmentHandler(poolsTmpl, "pools_table_fragment", s.poolsData))
	mux.HandleFunc("GET /ui/fragments/sandboxes-table", s.fragmentHandler(sandboxesTmpl, "sandboxes_table_fragment", s.sandboxesData))
}

// dataFn loads data from the store into a pageData.
type dataFn func(r *http.Request) (pageData, error)

// uiHandler returns a handler that renders a full page (layout + content).
func (s *Server) uiHandler(tmpl *template.Template, nav string, data dataFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := data(r)
		if err != nil {
			slog.Error("ui data", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
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
			http.Error(w, "internal error", http.StatusInternalServerError)
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

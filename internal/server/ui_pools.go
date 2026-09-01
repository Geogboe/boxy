package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/model"
)

func buildPoolViews(pools []model.Pool, resources []model.Resource) []poolView {
	buckets := make(map[model.PoolName][]model.Resource)
	for _, resource := range resources {
		name := resource.EffectivePool()
		if name == "" {
			name = "unassigned"
		}
		buckets[name] = append(buckets[name], resource)
	}
	for name := range buckets {
		sort.SliceStable(buckets[name], func(i, j int) bool { return buckets[name][i].ID < buckets[name][j].ID })
	}

	views := make([]poolView, 0, len(pools)+1)
	seen := make(map[model.PoolName]struct{}, len(pools))
	for _, configured := range pools {
		seen[configured.Name] = struct{}{}
		entries := append([]model.Resource(nil), buckets[configured.Name]...)
		known := make(map[model.ResourceID]struct{}, len(entries))
		for _, resource := range entries {
			known[resource.ID] = struct{}{}
		}
		// Inventory is the source of truth for ready capacity during normal
		// operation. Include a legacy inventory entry if its resource record
		// has not been persisted yet, rather than hiding capacity from admins.
		for _, resource := range configured.Inventory.Resources {
			if _, ok := known[resource.ID]; ok {
				continue
			}
			entries = append(entries, resource)
		}
		views = append(views, makePoolView(configured, entries))
	}
	// Keep orphaned records visible even if their configured pool was removed.
	var extra []model.PoolName
	for name := range buckets {
		if _, ok := seen[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	for _, name := range extra {
		views = append(views, makePoolView(model.Pool{Name: name}, buckets[name]))
	}
	return views
}

func makePoolView(configured model.Pool, resources []model.Resource) poolView {
	view := poolView{
		Name: string(configured.Name), Type: configured.Inventory.ExpectedType,
		MinReady: configured.Policies.Preheat.MinReady, MaxTotal: configured.Policies.Preheat.MaxTotal,
		EffectivelyDrained: configured.EffectivelyDrained(),
		Resources:          make([]poolResourceView, 0, len(resources)),
	}
	for _, resource := range resources {
		view.TotalCount++
		if resource.State == model.ResourceStateReady {
			view.ReadyCount++
		}
		view.Resources = append(view.Resources, poolResourceView{
			ID: string(resource.ID), Type: resource.Type, Profile: resource.Profile,
			State: resource.State, Provider: resource.Provider.Name,
		})
	}
	return view
}

func requireUIAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal, ok := sessionPrincipalFromRequest(r)
	if !ok {
		redirectToLogin(w, r)
		return "", false
	}
	if principal.Role != model.APIKeyRoleAdmin {
		http.Error(w, "pool management requires an administrator", http.StatusForbidden)
		return "", false
	}
	return principal.Subject, true
}

func (s *Server) handleDrainPoolUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	if s.poolMaintenance == nil {
		redirectPoolResult(w, r, "error", "drain", "")
		return
	}
	_, err := s.poolMaintenance.Drain(r.Context(), model.PoolName(r.PathValue("name")))
	if err != nil {
		redirectPoolResult(w, r, "error", "drain", "")
		return
	}
	redirectPoolResult(w, r, "drain", "", r.PathValue("name"))
}

func (s *Server) handleFillPoolUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	if s.poolMaintenance == nil {
		redirectPoolResult(w, r, "error", "fill", "")
		return
	}
	_, err := s.poolMaintenance.Fill(r.Context(), model.PoolName(r.PathValue("name")))
	if err != nil {
		redirectPoolResult(w, r, "error", "fill", "")
		return
	}
	redirectPoolResult(w, r, "fill", "", r.PathValue("name"))
}

func (s *Server) handlePurgeResourcesUI(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUIAdmin(w, r)
	if !ok || !requireUICSRF(w, r) {
		return
	}
	if s.resourceCleanup == nil {
		redirectPoolResult(w, r, "error", "cleanup", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectPoolResult(w, r, "error", "cleanup", "")
		return
	}
	action := r.FormValue("action")
	request := pool.CleanupRequest{Actor: actor, DryRun: true}
	result := "cleanup_preview"
	if action == "force" {
		request.DryRun = false
		request.Force = true
		result = "cleanup_force"
	} else if action != "preview" {
		redirectPoolResult(w, r, "error", "cleanup", "")
		return
	}
	report, err := s.resourceCleanup.Purge(r.Context(), request)
	if err != nil {
		redirectPoolResult(w, r, "error", "cleanup", "")
		return
	}
	values := url.Values{}
	values.Set("result", result)
	values.Set("candidates", strconv.Itoa(report.CandidateCount))
	values.Set("cleaned", strconv.Itoa(len(report.CleanedIDs)))
	values.Set("skipped", strconv.Itoa(len(report.SkippedIDs)))
	values.Set("errors", strconv.Itoa(len(report.Errors)))
	http.Redirect(w, r, "/ui/pools?"+values.Encode(), http.StatusSeeOther)
}

func redirectPoolResult(w http.ResponseWriter, r *http.Request, result, action, name string) {
	values := url.Values{}
	values.Set("result", result)
	if action != "" {
		values.Set("action", action)
	}
	if name != "" {
		values.Set("pool", name)
	}
	http.Redirect(w, r, "/ui/pools?"+values.Encode(), http.StatusSeeOther)
}

func poolResultFromQuery(r *http.Request) string {
	query := r.URL.Query()
	switch query.Get("result") {
	case "drain":
		return fmt.Sprintf("Pool %q drained.", query.Get("pool"))
	case "fill":
		return fmt.Sprintf("Pool %q fill requested.", query.Get("pool"))
	case "error":
		return fmt.Sprintf("Pool %s action failed; inspect Diagnostics and try again.", query.Get("action"))
	case "cleanup_preview":
		return fmt.Sprintf("Cleanup preview: %d candidates, %d skipped, %d errors.", queryInt(query.Get("candidates")), queryInt(query.Get("skipped")), queryInt(query.Get("errors")))
	case "cleanup_force":
		return fmt.Sprintf("Cleanup complete: %d cleaned, %d skipped, %d errors.", queryInt(query.Get("cleaned")), queryInt(query.Get("skipped")), queryInt(query.Get("errors")))
	default:
		return ""
	}
}

func queryInt(value string) int {
	result, err := strconv.Atoi(value)
	if err != nil || result < 0 {
		return 0
	}
	return result
}

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
)

func buildPoolViews(pools []model.Pool, resources []model.Resource, poolJobs []jobs.Job) []poolView {
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
	activeJobs := make(map[string]jobs.Job)
	for _, job := range poolJobs {
		if !job.Status.IsTerminal() {
			activeJobs[job.Target] = job
		}
	}
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
		active, ok := activeJobs["pool:"+string(configured.Name)]
		views = append(views, makePoolView(configured, entries, active, ok))
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
		active, ok := activeJobs["pool:"+string(name)]
		views = append(views, makePoolView(model.Pool{Name: name}, buckets[name], active, ok))
	}
	return views
}

func makePoolView(configured model.Pool, resources []model.Resource, active jobs.Job, hasActive bool) poolView {
	view := poolView{
		Name:                string(configured.Name),
		DetailPath:          "/ui/pools/" + url.PathEscape(string(configured.Name)),
		Type:                configured.Inventory.ExpectedType,
		ExpectedProfile:     configured.Inventory.ExpectedProfile,
		Template:            configured.Template,
		Source:              configured.Source,
		Packages:            append([]string(nil), configured.Packages...),
		MinReady:            configured.Policies.Preheat.MinReady,
		MaxTotal:            configured.Policies.Preheat.MaxTotal,
		EffectivelyDrained:  configured.EffectivelyDrained(),
		ConfigDrain:         configured.Drain.ConfigDeclared,
		OperatorDrain:       configured.Drain.Operator,
		Resources:           make([]poolResourceView, 0, len(resources)),
		HistoricalResources: make([]poolResourceView, 0),
	}
	providerNames := make(map[string]struct{})
	for _, resource := range resources {
		if !isHistoricalResource(resource) {
			view.TotalCount++
		}
		if resource.State == model.ResourceStateReady {
			view.ReadyCount++
		}
		if resource.State == model.ResourceStateError {
			view.FailedCount++
		}
		resourceView := poolResourceView{
			ID: string(resource.ID), Type: resource.Type, Profile: resource.Profile,
			State: resource.State, Provider: resource.Provider.Name,
		}
		if isHistoricalResource(resource) {
			view.HistoricalResources = append(view.HistoricalResources, resourceView)
			view.HistoricalCount++
		} else {
			view.Resources = append(view.Resources, resourceView)
		}
		if resource.Provider.Name != "" {
			providerNames[resource.Provider.Name] = struct{}{}
		}
	}
	for provider := range providerNames {
		view.ProviderNames = append(view.ProviderNames, provider)
	}
	sort.Strings(view.ProviderNames)
	view.Status = poolStatus(view, resources, active, hasActive)
	if hasActive {
		view.ActiveJobID = active.ID
		view.ActiveJobKind = active.Kind
	}
	return view
}

func poolStatus(view poolView, resources []model.Resource, active jobs.Job, hasActive bool) string {
	if hasActive {
		if active.Kind == "pool.drain" || active.Kind == "pool.destroy" {
			return "draining"
		}
		return "filling"
	}
	if view.EffectivelyDrained {
		return "draining"
	}
	if view.FailedCount > 0 && view.MaxTotal > 0 && view.TotalCount >= view.MaxTotal && view.ReadyCount < view.MinReady {
		return "blocked"
	}
	for _, resource := range resources {
		if resource.State == model.ResourceStateProvisioning || resource.State == model.ResourceStatePromoting {
			return "filling"
		}
		if resource.State == model.ResourceStateDestroying || resource.State == model.ResourceStateRecycling {
			return "draining"
		}
	}
	if view.ReadyCount >= view.MinReady {
		return "ready"
	}
	return "unknown"
}

func isHistoricalResource(resource model.Resource) bool {
	return resource.State == model.ResourceStateReleased || resource.State == model.ResourceStateDestroyed
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
	job, err := s.startPoolMaintenanceJob(r.Context(), "pool.drain", model.PoolName(r.PathValue("name")), s.poolMaintenance.Drain)
	if err != nil {
		redirectPoolResult(w, r, "error", "drain", "")
		return
	}
	redirectPoolJob(w, r, "drain", r.PathValue("name"), job.ID)
}

func (s *Server) handleFillPoolUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	if s.poolMaintenance == nil {
		redirectPoolResult(w, r, "error", "fill", "")
		return
	}
	job, err := s.startPoolMaintenanceJob(r.Context(), "pool.fill", model.PoolName(r.PathValue("name")), s.poolMaintenance.Fill)
	if err != nil {
		redirectPoolResult(w, r, "error", "fill", "")
		return
	}
	redirectPoolJob(w, r, "fill", r.PathValue("name"), job.ID)
}

func (s *Server) handleRetryPoolResourceUI(w http.ResponseWriter, r *http.Request) {
	s.handlePoolResourceJobUI(w, r, "pool.retry", func(ctx context.Context, maintenance PoolResourceMaintenance, resource model.Resource) error {
		return maintenance.RetryResource(ctx, resource)
	})
}

func (s *Server) handleDestroyPoolResourceUI(w http.ResponseWriter, r *http.Request) {
	s.handlePoolResourceJobUI(w, r, "pool.destroy", func(ctx context.Context, maintenance PoolResourceMaintenance, resource model.Resource) error {
		if err := maintenance.DestroyResource(ctx, resource); err != nil {
			return err
		}
		if reconciler, ok := s.poolMaintenance.(interface {
			Reconcile(context.Context, model.PoolName) error
		}); ok {
			return reconciler.Reconcile(ctx, resource.OriginPool)
		}
		return nil
	})
}

func (s *Server) handlePoolResourceJobUI(w http.ResponseWriter, r *http.Request, kind string, operation func(context.Context, PoolResourceMaintenance, model.Resource) error) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	poolName := model.PoolName(r.PathValue("name"))
	job, err := s.startPoolResourceJob(r.Context(), kind, poolName, model.ResourceID(r.PathValue("id")), operation)
	if err != nil {
		redirectPoolResult(w, r, "error", kind, string(poolName))
		return
	}
	redirectPoolJob(w, r, kind, string(poolName), job.ID)
}

func (s *Server) handleCancelJobUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	runner, err := s.ensureJobRunner()
	if err == nil {
		_, err = runner.Cancel(r.Context(), jobs.ID(r.PathValue("id")))
	}
	if err != nil {
		redirectPoolResult(w, r, "error", "cancel", r.FormValue("pool"))
		return
	}
	redirectPoolResult(w, r, "cancel", "", r.FormValue("pool"))
}

func redirectPoolJob(w http.ResponseWriter, r *http.Request, action, name string, id jobs.ID) {
	values := url.Values{}
	values.Set("result", "started")
	values.Set("action", action)
	values.Set("pool", name)
	values.Set("job", string(id))
	http.Redirect(w, r, "/ui/pools?"+values.Encode(), http.StatusSeeOther)
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

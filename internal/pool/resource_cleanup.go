package pool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

const ForcedCleanupAge = 30 * time.Minute

// CleanupRequest selects a safe resource-maintenance operation.
type CleanupRequest struct {
	Actor  string `json:"actor,omitempty"`
	DryRun bool   `json:"dry_run"`
	Force  bool   `json:"force"`
}

type CleanupSkipped struct {
	ID     model.ResourceID `json:"id"`
	Reason string           `json:"reason"`
}

type CleanupError struct {
	ID    model.ResourceID `json:"id"`
	Error string           `json:"error"`
}

// CleanupReport is intentionally composed of safe identifiers and status
// metadata. It never includes resource Properties or provider credentials.
type CleanupReport struct {
	DryRun         bool               `json:"dry_run"`
	Force          bool               `json:"force"`
	CandidateCount int                `json:"candidate_count"`
	CandidateIDs   []model.ResourceID `json:"candidate_ids"`
	CleanedIDs     []model.ResourceID `json:"cleaned_ids"`
	SkippedIDs     []CleanupSkipped   `json:"skipped_ids"`
	Errors         []CleanupError     `json:"errors"`
}

// ResourceCleanupService is shared by maintenance callers. The CLI and UI
// reach it through the REST handler, while tests and daemon wiring can use it
// directly. Manager is required for forced cleanup because it owns the normal
// provider destroy/retry lifecycle.
type ResourceCleanupService struct {
	Store   store.Store
	Manager *Manager
	Audit   diagnostics.AuditSink
	Now     func() time.Time
}

func (s *ResourceCleanupService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *ResourceCleanupService) Purge(ctx context.Context, request CleanupRequest) (CleanupReport, error) {
	if s == nil || s.Store == nil {
		return CleanupReport{}, errors.New("resource cleanup store is required")
	}
	// Cleanup is deliberately opt-in: callers must request --force before any
	// provider or state mutation is attempted. A plain request is therefore a
	// preview, which also keeps direct service callers safe by default.
	if !request.Force {
		request.DryRun = true
	}
	resources, err := s.Store.ListResources(ctx)
	if err != nil {
		return CleanupReport{}, fmt.Errorf("list resources: %w", err)
	}
	sandboxes, err := s.Store.ListSandboxes(ctx)
	if err != nil {
		return CleanupReport{}, fmt.Errorf("list sandboxes: %w", err)
	}
	referenced := make(map[model.ResourceID]struct{})
	for _, sandbox := range sandboxes {
		for _, id := range sandbox.Resources {
			referenced[id] = struct{}{}
		}
	}

	report := CleanupReport{DryRun: request.DryRun, Force: request.Force, CandidateIDs: []model.ResourceID{}, CleanedIDs: []model.ResourceID{}, SkippedIDs: []CleanupSkipped{}, Errors: []CleanupError{}}
	now := s.now()
	candidates := make(map[model.ResourceID]model.Resource)
	for _, resource := range resources {
		if _, ok := referenced[resource.ID]; ok {
			report.SkippedIDs = append(report.SkippedIDs, CleanupSkipped{ID: resource.ID, Reason: "referenced by a sandbox"})
			continue
		}
		candidate, reason := cleanupCandidate(resource, request.Force, now)
		if !candidate {
			report.SkippedIDs = append(report.SkippedIDs, CleanupSkipped{ID: resource.ID, Reason: reason})
			continue
		}
		candidates[resource.ID] = resource
		report.CandidateIDs = append(report.CandidateIDs, resource.ID)
	}
	sort.Slice(report.CandidateIDs, func(i, j int) bool { return report.CandidateIDs[i] < report.CandidateIDs[j] })
	report.CandidateCount = len(report.CandidateIDs)
	if request.DryRun {
		return report, nil
	}
	for _, id := range report.CandidateIDs {
		resource := candidates[id]
		var cleanupErr error
		if resource.State == model.ResourceStateDestroyed {
			cleanupErr = s.purgeDestroyed(ctx, resource)
		} else {
			if s.Manager == nil {
				cleanupErr = errors.New("forced cleanup manager is not configured")
			} else {
				cleanupErr = s.Manager.DestroyResource(ctx, resource)
			}
		}
		if cleanupErr != nil {
			report.Errors = append(report.Errors, CleanupError{ID: id, Error: cleanupErr.Error()})
			continue
		}
		report.CleanedIDs = append(report.CleanedIDs, id)
	}
	s.recordAudit(ctx, request, report)
	return report, nil
}

func cleanupCandidate(resource model.Resource, force bool, now time.Time) (bool, string) {
	switch resource.State {
	case model.ResourceStateDestroyed:
		return true, ""
	case model.ResourceStateDestroying, model.ResourceStateError:
		if !force {
			return false, "forced cleanup requires --force"
		}
		updated := resource.UpdatedAt
		if updated.IsZero() {
			updated = resource.CreatedAt
		}
		if updated.IsZero() || now.Sub(updated) < ForcedCleanupAge {
			return false, "resource is younger than the 30 minute forced-cleanup threshold"
		}
		return true, ""
	default:
		return false, "resource state is protected from bulk cleanup"
	}
}

func (s *ResourceCleanupService) purgeDestroyed(ctx context.Context, resource model.Resource) error {
	pools, err := s.Store.ListPools(ctx)
	if err != nil {
		return fmt.Errorf("list pools for resource %q: %w", resource.ID, err)
	}
	for _, pool := range pools {
		updated := removeInventoryResource(pool.Inventory.Resources, resource.ID)
		if len(updated) == len(pool.Inventory.Resources) {
			continue
		}
		pool.Inventory.Resources = updated
		if err := s.Store.PutPool(ctx, pool); err != nil {
			return fmt.Errorf("remove resource %q from pool %q: %w", resource.ID, pool.Name, err)
		}
	}
	if err := s.Store.DeleteResource(ctx, resource.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete destroyed resource %q: %w", resource.ID, err)
	}
	return nil
}

func (s *ResourceCleanupService) recordAudit(ctx context.Context, request CleanupRequest, report CleanupReport) {
	if s.Audit == nil {
		return
	}
	audit, ok := s.Audit.(diagnostics.ResourceCleanupAuditSink)
	if !ok {
		return
	}
	_ = audit.RecordResourceCleanup(ctx, diagnostics.ResourceCleanupAudit{
		Actor: request.Actor, Mode: "resource_purge", Force: request.Force, State: "destroyed,error,destroying",
		Unreferenced: true, OlderThan: ForcedCleanupAge.String(), CandidateCount: report.CandidateCount,
		CleanedCount: len(report.CleanedIDs), SkippedCount: len(report.SkippedIDs), ErrorCount: len(report.Errors),
	})
}

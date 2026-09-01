package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type cleanupAuditRecorder struct {
	audit diagnostics.ResourceCleanupAudit
	calls int
}

func (a *cleanupAuditRecorder) RecordDiagnosticsQuery(context.Context, diagnostics.QueryAudit) error {
	return nil
}

func (a *cleanupAuditRecorder) RecordResourceCleanup(_ context.Context, audit diagnostics.ResourceCleanupAudit) error {
	a.calls++
	a.audit = audit
	return nil
}

func cleanupResource(id model.ResourceID, state model.ResourceState, updated time.Time) model.Resource {
	return model.Resource{ID: id, OriginPool: "pool-a", Type: model.ResourceTypeContainer,
		Profile: model.ResourceProfileDefault, State: state, CreatedAt: updated, UpdatedAt: updated}
}

func cleanupPool(resources ...model.Resource) model.Pool {
	return model.Pool{Name: "pool-a", Inventory: model.ResourceCollection{
		ExpectedType: model.ResourceTypeContainer, ExpectedProfile: model.ResourceProfileDefault,
		Resources: resources,
	}}
}

func TestResourceCleanup_PurgeDefaultsToDryRunAndHonorsReferences(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	free := cleanupResource("destroyed-free", model.ResourceStateDestroyed, now.Add(-time.Hour))
	referenced := cleanupResource("destroyed-referenced", model.ResourceStateDestroyed, now.Add(-time.Hour))
	ready := cleanupResource("ready", model.ResourceStateReady, now.Add(-time.Hour))
	for _, resource := range []model.Resource{free, referenced, ready} {
		if err := st.PutResource(ctx, resource); err != nil {
			t.Fatalf("PutResource(%s): %v", resource.ID, err)
		}
	}
	if err := st.PutPool(ctx, cleanupPool(free, referenced, ready)); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	if err := st.CreateSandbox(ctx, model.Sandbox{ID: "sandbox-1", Resources: []model.ResourceID{referenced.ID}}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	audit := &cleanupAuditRecorder{}
	service := &ResourceCleanupService{Store: st, Audit: audit, Now: func() time.Time { return now }}

	report, err := service.Purge(ctx, CleanupRequest{Actor: "admin"})
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if !report.DryRun || report.Force {
		t.Fatalf("report mode = dry_run:%t force:%t, want dry-run without force", report.DryRun, report.Force)
	}
	if report.CandidateCount != 1 || len(report.CandidateIDs) != 1 || report.CandidateIDs[0] != free.ID {
		t.Fatalf("candidates = %+v, want only %q", report.CandidateIDs, free.ID)
	}
	if len(report.CleanedIDs) != 0 {
		t.Fatalf("cleaned = %+v during dry-run", report.CleanedIDs)
	}
	if audit.calls != 0 {
		t.Fatal("dry-run must not record a mutation audit event")
	}
	if _, err := st.GetResource(ctx, free.ID); err != nil {
		t.Fatalf("dry-run removed resource: %v", err)
	}
}

func TestResourceCleanup_ForceRetriesEligibleStatesAndAudits(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	oldError := cleanupResource("old-error", model.ResourceStateError, now.Add(-31*time.Minute))
	oldDestroying := cleanupResource("old-destroying", model.ResourceStateDestroying, now.Add(-2*time.Hour))
	recentError := cleanupResource("recent-error", model.ResourceStateError, now.Add(-29*time.Minute))
	for _, resource := range []model.Resource{oldError, oldDestroying, recentError} {
		if err := st.PutResource(ctx, resource); err != nil {
			t.Fatalf("PutResource(%s): %v", resource.ID, err)
		}
	}
	if err := st.PutPool(ctx, cleanupPool(oldError, oldDestroying, recentError)); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	audit := &cleanupAuditRecorder{}
	provisioner := &fakeProvisioner{}
	service := &ResourceCleanupService{
		Store: st, Manager: New(st, provisioner), Audit: audit,
		Now: func() time.Time { return now },
	}

	report, err := service.Purge(ctx, CleanupRequest{Actor: "key-admin", Force: true})
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if report.DryRun || !report.Force || report.CandidateCount != 2 {
		t.Fatalf("report = %+v, want forced mutation with two candidates", report)
	}
	if len(report.CleanedIDs) != 2 || len(report.Errors) != 0 {
		t.Fatalf("cleaned=%+v errors=%+v", report.CleanedIDs, report.Errors)
	}
	if audit.calls != 1 || audit.audit.Actor != "key-admin" || audit.audit.CleanedCount != 2 || audit.audit.ErrorCount != 0 {
		t.Fatalf("audit = %+v calls=%d", audit.audit, audit.calls)
	}
	for _, id := range []model.ResourceID{oldError.ID, oldDestroying.ID} {
		if _, err := st.GetResource(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("resource %s after force cleanup = %v, want not found", id, err)
		}
	}
	if _, err := st.GetResource(ctx, recentError.ID); err != nil {
		t.Fatalf("recent resource was removed: %v", err)
	}
}

func TestResourceCleanup_FailedProviderResourceRemainsRecorded(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	resource := cleanupResource("destroy-fails", model.ResourceStateError, now.Add(-time.Hour))
	if err := st.PutResource(ctx, resource); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	if err := st.PutPool(ctx, cleanupPool(resource)); err != nil {
		t.Fatalf("PutPool: %v", err)
	}
	provisioner := &fakeProvisioner{destroyErr: errors.New("provider unavailable")}
	service := &ResourceCleanupService{Store: st, Manager: New(st, provisioner), Now: func() time.Time { return now }}

	report, err := service.Purge(ctx, CleanupRequest{Force: true})
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if len(report.CleanedIDs) != 0 || len(report.Errors) != 1 || report.Errors[0].ID != resource.ID {
		t.Fatalf("report = %+v, want one individual cleanup error", report)
	}
	if _, err := st.GetResource(ctx, resource.ID); err != nil {
		t.Fatalf("failed resource was not retained: %v", err)
	}
}

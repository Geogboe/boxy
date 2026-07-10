package agentsdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/providers/devfactory"
)

func newTestAgent(t *testing.T) *agentsdk.EmbeddedAgent {
	t.Helper()
	d := devfactory.New(&devfactory.Config{DataDir: t.TempDir()})
	agent, err := agentsdk.NewEmbeddedAgent("test-agent", "Test Agent", d)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	return agent
}

func TestEmbeddedAgent_Info(t *testing.T) {
	agent := newTestAgent(t)
	info := agent.Info()

	if info.ID != "test-agent" {
		t.Errorf("expected ID test-agent, got %q", info.ID)
	}
	if info.Name != "Test Agent" {
		t.Errorf("expected Name Test Agent, got %q", info.Name)
	}
	if len(info.Providers) != 1 || info.Providers[0] != devfactory.ProviderType {
		t.Errorf("expected providers [devfactory], got %v", info.Providers)
	}
}

func TestEmbeddedAgent_CRUD(t *testing.T) {
	agent := newTestAgent(t)
	ctx := context.Background()

	// Create
	res, err := agent.Create(ctx, devfactory.ProviderType, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID == "" {
		t.Fatal("expected non-empty resource ID")
	}

	// Read
	status, err := agent.Read(ctx, devfactory.ProviderType, res.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected running state, got %q", status.State)
	}

	// Update
	result, err := agent.Update(ctx, devfactory.ProviderType, res.ID, &devfactory.ExecOp{
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Outputs["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result.Outputs["status"])
	}

	// Delete
	if err := agent.Delete(ctx, devfactory.ProviderType, res.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify gone.
	_, err = agent.Read(ctx, devfactory.ProviderType, res.ID)
	if err == nil {
		t.Fatal("expected error reading deleted resource")
	}
}

func TestEmbeddedAgent_UnknownProvider(t *testing.T) {
	agent := newTestAgent(t)
	ctx := context.Background()

	_, err := agent.Create(ctx, providersdk.Type("nonexistent"), nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}

}

func TestEmbeddedAgent_UnknownProviderForEveryLifecycleMethod(t *testing.T) {
	agent := newTestAgent(t)
	ctx := context.Background()
	missing := providersdk.Type("nonexistent")

	if _, err := agent.Read(ctx, missing, "res-1"); err == nil {
		t.Fatal("Read unknown provider error = nil")
	}
	if _, err := agent.Update(ctx, missing, "res-1", &devfactory.ExecOp{}); err == nil {
		t.Fatal("Update unknown provider error = nil")
	}
	if err := agent.Delete(ctx, missing, "res-1"); err == nil {
		t.Fatal("Delete unknown provider error = nil")
	}
	if _, err := agent.Allocate(ctx, missing, "res-1"); err == nil {
		t.Fatal("Allocate unknown provider error = nil")
	}
	if result, err := agent.PersonalizeGuest(ctx, missing, "res-1"); err == nil || result != nil {
		t.Fatalf("PersonalizeGuest unknown provider result=%v err=%v, want error", result, err)
	}
	if _, err := agent.List(ctx, missing); err == nil {
		t.Fatal("List unknown provider error = nil")
	}
}

// TestEmbeddedAgent_ListReturnsErrorForDriverWithoutCapability verifies that
// an unsupported List returns an error rather than an empty slice — an empty
// slice would be indistinguishable from "confirmed nothing exists," which
// the #133 reconciliation sweep must never assume for a driver that simply
// can't enumerate.
func TestEmbeddedAgent_ListReturnsErrorForDriverWithoutCapability(t *testing.T) {
	agent := newTestAgent(t)

	statuses, err := agent.List(context.Background(), devfactory.ProviderType)
	if err == nil {
		t.Fatalf("List result = %+v, err = nil; want error for driver without ResourceLister", statuses)
	}
}

func TestEmbeddedAgent_PersonalizeGuestReturnsNilForDriverWithoutCapability(t *testing.T) {
	agent := newTestAgent(t)

	result, err := agent.PersonalizeGuest(context.Background(), devfactory.ProviderType, "res-1")
	if err != nil {
		t.Fatalf("PersonalizeGuest: %v", err)
	}
	if result != nil {
		t.Fatalf("PersonalizeGuest result = %+v, want nil for driver without capability", result)
	}
}

func TestEmbeddedAgent_DuplicateProviderType(t *testing.T) {
	d1 := devfactory.New(&devfactory.Config{DataDir: t.TempDir()})
	d2 := devfactory.New(&devfactory.Config{DataDir: t.TempDir()})

	_, err := agentsdk.NewEmbeddedAgent("dup", "Dup", d1, d2)
	if err == nil {
		t.Fatal("expected error for duplicate provider type")
	}
}

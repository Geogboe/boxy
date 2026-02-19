package agentsdk_test

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/v2/pkg/agentsdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/providers/devboxes"
)

func newTestAgent(t *testing.T) *agentsdk.EmbeddedAgent {
	t.Helper()
	d := devboxes.New(&devboxes.Config{DataDir: t.TempDir()})
	return agentsdk.NewEmbeddedAgent("test-agent", "Test Agent", d)
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
	if len(info.Providers) != 1 || info.Providers[0] != devboxes.ProviderType {
		t.Errorf("expected providers [devboxes], got %v", info.Providers)
	}
}

func TestEmbeddedAgent_CRUD(t *testing.T) {
	agent := newTestAgent(t)
	ctx := context.Background()

	// Create
	res, err := agent.Create(ctx, devboxes.ProviderType, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID == "" {
		t.Fatal("expected non-empty resource ID")
	}

	// Read
	status, err := agent.Read(ctx, devboxes.ProviderType, res.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if status.State != "running" {
		t.Errorf("expected running state, got %q", status.State)
	}

	// Update
	result, err := agent.Update(ctx, devboxes.ProviderType, res.ID, &devboxes.ExecOp{
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Outputs["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result.Outputs["status"])
	}

	// Delete
	if err := agent.Delete(ctx, devboxes.ProviderType, res.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify gone.
	_, err = agent.Read(ctx, devboxes.ProviderType, res.ID)
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

func TestEmbeddedAgent_MultipleProviders(t *testing.T) {
	d1 := devboxes.New(&devboxes.Config{
		DataDir: t.TempDir(),
		Profile: devboxes.ProfileContainer,
	})
	d2 := devboxes.New(&devboxes.Config{
		DataDir: t.TempDir(),
		Profile: devboxes.ProfileVM,
	})

	// Both are "devboxes" type — last one wins in the driver map.
	// This test verifies multi-driver construction works.
	agent := agentsdk.NewEmbeddedAgent("multi", "Multi", d1, d2)
	info := agent.Info()

	// We get 2 entries in the providers list (both "devboxes").
	if len(info.Providers) != 2 {
		t.Errorf("expected 2 provider entries, got %d", len(info.Providers))
	}
}

package pool

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// --- reconcileEvaluator: diff logic, in isolation ---

func TestReconcileEvaluator(t *testing.T) {
	eval := reconcileEvaluator()

	t.Run("adopts a remote resource the store never tracked", func(t *testing.T) {
		obs := reconcileObserved{
			agentID: "agent-1",
			tracked: nil,
			remote: map[model.ResourceID]remoteEntry{
				"orphan-1": {provider: "docker", status: providersdk.ResourceStatus{ID: "orphan-1", State: "running"}},
			},
			listedProviders: map[providersdk.Type]int{"docker": 1},
		}
		decision, err := eval.Evaluate(context.Background(), obs)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if !decision.ShouldAct {
			t.Fatal("expected ShouldAct=true")
		}
		if len(decision.Plan.adopt) != 1 || decision.Plan.adopt[0].ID != "orphan-1" {
			t.Fatalf("unexpected adopt plan: %+v", decision.Plan.adopt)
		}
		if len(decision.Plan.reap) != 0 {
			t.Fatalf("expected no reaps, got %+v", decision.Plan.reap)
		}
	})

	t.Run("reaps a tracked resource the agent no longer reports", func(t *testing.T) {
		obs := reconcileObserved{
			agentID: "agent-1",
			tracked: []model.Resource{
				{ID: "gone-1", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}},
				{ID: "still-here", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}},
			},
			remote: map[model.ResourceID]remoteEntry{
				"still-here": {provider: "docker", status: providersdk.ResourceStatus{ID: "still-here", State: "running"}},
			},
			listedProviders: map[providersdk.Type]int{"docker": 1},
		}
		decision, err := eval.Evaluate(context.Background(), obs)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(decision.Plan.reap) != 1 || decision.Plan.reap[0] != "gone-1" {
			t.Fatalf("unexpected reap plan: %+v", decision.Plan.reap)
		}
		if len(decision.Plan.adopt) != 0 {
			t.Fatalf("expected no adopts, got %+v", decision.Plan.adopt)
		}
	})

	t.Run("never reaps a provider type that failed to list this pass", func(t *testing.T) {
		obs := reconcileObserved{
			agentID: "agent-1",
			tracked: []model.Resource{
				{ID: "unverifiable", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"}},
			},
			remote:          map[model.ResourceID]remoteEntry{}, // hyperv wasn't listed at all
			listedProviders: map[providersdk.Type]int{},         // empty: hyperv absent entirely
		}
		decision, err := eval.Evaluate(context.Background(), obs)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.ShouldAct {
			t.Fatalf("expected no action for an unlisted provider type, got %+v", decision.Plan)
		}
	})

	t.Run("safety valve: never reaps when a listed provider type came back suspiciously empty", func(t *testing.T) {
		obs := reconcileObserved{
			agentID: "agent-1",
			tracked: []model.Resource{
				{ID: "res-a", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}},
				{ID: "res-b", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}},
			},
			remote:          map[model.ResourceID]remoteEntry{},
			listedProviders: map[providersdk.Type]int{"docker": 0}, // listed successfully, but zero results
		}
		decision, err := eval.Evaluate(context.Background(), obs)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.ShouldAct {
			t.Fatalf("expected the safety valve to suppress reaping, got %+v", decision.Plan)
		}
	})

	t.Run("noop when everything already agrees", func(t *testing.T) {
		obs := reconcileObserved{
			agentID: "agent-1",
			tracked: []model.Resource{
				{ID: "res-a", Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"}},
			},
			remote: map[model.ResourceID]remoteEntry{
				"res-a": {provider: "docker", status: providersdk.ResourceStatus{ID: "res-a", State: "running"}},
			},
			listedProviders: map[providersdk.Type]int{"docker": 1},
		}
		decision, err := eval.Evaluate(context.Background(), obs)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.ShouldAct {
			t.Fatalf("expected noop, got %+v", decision.Plan)
		}
	})
}

// --- ReconcileAgent: end-to-end against a real MemoryStore/AgentRegistry ---

// fakeListingAgent is a minimal agentsdk.Agent + agentsdk.ResourceListingAgent
// test double. Unlike mockAgent (provisioner_agent_test.go), it exists
// specifically to control List's per-provider-type results/errors.
type fakeListingAgent struct {
	info        agentsdk.AgentInfo
	listResults map[providersdk.Type][]providersdk.ResourceStatus
	listErrs    map[providersdk.Type]error
}

func (a *fakeListingAgent) Info() agentsdk.AgentInfo { return a.info }
func (a *fakeListingAgent) Create(context.Context, providersdk.Type, any) (*providersdk.Resource, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeListingAgent) Read(context.Context, providersdk.Type, string) (*providersdk.ResourceStatus, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeListingAgent) Update(context.Context, providersdk.Type, string, providersdk.Operation) (*providersdk.Result, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeListingAgent) Delete(context.Context, providersdk.Type, string) error {
	return errors.New("not implemented")
}
func (a *fakeListingAgent) Allocate(context.Context, providersdk.Type, string) (map[string]any, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeListingAgent) List(_ context.Context, provider providersdk.Type) ([]providersdk.ResourceStatus, error) {
	if err, ok := a.listErrs[provider]; ok {
		return nil, err
	}
	return a.listResults[provider], nil
}

// fakeNonListingAgent implements agentsdk.Agent only, deliberately missing
// List — for exercising ReconcileAgent's behavior against an agent whose
// driver(s) don't support enumeration at all. Not a fakeListingAgent
// embedding (that would promote List and defeat the point).
type fakeNonListingAgent struct {
	info agentsdk.AgentInfo
}

func (a *fakeNonListingAgent) Info() agentsdk.AgentInfo { return a.info }
func (a *fakeNonListingAgent) Create(context.Context, providersdk.Type, any) (*providersdk.Resource, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeNonListingAgent) Read(context.Context, providersdk.Type, string) (*providersdk.ResourceStatus, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeNonListingAgent) Update(context.Context, providersdk.Type, string, providersdk.Operation) (*providersdk.Result, error) {
	return nil, errors.New("not implemented")
}
func (a *fakeNonListingAgent) Delete(context.Context, providersdk.Type, string) error {
	return errors.New("not implemented")
}
func (a *fakeNonListingAgent) Allocate(context.Context, providersdk.Type, string) (map[string]any, error) {
	return nil, errors.New("not implemented")
}

func TestReconcileAgent_AdoptsOrphanAcrossStoreAndRegistry(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	agent := &fakeListingAgent{
		info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"docker"}},
		listResults: map[providersdk.Type][]providersdk.ResourceStatus{
			"docker": {{ID: "orphan-1", State: "running"}},
		},
	}
	registry := registryWith(t, agent)

	if err := ReconcileAgent(ctx, st, registry, "agent-1", slog.Default()); err != nil {
		t.Fatalf("ReconcileAgent: %v", err)
	}

	res, err := st.GetResource(ctx, "orphan-1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res.Provider.AgentID != "agent-1" || res.Provider.Name != "docker" {
		t.Errorf("unexpected provider ref: %+v", res.Provider)
	}
	if res.State != model.ResourceStateUnknown {
		t.Errorf("expected State=Unknown for an adopted orphan, got %v", res.State)
	}
}

func TestReconcileAgent_ReapsConfirmedGoneResource(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutResource(ctx, model.Resource{
		ID:       "gone-1",
		Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"},
		State:    model.ResourceStateReady,
	}); err != nil {
		t.Fatalf("seed PutResource: %v", err)
	}
	// A second docker resource under the same provider type keeps the list
	// non-empty, so the safety valve doesn't suppress the reap.
	if err := st.PutResource(ctx, model.Resource{
		ID:       "still-here",
		Provider: model.ProviderRef{Name: "docker", AgentID: "agent-1"},
		State:    model.ResourceStateReady,
	}); err != nil {
		t.Fatalf("seed PutResource: %v", err)
	}

	agent := &fakeListingAgent{
		info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"docker"}},
		listResults: map[providersdk.Type][]providersdk.ResourceStatus{
			"docker": {{ID: "still-here", State: "running"}},
		},
	}
	registry := registryWith(t, agent)

	if err := ReconcileAgent(ctx, st, registry, "agent-1", slog.Default()); err != nil {
		t.Fatalf("ReconcileAgent: %v", err)
	}

	res, err := st.GetResource(ctx, "gone-1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res.State != model.ResourceStateDestroyed {
		t.Errorf("expected gone-1 to be marked Destroyed, got %v", res.State)
	}

	stillHere, err := st.GetResource(ctx, "still-here")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if stillHere.State != model.ResourceStateReady {
		t.Errorf("expected still-here to be untouched, got %v", stillHere.State)
	}
}

func TestReconcileAgent_SkipsAuditForUnlistableProvider(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutResource(ctx, model.Resource{
		ID:       "vm-1",
		Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-1"},
		State:    model.ResourceStateReady,
	}); err != nil {
		t.Fatalf("seed PutResource: %v", err)
	}

	agent := &fakeListingAgent{
		info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"hyperv"}},
		listErrs: map[providersdk.Type]error{
			"hyperv": errors.New("list not supported by driver \"hyperv\""),
		},
	}
	registry := registryWith(t, agent)

	if err := ReconcileAgent(ctx, st, registry, "agent-1", slog.Default()); err != nil {
		t.Fatalf("ReconcileAgent: %v", err)
	}

	res, err := st.GetResource(ctx, "vm-1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res.State != model.ResourceStateReady {
		t.Errorf("expected vm-1 to be untouched when its provider can't be listed, got %v", res.State)
	}
}

func TestReconcileAgent_NoResourceListingCapability(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	agent := &fakeNonListingAgent{
		info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"docker"}},
	}
	registry := registryWith(t, agent)

	// Must not error even though this agent can't be audited at all.
	if err := ReconcileAgent(ctx, st, registry, "agent-1", slog.Default()); err != nil {
		t.Fatalf("ReconcileAgent: %v", err)
	}
}

func TestReconcileAgent_UnregisteredAgentErrors(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	registry := NewAgentRegistry()

	if err := ReconcileAgent(ctx, st, registry, "does-not-exist", slog.Default()); err == nil {
		t.Fatal("expected an error for an unregistered agent")
	}
}

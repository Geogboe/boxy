package sandbox

import (
	"context"
	"errors"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	boxypool "github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// stubIsolatingAgent is a full agentsdk.Agent that ALSO implements
// agentsdk.NetworkIsolatingAgent unconditionally -- exactly like the real
// EmbeddedAgent and RemoteAgent, both of which satisfy that interface
// whether or not the driver behind them can isolate anything. What it
// advertises in AgentInfo.NetworkIsolatingProviders is set per test; that
// advertisement, not the interface, is what the control plane must obey.
type stubIsolatingAgent struct {
	info               agentsdk.AgentInfo
	createSegmentRef   providersdk.SegmentRef
	createSegmentErr   error
	attachErr          error
	createSegmentCall  int
	attachCall         int
	destroySegmentCall int
}

func (s *stubIsolatingAgent) Info() agentsdk.AgentInfo { return s.info }
func (s *stubIsolatingAgent) Create(context.Context, providersdk.Type, any) (*providersdk.Resource, error) {
	return nil, errors.New("not used")
}
func (s *stubIsolatingAgent) Read(context.Context, providersdk.Type, string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (s *stubIsolatingAgent) Update(context.Context, providersdk.Type, string, providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (s *stubIsolatingAgent) Delete(context.Context, providersdk.Type, string) error { return nil }
func (s *stubIsolatingAgent) Allocate(context.Context, providersdk.Type, string) (map[string]any, error) {
	return nil, nil
}
func (s *stubIsolatingAgent) CreateSegment(_ context.Context, _ providersdk.Type, _ string) (providersdk.SegmentRef, error) {
	s.createSegmentCall++
	return s.createSegmentRef, s.createSegmentErr
}
func (s *stubIsolatingAgent) AttachToSegment(_ context.Context, _ providersdk.Type, _ string, _ providersdk.SegmentRef) error {
	s.attachCall++
	return s.attachErr
}
func (s *stubIsolatingAgent) DestroySegment(context.Context, providersdk.Type, providersdk.SegmentRef) error {
	s.destroySegmentCall++
	return nil
}

var (
	_ agentsdk.Agent                 = (*stubIsolatingAgent)(nil)
	_ agentsdk.NetworkIsolatingAgent = (*stubIsolatingAgent)(nil)
)

// isolationFixture wires a real pool.AgentProvisioner (NOT a hand-rolled
// allocator fake) over agent, plus a one-resource pool, so the whole chain
// Manager.CreateFromPool -> ensureNetworkSegment -> AgentProvisioner.CreateSegment
// is exercised end to end.
func isolationFixture(t *testing.T, agent *stubIsolatingAgent, resourceIDs ...model.ResourceID) (*Manager, store.Store) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemoryStore()

	resources := make([]model.Resource, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		res := model.Resource{
			ID:       id,
			Type:     model.ResourceTypeVM,
			Profile:  model.ResourceProfileDefault,
			State:    model.ResourceStateReady,
			Provider: model.ProviderRef{Name: "hyperv", AgentID: agent.Info().ID},
		}
		resources = append(resources, res)
		stored := res
		stored.OriginPool = "pool-a"
		if err := st.PutResource(ctx, stored); err != nil {
			t.Fatalf("PutResource: %v", err)
		}
	}
	if err := st.PutPool(ctx, model.Pool{
		Name: "pool-a",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: model.ResourceProfileDefault,
			Resources:       resources,
		},
	}); err != nil {
		t.Fatalf("PutPool: %v", err)
	}

	registry := boxypool.NewAgentRegistry()
	if err := registry.Register(agent); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ap := &boxypool.AgentProvisioner{
		Registry: registry,
		Specs:    map[model.PoolName]boxyconfig.PoolSpec{"pool-a": {Type: "hyperv"}},
	}
	return New(st, ap), st
}

// TestManager_CreateFromPool_NonAdvertisingAgentAllocatesWithNoSegments is
// the regression test C1's review named as its top recommendation, and the
// exact combination no test in Plans 1a/1b/1c reached: a real
// AgentProvisioner plus an agent that satisfies NetworkIsolatingAgent but
// advertises no isolation for the pool's provider type. Before the
// capability check, this path type-asserted its way down to the driver and
// came back with a hard error, failing an allocation that should simply
// have gone un-isolated.
func TestManager_CreateFromPool_NonAdvertisingAgentAllocatesWithNoSegments(t *testing.T) {
	ctx := context.Background()
	agent := &stubIsolatingAgent{
		info: agentsdk.AgentInfo{
			ID:        "agent-1",
			Providers: []providersdk.Type{"hyperv"},
			// Advertises nothing: the driver behind this agent cannot
			// isolate, even though the agent interface says it can be asked.
			NetworkIsolatingProviders: nil,
		},
		createSegmentRef: "must-not-be-created",
		// The error shape EmbeddedAgent.CreateSegment really returns when
		// its driver doesn't implement providersdk.NetworkIsolator. Set so
		// a regression reproduces C1's ACTUAL symptom -- the allocation
		// failing outright -- rather than only the weaker "a segment got
		// recorded" proxy for it.
		createSegmentErr: errors.New(`agent "agent-1": provider "hyperv" does not support network isolation`),
	}
	m, st := isolationFixture(t, agent, "res-1", "res-2")

	sb, err := m.CreateFromPool(ctx, "pool-a", 2, "test-sandbox", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool must succeed for a non-isolating agent, got: %v", err)
	}
	if len(sb.NetworkSegments) != 0 {
		t.Fatalf("NetworkSegments = %+v, want none recorded", sb.NetworkSegments)
	}
	if agent.createSegmentCall != 0 || agent.attachCall != 0 {
		t.Fatalf("agent was called (create=%d attach=%d); the capability check must short-circuit before any agent call",
			agent.createSegmentCall, agent.attachCall)
	}

	persisted, err := st.GetSandbox(ctx, sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if len(persisted.NetworkSegments) != 0 {
		t.Fatalf("persisted NetworkSegments = %+v, want none", persisted.NetworkSegments)
	}
	if len(persisted.Resources) != 2 {
		t.Fatalf("persisted Resources = %v, want both resources allocated", persisted.Resources)
	}
}

// The same chain with an advertising agent must still isolate -- otherwise
// the fix above could "pass" by disabling isolation everywhere.
func TestManager_CreateFromPool_AdvertisingAgentStillCreatesSegment(t *testing.T) {
	ctx := context.Background()
	agent := &stubIsolatingAgent{
		info: agentsdk.AgentInfo{
			ID:                        "agent-1",
			Providers:                 []providersdk.Type{"hyperv"},
			NetworkIsolatingProviders: []providersdk.Type{"hyperv"},
		},
		createSegmentRef: "boxy-sb-sb-1",
	}
	m, _ := isolationFixture(t, agent, "res-1", "res-2")

	sb, err := m.CreateFromPool(ctx, "pool-a", 2, "test-sandbox", model.SandboxPolicies{})
	if err != nil {
		t.Fatalf("CreateFromPool: %v", err)
	}
	if agent.createSegmentCall != 1 || agent.attachCall != 2 {
		t.Fatalf("create=%d attach=%d, want create=1 attach=2", agent.createSegmentCall, agent.attachCall)
	}
	if len(sb.NetworkSegments) != 1 || sb.NetworkSegments[0].Ref != "boxy-sb-sb-1" {
		t.Fatalf("NetworkSegments = %+v, want one entry for boxy-sb-sb-1", sb.NetworkSegments)
	}
}

// AddFromPoolWithPackages runs the same ensureNetworkSegment path through a
// second, separate loop; the skip must hold there too.
func TestManager_AddFromPool_NonAdvertisingAgentAllocatesWithNoSegments(t *testing.T) {
	ctx := context.Background()
	agent := &stubIsolatingAgent{
		info:             agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"hyperv"}},
		createSegmentRef: "must-not-be-created",
	}
	m, st := isolationFixture(t, agent, "res-1")
	if err := st.CreateSandbox(ctx, model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	sb, err := m.AddFromPool(ctx, "sb-1", "pool-a", 1)
	if err != nil {
		t.Fatalf("AddFromPool must succeed for a non-isolating agent, got: %v", err)
	}
	if len(sb.NetworkSegments) != 0 || agent.createSegmentCall != 0 {
		t.Fatalf("segments = %+v, createSegment calls = %d; want neither", sb.NetworkSegments, agent.createSegmentCall)
	}
}

// I4: a segment that was really created must be recorded before anything
// else can fail. Here AttachToSegment fails right after CreateSegment
// succeeded -- previously the in-memory append was discarded with the
// returning stack frame, leaving a live host object (vSwitch/NAT/network)
// with no record anywhere.
func TestManager_CreateFromPool_PersistsSegmentBeforeAttach(t *testing.T) {
	ctx := context.Background()
	agent := &stubIsolatingAgent{
		info: agentsdk.AgentInfo{
			ID:                        "agent-1",
			Providers:                 []providersdk.Type{"hyperv"},
			NetworkIsolatingProviders: []providersdk.Type{"hyperv"},
		},
		createSegmentRef: "boxy-sb-sb-1",
		attachErr:        errors.New("Connect-VMNetworkAdapter failed"),
	}
	m, st := isolationFixture(t, agent, "res-1")

	_, err := m.CreateFromPool(ctx, "pool-a", 1, "test-sandbox", model.SandboxPolicies{})
	if err == nil {
		t.Fatal("expected the attach failure to surface as a hard error")
	}

	sandboxes, err := st.ListSandboxes(ctx)
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(sandboxes) != 1 {
		t.Fatalf("got %d sandboxes, want the one created before the failure", len(sandboxes))
	}
	segments := sandboxes[0].NetworkSegments
	if len(segments) != 1 || segments[0].Ref != "boxy-sb-sb-1" || segments[0].AgentID != "agent-1" {
		t.Fatalf("persisted NetworkSegments = %+v; the created segment must be recorded even though the later attach failed", segments)
	}
}

// I5: a segment whose agent is permanently gone must not block its
// sandbox's deletion -- nor, since Reconcile returns on the first
// cleanupSandbox error, every later sandbox in the same tick.
func TestDeletionReconciler_SkipsSegmentWhoseAgentIsGone(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	for _, sb := range []model.Sandbox{
		{
			ID:              "sb-1",
			Status:          model.SandboxStatusDeleting,
			NetworkSegments: []model.NetworkSegment{{AgentID: "long-gone-agent", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"}},
		},
		{ID: "sb-2", Status: model.SandboxStatusDeleting},
	} {
		if err := st.CreateSandbox(ctx, sb); err != nil {
			t.Fatalf("CreateSandbox: %v", err)
		}
	}

	// A real AgentProvisioner over an empty registry produces exactly the
	// sentinel the reconciler has to recognize.
	ap := &boxypool.AgentProvisioner{Registry: boxypool.NewAgentRegistry()}
	r := NewDeletionReconciler(st, &provisionerBackedDestroyer{ap: ap})

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile must not fail on a segment whose agent is gone: %v", err)
	}
	for _, id := range []model.SandboxID{"sb-1", "sb-2"} {
		if _, err := st.GetSandbox(ctx, id); err == nil {
			t.Fatalf("sandbox %q should have been deleted", id)
		}
	}
}

// The combined re-review's residual gap (b): an agent that is still
// registered but no longer advertises isolation for this segment's provider
// type -- reconfigured or downgraded between allocation and deletion -- must
// also be skipped rather than blocking deletion. Without the advertisement
// check in AgentProvisioner.DestroySegment this reproduced I5's stall through
// a different door.
func TestDeletionReconciler_SkipsSegmentWhoseAgentNoLongerAdvertisesIsolation(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	for _, sb := range []model.Sandbox{
		{
			ID:              "sb-1",
			Status:          model.SandboxStatusDeleting,
			NetworkSegments: []model.NetworkSegment{{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"}},
		},
		{ID: "sb-2", Status: model.SandboxStatusDeleting},
	} {
		if err := st.CreateSandbox(ctx, sb); err != nil {
			t.Fatalf("CreateSandbox: %v", err)
		}
	}

	// Registered and reachable, but advertising no isolation for hyperv any
	// more -- unlike TestDeletionReconciler_SkipsSegmentWhoseAgentIsGone,
	// where the agent isn't in the registry at all.
	agent := &stubIsolatingAgent{info: agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"hyperv"}}}
	registry := boxypool.NewAgentRegistry()
	if err := registry.Register(agent); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := NewDeletionReconciler(st, &provisionerBackedDestroyer{ap: &boxypool.AgentProvisioner{Registry: registry}})

	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile must not fail on an agent that no longer advertises isolation: %v", err)
	}
	if agent.destroySegmentCall != 0 {
		t.Fatalf("agent was asked to destroy a segment (%d calls) despite advertising no isolation support", agent.destroySegmentCall)
	}
	for _, id := range []model.SandboxID{"sb-1", "sb-2"} {
		if _, err := st.GetSandbox(ctx, id); err == nil {
			t.Fatalf("sandbox %q should have been deleted", id)
		}
	}
}

// A genuine DestroySegment failure stays a hard error and still blocks
// deletion -- the skip above must be narrow.
func TestDeletionReconciler_RealSegmentFailureStillBlocksDeletion(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	// Advertises hyperv: this agent really can isolate, so reaching its
	// DestroySegment (and failing there) is the behavior under test, not the
	// advertisement skip above.
	agent := &stubIsolatingAgent{info: agentsdk.AgentInfo{
		ID:                        "agent-1",
		Providers:                 []providersdk.Type{"hyperv"},
		NetworkIsolatingProviders: []providersdk.Type{"hyperv"},
	}}
	if err := st.CreateSandbox(ctx, model.Sandbox{
		ID:              "sb-1",
		Status:          model.SandboxStatusDeleting,
		NetworkSegments: []model.NetworkSegment{{AgentID: "agent-1", ProviderType: "hyperv", Ref: "boxy-sb-sb-1"}},
	}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	registry := boxypool.NewAgentRegistry()
	if err := registry.Register(&failingDestroySegmentAgent{stubIsolatingAgent: agent}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := NewDeletionReconciler(st, &provisionerBackedDestroyer{ap: &boxypool.AgentProvisioner{Registry: registry}})

	if err := r.Reconcile(ctx); err == nil {
		t.Fatal("expected a real segment teardown failure to block deletion")
	}
	if _, err := st.GetSandbox(ctx, "sb-1"); err != nil {
		t.Fatalf("sandbox must survive a failed teardown: %v", err)
	}
}

type failingDestroySegmentAgent struct{ *stubIsolatingAgent }

func (f *failingDestroySegmentAgent) DestroySegment(context.Context, providersdk.Type, providersdk.SegmentRef) error {
	return errors.New("vswitch still has connected adapters")
}

// provisionerBackedDestroyer is the plain-string SegmentDestroyer shim that
// pool.Manager provides in production (see its DestroySegment doc comment);
// reproduced here so these tests can drive a real AgentProvisioner without
// standing up a whole pool.Manager.
type provisionerBackedDestroyer struct{ ap *boxypool.AgentProvisioner }

func (p *provisionerBackedDestroyer) DestroyResource(context.Context, model.Resource) error {
	return nil
}

func (p *provisionerBackedDestroyer) DestroySegment(ctx context.Context, agentID string, providerType string, ref string) error {
	return p.ap.DestroySegment(ctx, agentID, providersdk.Type(providerType), providersdk.SegmentRef(ref))
}

var _ SegmentDestroyer = (*provisionerBackedDestroyer)(nil)

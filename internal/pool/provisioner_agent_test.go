package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

var (
	errAgentDeleteFailed = errors.New("agent delete failed")
	errPersonalizeFailed = errors.New("personalize failed")
)

// mockAgent is a minimal test double for agentsdk.Agent.
type mockAgent struct {
	info           agentsdk.AgentInfo
	createCalls    []mockCreateCall
	deleteCalls    []mockDeleteCall
	allocateCalls  []mockAllocateCall
	nextResourceID string
	createErr      error
	deleteErr      error
	allocateResult map[string]any
	personalized   *providersdk.GuestPersonalizationResult
	personalizeErr error
}

type mockCreateCall struct {
	driverType providersdk.Type
	cfg        any
}

type mockDeleteCall struct {
	driverType providersdk.Type
	id         string
}

type mockAllocateCall struct {
	driverType providersdk.Type
	id         string
}

func newMockAgent(providers ...providersdk.Type) *mockAgent {
	return &mockAgent{
		info: agentsdk.AgentInfo{
			ID:        "mock-agent",
			Name:      "Mock Agent",
			Providers: providers,
		},
		nextResourceID: "mock-resource-1",
	}
}

// registryWith builds an AgentRegistry with each given agent registered and
// available, for tests that don't care about registry construction itself.
func registryWith(t *testing.T, agents ...agentsdk.Agent) *AgentRegistry {
	t.Helper()
	r := NewAgentRegistry()
	for _, a := range agents {
		if err := r.Register(a); err != nil {
			t.Fatalf("register agent: %v", err)
		}
	}
	return r
}

func (m *mockAgent) Info() agentsdk.AgentInfo {
	return m.info
}

func (m *mockAgent) Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error) {
	m.createCalls = append(m.createCalls, mockCreateCall{driverType: provider, cfg: cfg})
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &providersdk.Resource{
		ID:             m.nextResourceID,
		ConnectionInfo: map[string]string{"test": "value"},
	}, nil
}

func (m *mockAgent) Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}

func (m *mockAgent) Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}

func (m *mockAgent) Delete(ctx context.Context, provider providersdk.Type, id string) error {
	m.deleteCalls = append(m.deleteCalls, mockDeleteCall{driverType: provider, id: id})
	return m.deleteErr
}

func (m *mockAgent) Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error) {
	m.allocateCalls = append(m.allocateCalls, mockAllocateCall{driverType: provider, id: id})
	return m.allocateResult, nil
}

func (m *mockAgent) PersonalizeGuest(ctx context.Context, provider providersdk.Type, id string) (*providersdk.GuestPersonalizationResult, error) {
	if m.personalizeErr != nil {
		return nil, m.personalizeErr
	}
	return m.personalized, nil
}

func TestAgentProvisioner_Provision(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("docker"))
	now := time.Now().UTC()

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"test-pool": {
				Name:   "test-pool",
				Type:   "docker",
				Config: map[string]any{"image": "alpine:latest"},
			},
		},
		Providers: map[string]providersdk.Instance{},
		Now:       func() time.Time { return now },
	}

	pool := model.Pool{
		Name: "test-pool",
		Inventory: model.ResourceCollection{
			ExpectedType:    "container",
			ExpectedProfile: "default",
		},
	}

	res, err := provisioner.Provision(context.Background(), pool)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if res.ID != "mock-resource-1" {
		t.Errorf("expected resource ID mock-resource-1, got %q", res.ID)
	}
	if res.State != model.ResourceStateReady {
		t.Errorf("expected state Ready, got %v", res.State)
	}
	if len(mockAgent.createCalls) != 1 {
		t.Errorf("expected 1 create call, got %d", len(mockAgent.createCalls))
	} else {
		call := mockAgent.createCalls[0]
		if call.driverType != "docker" {
			t.Errorf("expected driver type docker, got %q", call.driverType)
		}
	}
}

func TestAgentProvisioner_Destroy(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("docker"))

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"test-pool": {
				Name: "test-pool",
				Type: "docker",
			},
		},
		Providers: map[string]providersdk.Instance{},
	}

	pool := model.Pool{Name: "test-pool"}
	res := model.Resource{ID: "test-resource-id", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}

	err := provisioner.Destroy(context.Background(), pool, res)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if len(mockAgent.deleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(mockAgent.deleteCalls))
	} else if mockAgent.deleteCalls[0].id != "test-resource-id" || mockAgent.deleteCalls[0].driverType != "docker" {
		t.Errorf("delete call = %+v, want docker/test-resource-id", mockAgent.deleteCalls[0])
	}
}

func TestAgentProvisioner_Destroy_RejectsEmptyIDBeforeAgentCall(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("docker"))
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"test-pool": {Name: "test-pool", Type: "docker"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	err := provisioner.Destroy(context.Background(), model.Pool{Name: "test-pool"}, model.Resource{})
	if err == nil {
		t.Fatal("Destroy error = nil, want empty id error")
	}
	if len(mockAgent.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v, want none", mockAgent.deleteCalls)
	}
}

func TestAgentProvisioner_Destroy_SurfacesAgentDeleteFailure(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("docker"))
	mockAgent.deleteErr = errAgentDeleteFailed
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"test-pool": {Name: "test-pool", Type: "docker"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "test-resource-id", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}
	err := provisioner.Destroy(context.Background(), model.Pool{Name: "test-pool"}, res)
	if err == nil {
		t.Fatal("Destroy error = nil, want agent delete failure")
	}
	if len(mockAgent.deleteCalls) != 1 {
		t.Fatalf("deleteCalls = %v, want one delete attempt", mockAgent.deleteCalls)
	}
}

func TestAgentProvisioner_Allocate_PrefersTypedGuestPersonalization(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	mockAgent.allocateResult = map[string]any{"legacy": "path"}
	mockAgent.personalized = &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{
			Properties: map[string]string{"access": "winrm", "host": "10.0.0.5"},
		},
	}

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {
				Name: "vm-pool",
				Type: "hyperv",
			},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}
	got, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got["access"] != "winrm" {
		t.Fatalf("access = %v, want winrm", got["access"])
	}
	if _, ok := got["legacy"]; ok {
		t.Fatal("expected typed guest personalization to bypass legacy allocate result")
	}
}

func TestAgentProvisioner_Allocate_FallsBackWhenPersonalizationReturnsNil(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	mockAgent.allocateResult = map[string]any{"legacy": "path"}
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "vm", Provider: "hyperv-local"},
		},
		Providers: map[string]providersdk.Instance{
			"hyperv-local": {Name: "hyperv-local", Type: "hyperv"},
		},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}
	got, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got["legacy"] != "path" {
		t.Fatalf("Allocate result = %+v, want legacy fallback result", got)
	}
	if len(mockAgent.allocateCalls) != 1 || mockAgent.allocateCalls[0].driverType != "hyperv" || mockAgent.allocateCalls[0].id != "vm-1" {
		t.Fatalf("allocateCalls = %+v, want hyperv/vm-1 fallback", mockAgent.allocateCalls)
	}
}

func TestAgentProvisioner_Allocate_SurfacesPersonalizationFailure(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	mockAgent.personalizeErr = errPersonalizeFailed
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}
	if _, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res); err == nil {
		t.Fatal("Allocate error = nil, want personalization failure")
	}
	if len(mockAgent.allocateCalls) != 0 {
		t.Fatalf("allocateCalls = %+v, want no fallback after personalization failure", mockAgent.allocateCalls)
	}
}

// TestAgentProvisioner_DestroyAndAllocateRouteToCreatingAgent guards against
// the misrouting bug found during design review: once two agents advertise
// the same provider type, Destroy/Allocate must go back to the exact agent
// that created the resource (via Provider.AgentID), never re-resolve by
// type — which could silently pick the *other*, wrong agent.
func TestAgentProvisioner_DestroyAndAllocateRouteToCreatingAgent(t *testing.T) {
	agentA := newMockAgent(providersdk.Type("hyperv"))
	agentA.info.ID = "agent-a"
	agentB := newMockAgent(providersdk.Type("hyperv"))
	agentB.info.ID = "agent-b"

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, agentA, agentB),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	// The resource was created through agent A.
	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{Name: "hyperv", AgentID: agentA.info.ID}}

	if err := provisioner.Destroy(context.Background(), model.Pool{Name: "vm-pool"}, res); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(agentA.deleteCalls) != 1 {
		t.Fatalf("expected agent A (the creator) to receive the delete call, got %d calls", len(agentA.deleteCalls))
	}
	if len(agentB.deleteCalls) != 0 {
		t.Fatalf("agent B (not the creator) must never receive a delete call for this resource, got %d calls", len(agentB.deleteCalls))
	}

	if _, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(agentA.allocateCalls) != 1 {
		t.Fatalf("expected agent A (the creator) to receive the allocate call, got %d calls", len(agentA.allocateCalls))
	}
	if len(agentB.allocateCalls) != 0 {
		t.Fatalf("agent B (not the creator) must never receive an allocate call for this resource, got %d calls", len(agentB.allocateCalls))
	}
}

// TestAgentProvisioner_DestroyFailsClearlyWhenCreatingAgentGone proves the
// fix fails loudly rather than silently substituting a different agent
// when the resource's original agent is no longer registered (e.g.
// disconnected or revoked).
func TestAgentProvisioner_DestroyFailsClearlyWhenCreatingAgentGone(t *testing.T) {
	agentB := newMockAgent(providersdk.Type("hyperv"))
	agentB.info.ID = "agent-b"

	provisioner := &AgentProvisioner{
		// Only agent-b is registered; the resource was created by an agent
		// ("agent-a") that is no longer present.
		Registry: registryWith(t, agentB),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-a"}}
	if err := provisioner.Destroy(context.Background(), model.Pool{Name: "vm-pool"}, res); err == nil {
		t.Fatal("expected Destroy to fail clearly when the creating agent is gone, not silently substitute another agent")
	}
	if len(agentB.deleteCalls) != 0 {
		t.Fatalf("agent-b must never receive a delete call meant for agent-a's resource, got %d calls", len(agentB.deleteCalls))
	}
}

func TestAgentProvisioner_DriverTypeForPool_Docker(t *testing.T) {
	provisioner := &AgentProvisioner{
		Specs:     map[model.PoolName]boxyconfig.PoolSpec{},
		Providers: map[string]providersdk.Instance{},
	}

	tests := []struct {
		name     string
		spec     boxyconfig.PoolSpec
		expected providersdk.Type
	}{
		{
			name:     "type docker",
			spec:     boxyconfig.PoolSpec{Type: "docker"},
			expected: "docker",
		},
		{
			name:     "type container",
			spec:     boxyconfig.PoolSpec{Type: "container"},
			expected: "docker",
		},
		{
			name:     "empty type",
			spec:     boxyconfig.PoolSpec{Type: ""},
			expected: "docker",
		},
		{
			name:     "custom type",
			spec:     boxyconfig.PoolSpec{Type: "hyperv"},
			expected: "hyperv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provisioner.driverTypeForPool(tt.spec)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestAgentProvisioner_DriverTypeForPool_ExplicitProvider(t *testing.T) {
	provisioner := &AgentProvisioner{
		Specs: map[model.PoolName]boxyconfig.PoolSpec{},
		Providers: map[string]providersdk.Instance{
			"custom-docker": {
				Name: "custom-docker",
				Type: "docker",
			},
		},
	}

	// Provider field references a known instance
	spec := boxyconfig.PoolSpec{
		Type:     "container",
		Provider: "custom-docker",
	}
	got := provisioner.driverTypeForPool(spec)
	if got != "docker" {
		t.Errorf("expected docker, got %q", got)
	}

	// Provider field is a direct type name (not in Providers map)
	spec2 := boxyconfig.PoolSpec{
		Type:     "vm",
		Provider: "hyperv",
	}
	got2 := provisioner.driverTypeForPool(spec2)
	if got2 != "hyperv" {
		t.Errorf("expected hyperv, got %q", got2)
	}
}

func TestAgentProvisioner_UnknownPool(t *testing.T) {
	provisioner := &AgentProvisioner{
		Registry:  registryWith(t, newMockAgent()),
		Specs:     map[model.PoolName]boxyconfig.PoolSpec{},
		Providers: map[string]providersdk.Instance{},
	}

	pool := model.Pool{Name: "unknown-pool"}
	_, err := provisioner.Provision(context.Background(), pool)
	if err == nil {
		t.Fatal("expected error for unknown pool")
	}
	if _, err := provisioner.Allocate(context.Background(), pool, model.Resource{ID: "res-1"}); err == nil {
		t.Fatal("expected allocate error for unknown pool")
	}
	if err := provisioner.Destroy(context.Background(), pool, model.Resource{ID: "res-1"}); err == nil {
		t.Fatal("expected destroy error for unknown pool")
	}
}

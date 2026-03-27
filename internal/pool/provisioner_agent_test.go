package pool

import (
	"context"
	"testing"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// mockAgent is a minimal test double for agentsdk.Agent.
type mockAgent struct {
	info           agentsdk.AgentInfo
	createCalls    []mockCreateCall
	deleteCalls    []string
	nextResourceID string
	createErr      error
	deleteErr      error
}

type mockCreateCall struct {
	driverType providersdk.Type
	cfg        any
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
	m.deleteCalls = append(m.deleteCalls, id)
	return m.deleteErr
}

func (m *mockAgent) Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error) {
	return nil, nil
}

func TestAgentProvisioner_Provision(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("docker"))
	now := time.Now().UTC()

	provisioner := &AgentProvisioner{
		Agent: mockAgent,
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
		Agent: mockAgent,
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"test-pool": {
				Name: "test-pool",
				Type: "docker",
			},
		},
		Providers: map[string]providersdk.Instance{},
	}

	pool := model.Pool{Name: "test-pool"}
	res := model.Resource{ID: "test-resource-id"}

	err := provisioner.Destroy(context.Background(), pool, res)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if len(mockAgent.deleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(mockAgent.deleteCalls))
	} else if mockAgent.deleteCalls[0] != "test-resource-id" {
		t.Errorf("expected delete call for test-resource-id, got %q", mockAgent.deleteCalls[0])
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
		Agent:     newMockAgent(),
		Specs:     map[model.PoolName]boxyconfig.PoolSpec{},
		Providers: map[string]providersdk.Instance{},
	}

	pool := model.Pool{Name: "unknown-pool"}
	_, err := provisioner.Provision(context.Background(), pool)
	if err == nil {
		t.Fatal("expected error for unknown pool")
	}
}

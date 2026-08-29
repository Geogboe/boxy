package pool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
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

	// createEntered and createGate let a test observe that Create has been
	// called (createEntered closes) and hold it there until the test is
	// ready to let it proceed (closing createGate) — used to probe
	// AgentProvisioner.ProvisionLocked's lock scope against a concurrent
	// ReconcileAgent sweep. Both nil is the default, non-blocking behavior
	// every other test relies on.
	createEntered chan struct{}
	createGate    chan struct{}
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
	if m.createEntered != nil {
		close(m.createEntered)
	}
	if m.createGate != nil {
		<-m.createGate
	}
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

// nonPersonalizingAgent implements only the base agentsdk.Agent methods —
// unlike mockAgent, it does NOT define PersonalizeGuest, so a type assertion
// to agentsdk.GuestPersonalizingAgent against it fails. This is the double
// SupportsGuestPersonalization's "not supported" branch needs: mockAgent
// always implements the capability (it defines PersonalizeGuest
// unconditionally), so it can only exercise the "supported" branch.
type nonPersonalizingAgent struct {
	info agentsdk.AgentInfo
}

func (a *nonPersonalizingAgent) Info() agentsdk.AgentInfo { return a.info }
func (a *nonPersonalizingAgent) Create(context.Context, providersdk.Type, any) (*providersdk.Resource, error) {
	return nil, nil
}
func (a *nonPersonalizingAgent) Read(context.Context, providersdk.Type, string) (*providersdk.ResourceStatus, error) {
	return nil, nil
}
func (a *nonPersonalizingAgent) Update(context.Context, providersdk.Type, string, providersdk.Operation) (*providersdk.Result, error) {
	return nil, nil
}
func (a *nonPersonalizingAgent) Delete(context.Context, providersdk.Type, string) error { return nil }
func (a *nonPersonalizingAgent) Allocate(context.Context, providersdk.Type, string) (map[string]any, error) {
	return nil, nil
}

// fakeAgentStream is a minimal, no-network implementation of
// boxyagentv1.AgentTransportService_ConnectServer, used to drive a real
// agentsdk.RemoteAgent through its wire path (rather than a mockAgent
// double) in the tests below.
type fakeAgentStream struct {
	ctx    context.Context
	recvCh chan *boxyagentv1.AgentMessage
	sentCh chan *boxyagentv1.ServerMessage
}

func newFakeAgentStream() *fakeAgentStream {
	return &fakeAgentStream{
		ctx:    context.Background(),
		recvCh: make(chan *boxyagentv1.AgentMessage, 16),
		sentCh: make(chan *boxyagentv1.ServerMessage, 16),
	}
}

func (f *fakeAgentStream) Send(m *boxyagentv1.ServerMessage) error {
	f.sentCh <- m
	return nil
}

func (f *fakeAgentStream) Recv() (*boxyagentv1.AgentMessage, error) {
	m, ok := <-f.recvCh
	if !ok {
		return nil, io.EOF
	}
	return m, nil
}

func (f *fakeAgentStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeAgentStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeAgentStream) SetTrailer(metadata.MD)       {}
func (f *fakeAgentStream) Context() context.Context     { return f.ctx }
func (f *fakeAgentStream) SendMsg(m any) error          { return nil }
func (f *fakeAgentStream) RecvMsg(m any) error          { return nil }

func (f *fakeAgentStream) feedResult(res *boxyagentv1.CommandResult) {
	f.recvCh <- &boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{Result: res}}
}

// recvCommand waits for the next ServerMessage carrying a Command and
// returns it, failing the test if none arrives within the timeout.
func recvCommand(t *testing.T, sentCh <-chan *boxyagentv1.ServerMessage) *boxyagentv1.Command {
	t.Helper()
	select {
	case msg := <-sentCh:
		cmd := msg.GetCommand()
		if cmd == nil {
			t.Fatalf("expected a Command, got %#v", msg)
		}
		return cmd
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a Command to be sent")
		return nil
	}
}

// TestAgentProvisioner_Allocate_RemoteAgentPrefersTypedGuestPersonalization
// exercises the same "typed personalization preferred" behavior as
// TestAgentProvisioner_Allocate_PrefersTypedGuestPersonalization, but end to
// end through a real agentsdk.RemoteAgent wired to a fake gRPC stream,
// rather than through mockAgent, proving the wire path (proto
// Command_PersonalizeGuest / CommandResult_PersonalizeGuest) actually
// carries typed properties through to AgentProvisioner.Allocate.
func TestAgentProvisioner_Allocate_RemoteAgentPrefersTypedGuestPersonalization(t *testing.T) {
	stream := newFakeAgentStream()
	remote := agentsdk.NewRemoteAgent(agentsdk.AgentInfo{ID: "remote-1", Providers: []providersdk.Type{"hyperv"}}, stream)
	go func() { _ = remote.Serve() }()

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, remote),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: "remote-1"}}

	type allocResult struct {
		props map[string]any
		err   error
	}
	resultCh := make(chan allocResult, 1)
	go func() {
		allocation, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res)
		resultCh <- allocResult{allocation.Properties, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	if cmd.GetPersonalizeGuest() == nil {
		t.Fatalf("expected a PersonalizeGuestCommand, got %#v", cmd)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{
			Properties: map[string]string{"access": "winrm", "host": "192.0.2.5"},
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Allocate: %v", r.err)
		}
		if r.props["access"] != "winrm" || r.props["host"] != "192.0.2.5" {
			t.Fatalf("Allocate result = %+v, want typed personalization properties", r.props)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Allocate to return")
	}
}

// TestAgentProvisioner_Allocate_RemoteAgentFallsBackWhenPersonalizationUnsupported
// exercises the same "falls back to generic Allocate" behavior as
// TestAgentProvisioner_Allocate_FallsBackWhenPersonalizationReturnsNil, but
// through a real agentsdk.RemoteAgent: the remote driver's executeCommand
// answers PersonalizeGuest with an empty (non-error) PersonalizeGuestResult,
// proving that collapses to nil, nil at the RemoteAgent boundary and
// AgentProvisioner.Allocate falls all the way back to a real wire-path
// AllocateCommand/AllocateResult round trip.
func TestAgentProvisioner_Allocate_RemoteAgentFallsBackWhenPersonalizationUnsupported(t *testing.T) {
	stream := newFakeAgentStream()
	remote := agentsdk.NewRemoteAgent(agentsdk.AgentInfo{ID: "remote-1", Providers: []providersdk.Type{"hyperv"}}, stream)
	go func() { _ = remote.Serve() }()

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, remote),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: "remote-1"}}

	type allocResult struct {
		props map[string]any
		err   error
	}
	resultCh := make(chan allocResult, 1)
	go func() {
		allocation, err := provisioner.Allocate(context.Background(), model.Pool{Name: "vm-pool"}, res)
		resultCh <- allocResult{allocation.Properties, err}
	}()

	personalizeCmd := recvCommand(t, stream.sentCh)
	if personalizeCmd.GetPersonalizeGuest() == nil {
		t.Fatalf("expected a PersonalizeGuestCommand, got %#v", personalizeCmd)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: personalizeCmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{}},
	})

	allocateCmd := recvCommand(t, stream.sentCh)
	if allocateCmd.GetAllocate() == nil {
		t.Fatalf("expected a fallback AllocateCommand, got %#v", allocateCmd)
	}
	propsJSON, err := json.Marshal(map[string]any{"legacy": "path"})
	if err != nil {
		t.Fatalf("marshal fallback properties: %v", err)
	}
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: allocateCmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_Allocate{Allocate: &boxyagentv1.AllocateResult{PropertiesJson: propsJSON}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Allocate: %v", r.err)
		}
		if r.props["legacy"] != "path" {
			t.Fatalf("Allocate result = %+v, want legacy fallback result", r.props)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Allocate to return")
	}
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

func TestAgentProvisioner_ProvisionQuarantinesOrphanedResource(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	mockAgent.info.ID = "agent-a"
	mockAgent.createErr = &providersdk.OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res, err := provisioner.Provision(context.Background(), model.Pool{
		Name:      "vm-pool",
		Inventory: model.ResourceCollection{ExpectedType: model.ResourceTypeContainer, ExpectedProfile: "alpine"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.ID != "guid-1" {
		t.Errorf("ID = %q, want %q", res.ID, "guid-1")
	}
	if res.OriginPool != "vm-pool" {
		t.Errorf("OriginPool = %q, want %q", res.OriginPool, "vm-pool")
	}
	if res.State != model.ResourceStateError {
		t.Errorf("State = %q, want %q", res.State, model.ResourceStateError)
	}
	if res.Properties["quarantine_reason"] != "remove-vm failed" {
		t.Errorf("quarantine_reason = %v, want %q", res.Properties["quarantine_reason"], "remove-vm failed")
	}
	if res.Provider.AgentID != "agent-a" {
		t.Errorf("Provider.AgentID = %q, want %q", res.Provider.AgentID, "agent-a")
	}
}

func TestAgentProvisioner_ProvisionPlainErrorWithoutOrphan(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	mockAgent.createErr = errors.New("boom")

	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}

	res, err := provisioner.Provision(context.Background(), model.Pool{Name: "vm-pool"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.ID != "" {
		t.Errorf("expected zero-value Resource for a non-orphan failure, got %+v", res)
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
			Properties: map[string]string{"access": "winrm", "host": "192.0.2.5"},
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
	if got.Properties["access"] != "winrm" {
		t.Fatalf("access = %v, want winrm", got.Properties["access"])
	}
	if _, ok := got.Properties["legacy"]; ok {
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
	if got.Properties["legacy"] != "path" {
		t.Fatalf("Allocate result = %+v, want legacy fallback result", got)
	}
	if len(mockAgent.allocateCalls) != 1 || mockAgent.allocateCalls[0].driverType != "hyperv" || mockAgent.allocateCalls[0].id != "vm-1" {
		t.Fatalf("allocateCalls = %+v, want hyperv/vm-1 fallback", mockAgent.allocateCalls)
	}
}

// TestAgentProvisioner_SupportsGuestPersonalization_TrueForCapableAgent and
// its _False counterpart guard the fix for a real bug: admission used to
// call PersonalizeGuestForPool (a live, irreversible guest credential
// rotation) before confirming a secret backend existed to store the
// result. SupportsGuestPersonalization must answer correctly, and without
// itself triggering any rotation, so admission can check it — and a secret
// backend — first. See internal/pool/admission.go's Handle and ADR-0010.
func TestAgentProvisioner_SupportsGuestPersonalization_TrueForCapableAgent(t *testing.T) {
	mockAgent := newMockAgent(providersdk.Type("hyperv"))
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, mockAgent),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "hyperv"},
		},
		Providers: map[string]providersdk.Instance{},
	}
	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{AgentID: mockAgent.info.ID}}

	supports, err := provisioner.SupportsGuestPersonalization(context.Background(), model.Pool{Name: "vm-pool"}, res)
	if err != nil {
		t.Fatalf("SupportsGuestPersonalization: %v", err)
	}
	if !supports {
		t.Fatal("supports = false, want true for an agent implementing GuestPersonalizingAgent")
	}
	if mockAgent.personalized != nil || len(mockAgent.createCalls) != 0 {
		t.Fatal("SupportsGuestPersonalization must not perform any personalization side effect")
	}
}

func TestAgentProvisioner_SupportsGuestPersonalization_FalseForPlainAgent(t *testing.T) {
	plain := &nonPersonalizingAgent{info: agentsdk.AgentInfo{ID: "plain-agent", Providers: []providersdk.Type{"docker"}}}
	provisioner := &AgentProvisioner{
		Registry: registryWith(t, plain),
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"web-pool": {Name: "web-pool", Type: "docker"},
		},
		Providers: map[string]providersdk.Instance{},
	}
	res := model.Resource{ID: "res-1", Provider: model.ProviderRef{AgentID: "plain-agent"}}

	supports, err := provisioner.SupportsGuestPersonalization(context.Background(), model.Pool{Name: "web-pool"}, res)
	if err != nil {
		t.Fatalf("SupportsGuestPersonalization: %v", err)
	}
	if supports {
		t.Fatal("supports = true, want false for an agent not implementing GuestPersonalizingAgent")
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

func TestAgentProvisioner_CompatibleWithPool(t *testing.T) {
	provisioner := &AgentProvisioner{
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"apps": {Type: "vm", Provider: "hyperv"},
		},
	}
	p := model.Pool{Name: "apps"}

	for name, resourceProvider := range map[string]string{
		"matching provider": "hyperv",
		"different provider": "docker",
		"missing provider":   "",
	} {
		t.Run(name, func(t *testing.T) {
			res := model.Resource{Provider: model.ProviderRef{Name: resourceProvider}}
			got := provisioner.CompatibleWithPool(p, res)
			want := name == "matching provider"
			if got != want {
				t.Fatalf("CompatibleWithPool() = %t, want %t", got, want)
			}
		})
	}
}

// TestAgentProvisioner_ForceOrphan_SucceedsWhenAgentGone proves ForceOrphan
// succeeds (never contacts any agent) once the owning agent is entirely
// absent from the registry — the state Revoke leaves behind after
// Deregister.
func TestAgentProvisioner_ForceOrphan_SucceedsWhenAgentGone(t *testing.T) {
	other := newMockAgent(providersdk.Type("hyperv"))
	other.info.ID = "agent-b"

	provisioner := &AgentProvisioner{
		// Only agent-b is registered; the resource belongs to "agent-a",
		// which has already been deregistered (e.g. via Revoke).
		Registry: registryWith(t, other),
	}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-a"}}
	if err := provisioner.ForceOrphan(context.Background(), res); err != nil {
		t.Fatalf("ForceOrphan: %v", err)
	}
	if len(other.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v, want none — ForceOrphan must never contact any agent", other.deleteCalls)
	}
}

// TestAgentProvisioner_ForceOrphan_RefusedWhenAgentStillRegistered is the
// safety-critical case: an agent that's merely unavailable (heartbeat-miss
// marked, but not deregistered) must still refuse force-orphan, since it may
// reconnect and the resource may still be alive on it.
func TestAgentProvisioner_ForceOrphan_RefusedWhenAgentStillRegistered(t *testing.T) {
	agent := newMockAgent(providersdk.Type("hyperv"))
	agent.info.ID = "agent-a"
	registry := registryWith(t, agent)
	registry.SetAvailable("agent-a", false)

	provisioner := &AgentProvisioner{Registry: registry}

	res := model.Resource{ID: "vm-1", Provider: model.ProviderRef{Name: "hyperv", AgentID: "agent-a"}}
	if err := provisioner.ForceOrphan(context.Background(), res); err == nil {
		t.Fatal("ForceOrphan error = nil, want refusal because the agent is still registered")
	}
	if len(agent.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v, want none — ForceOrphan must never contact the agent even when refusing", agent.deleteCalls)
	}
}

// TestAgentProvisioner_ProvisionLocked_HoldsLockThroughCreate proves the
// extended fix for the ghost-orphan race: the per-agent ProvisionLocker
// lock must be held from before Create is even called, not just around the
// subsequent store write. A version of this fix that only wrapped the
// store write (the version this branch shipped with initially) would let
// this test's competing LockProvisioning acquire while Create is still
// blocked below — which is exactly the window a fast ResourceLister driver
// like devfactory (returns from Create in well under a millisecond) could
// hit in practice, since List() visibility happens inside Create, before
// any caller regains control to acquire anything. See LockedProvisioner's
// and AgentRegistry.LockProvisioning's doc comments.
func TestAgentProvisioner_ProvisionLocked_HoldsLockThroughCreate(t *testing.T) {
	agent := &mockAgent{
		info:           agentsdk.AgentInfo{ID: "agent-1", Providers: []providersdk.Type{"devfactory"}},
		nextResourceID: "res-1",
		createEntered:  make(chan struct{}),
		createGate:     make(chan struct{}),
	}
	registry := registryWith(t, agent)
	provisioner := &AgentProvisioner{
		Registry: registry,
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"vm-pool": {Name: "vm-pool", Type: "devfactory"},
		},
		Providers: map[string]providersdk.Instance{},
	}
	pool := model.Pool{Name: "vm-pool", Inventory: model.ResourceCollection{ExpectedType: "container", ExpectedProfile: "default"}}

	provisionDone := make(chan error, 1)
	go func() {
		_, _, err := provisioner.ProvisionLocked(context.Background(), pool, nil)
		provisionDone <- err
	}()

	select {
	case <-agent.createEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ProvisionLocked to enter Create")
	}

	competingAcquire := make(chan struct{})
	go func() {
		release := registry.LockProvisioning("agent-1")
		close(competingAcquire)
		release()
	}()

	select {
	case <-competingAcquire:
		t.Fatal("a competing LockProvisioning(\"agent-1\") acquired while ProvisionLocked was still blocked inside Create — the lock must be held from before Create returns, not just around the store write after")
	case <-time.After(50 * time.Millisecond):
		// expected: still blocked
	}

	close(agent.createGate) // let Create return, finishing ProvisionLocked and releasing the lock

	if err := <-provisionDone; err != nil {
		t.Fatalf("ProvisionLocked: %v", err)
	}
	select {
	case <-competingAcquire:
		// expected: unblocks once ProvisionLocked releases its lock
	case <-time.After(time.Second):
		t.Fatal("competing LockProvisioning(\"agent-1\") never acquired after ProvisionLocked finished")
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

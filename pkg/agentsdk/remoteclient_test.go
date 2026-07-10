package agentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

type fakeDriver struct {
	providerType providersdk.Type

	createErr error
	createRes *providersdk.Resource

	readErr error
	readRes *providersdk.ResourceStatus

	updateErr error
	updateRes *providersdk.Result

	deleteErr error

	allocateErr error
	allocateRes map[string]any
}

func (d *fakeDriver) Type() providersdk.Type { return d.providerType }

func (d *fakeDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	return d.createRes, d.createErr
}

func (d *fakeDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	return d.readRes, d.readErr
}

func (d *fakeDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return d.updateRes, d.updateErr
}

func (d *fakeDriver) Delete(ctx context.Context, id string) error {
	return d.deleteErr
}

func (d *fakeDriver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	return d.allocateRes, d.allocateErr
}

// fakeListingDriver adds providersdk.ResourceLister on top of fakeDriver, so
// tests can exercise both the "driver supports List" and "driver doesn't"
// paths through executeCommand — the latter using plain *fakeDriver, which
// deliberately has no List method.
type fakeListingDriver struct {
	*fakeDriver
	listErr error
	listRes []providersdk.ResourceStatus
}

func (d *fakeListingDriver) List(ctx context.Context) ([]providersdk.ResourceStatus, error) {
	return d.listRes, d.listErr
}

func TestExecuteCommand(t *testing.T) {
	drivers := DriverSet{
		"docker": &fakeDriver{
			providerType: "docker",
			createRes:    &providersdk.Resource{ID: "c1", ConnectionInfo: map[string]string{"host": "10.0.0.1"}},
			readRes:      &providersdk.ResourceStatus{ID: "c1", State: "running"},
			updateRes:    &providersdk.Result{Outputs: map[string]string{"stdout": "ok"}},
			allocateRes:  map[string]any{"ssh_user": "ubuntu"},
		},
	}

	t.Run("create success", func(t *testing.T) {
		cfgJSON, _ := json.Marshal(map[string]any{"image": "kali"})
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-1",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Create{Create: &boxyagentv1.CreateCommand{ConfigJson: cfgJSON}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		if got := res.GetResource().GetId(); got != "c1" {
			t.Fatalf("expected resource id c1, got %q", got)
		}
		if got := res.GetResource().GetConnectionInfo()["host"]; got != "10.0.0.1" {
			t.Fatalf("expected connection info to round-trip, got %q", got)
		}
	})

	t.Run("read success", func(t *testing.T) {
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-2",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Read{Read: &boxyagentv1.ReadCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetStatus().GetState() != "running" {
			t.Fatalf("expected state running, got %q", res.GetStatus().GetState())
		}
	})

	t.Run("update success", func(t *testing.T) {
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-3",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Update{Update: &boxyagentv1.UpdateCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if got := res.GetOperation().GetOutputs()["stdout"]; got != "ok" {
			t.Fatalf("expected stdout ok, got %q", got)
		}
	})

	t.Run("delete success", func(t *testing.T) {
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-4",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Delete{Delete: &boxyagentv1.DeleteCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetDeleted() == nil {
			t.Fatalf("expected a Deleted outcome, got %#v", res.GetOutcome())
		}
	})

	t.Run("allocate success round-trips non-string values", func(t *testing.T) {
		drivers := DriverSet{"docker": &fakeDriver{providerType: "docker", allocateRes: map[string]any{"port": float64(2222), "user": "ubuntu"}}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-5",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Allocate{Allocate: &boxyagentv1.AllocateCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		var props map[string]any
		if err := json.Unmarshal(res.GetAllocate().GetPropertiesJson(), &props); err != nil {
			t.Fatalf("unmarshal properties: %v", err)
		}
		if props["port"] != float64(2222) {
			t.Fatalf("expected numeric property to round-trip, got %#v", props["port"])
		}
	})

	t.Run("list success", func(t *testing.T) {
		drivers := DriverSet{"docker": &fakeListingDriver{
			fakeDriver: &fakeDriver{providerType: "docker"},
			listRes: []providersdk.ResourceStatus{
				{ID: "c1", State: "running"},
				{ID: "c2", State: "exited"},
			},
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-8",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_List{List: &boxyagentv1.ListCommand{}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		got := res.GetList().GetResources()
		if len(got) != 2 || got[0].GetId() != "c1" || got[1].GetId() != "c2" {
			t.Fatalf("unexpected list result: %#v", got)
		}
	})

	t.Run("list unsupported by driver errors", func(t *testing.T) {
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-9",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_List{List: &boxyagentv1.ListCommand{}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil {
			t.Fatal("expected an error result for a driver without ResourceLister")
		}
	})

	t.Run("unknown provider type errors", func(t *testing.T) {
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-6",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_Read{Read: &boxyagentv1.ReadCommand{ResourceId: "x"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil {
			t.Fatal("expected an error result for an unavailable provider type")
		}
	})

	t.Run("driver error is surfaced as AgentError", func(t *testing.T) {
		drivers := DriverSet{"docker": &fakeDriver{providerType: "docker", deleteErr: errors.New("boom")}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-7",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Delete{Delete: &boxyagentv1.DeleteCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil || res.GetError().GetMessage() != "boom" {
			t.Fatalf("expected AgentError{boom}, got %#v", res.GetOutcome())
		}
	})
}

// fakeClientStream is a hand-rolled AgentTransportService_ConnectClient for
// exercising RunSession's register/heartbeat/dispatch loop without a real
// network connection.
type fakeClientStream struct {
	ctx     context.Context
	recvCh  chan *boxyagentv1.ServerMessage
	sentCh  chan *boxyagentv1.AgentMessage
	recvErr error
}

func newFakeClientStream() *fakeClientStream {
	return &fakeClientStream{
		ctx:    context.Background(),
		recvCh: make(chan *boxyagentv1.ServerMessage, 16),
		sentCh: make(chan *boxyagentv1.AgentMessage, 16),
	}
}

func (f *fakeClientStream) Send(m *boxyagentv1.AgentMessage) error {
	f.sentCh <- m
	return nil
}

func (f *fakeClientStream) Recv() (*boxyagentv1.ServerMessage, error) {
	m, ok := <-f.recvCh
	if !ok {
		if f.recvErr != nil {
			return nil, f.recvErr
		}
		return nil, io.EOF
	}
	return m, nil
}

// close simulates the underlying connection ending, unblocking any pending
// Recv the way a real dropped gRPC stream would (context cancellation alone
// does not stop a blocked channel receive).
func (f *fakeClientStream) close() {
	close(f.recvCh)
}

func (f *fakeClientStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakeClientStream) Trailer() metadata.MD         { return nil }
func (f *fakeClientStream) CloseSend() error             { return nil }
func (f *fakeClientStream) Context() context.Context     { return f.ctx }
func (f *fakeClientStream) SendMsg(m any) error          { return nil }
func (f *fakeClientStream) RecvMsg(m any) error          { return nil }

func TestRunSession_RegistersAndDispatchesCommand(t *testing.T) {
	stream := newFakeClientStream()
	drivers := DriverSet{"docker": &fakeDriver{providerType: "docker", createRes: &providersdk.Resource{ID: "c1"}}}

	registeredCh := make(chan *boxyagentv1.RegisterResponse, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessionErrCh := make(chan error, 1)
	go func() {
		sessionErrCh <- RunSession(ctx, stream, RemoteClientConfig{
			AgentName:         "test-agent",
			Token:             "tok-123",
			ProviderTypes:     []providersdk.Type{"docker"},
			Drivers:           drivers,
			HeartbeatInterval: 20 * time.Millisecond,
			OnRegistered:      func(reg *boxyagentv1.RegisterResponse) { registeredCh <- reg },
		})
	}()

	// First frame from the agent must be the RegisterRequest.
	var registerSent *boxyagentv1.AgentMessage
	select {
	case registerSent = <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RegisterRequest")
	}
	reg := registerSent.GetRegister()
	if reg == nil || reg.GetRegistrationToken() != "tok-123" {
		t.Fatalf("expected RegisterRequest with the configured token, got %#v", registerSent)
	}

	// Server acks registration.
	stream.recvCh <- &boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_Registered{Registered: &boxyagentv1.RegisterResponse{AgentId: "agent-xyz"}},
	}

	select {
	case reg := <-registeredCh:
		if reg.GetAgentId() != "agent-xyz" {
			t.Fatalf("expected agent id agent-xyz, got %q", reg.GetAgentId())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnRegistered callback")
	}

	// Server pushes a command; the agent should dispatch it to the driver
	// and send a matching CommandResult back.
	stream.recvCh <- &boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_Command{Command: &boxyagentv1.Command{
			CommandId:    "cmd-1",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_Create{Create: &boxyagentv1.CreateCommand{}},
		}},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-stream.sentCh:
			if result := msg.GetResult(); result != nil {
				if result.GetCommandId() != "cmd-1" {
					t.Fatalf("expected result for cmd-1, got %q", result.GetCommandId())
				}
				if result.GetResource().GetId() != "c1" {
					t.Fatalf("expected resource id c1, got %q", result.GetResource().GetId())
				}
				// End the session: closing the stream unblocks dispatchCommands'
				// blocked Recv (context cancellation alone would not, just as
				// with a real dropped connection), and cancel stops the
				// heartbeat sender.
				stream.close()
				cancel()
				select {
				case <-sessionErrCh:
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for RunSession to return after closing the stream")
				}
				return
			}
			// else: a heartbeat frame, keep waiting for the command result
		case <-deadline:
			t.Fatal("timed out waiting for CommandResult")
		}
	}
}

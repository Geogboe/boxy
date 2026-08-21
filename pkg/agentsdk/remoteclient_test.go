package agentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

const testRegistrationToken = "${BOXY_TEST_REGISTRATION_TOKEN}"

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

// fakePersonalizingDriver adds providersdk.GuestPersonalizer on top of
// fakeDriver, so tests can exercise both the "driver supports
// PersonalizeGuest" and "driver doesn't" paths through executeCommand — the
// latter using plain *fakeDriver, which deliberately has no
// PersonalizeGuest method.
type fakePersonalizingDriver struct {
	*fakeDriver
	personalizeErr error
	personalizeRes *providersdk.GuestPersonalizationResult
}

func (d *fakePersonalizingDriver) PersonalizeGuest(ctx context.Context, id string) (*providersdk.GuestPersonalizationResult, error) {
	return d.personalizeRes, d.personalizeErr
}

// fakeAvailabilityDriver adds providersdk.AvailabilityReporter on top of
// fakeDriver, so tests can exercise both "driver has a reporter" and
// "driver doesn't" (plain *fakeDriver, which deliberately has no
// Availability method) through sampleAvailability.
type fakeAvailabilityDriver struct {
	*fakeDriver
	avail    *providersdk.ResourceAvailability
	availErr error
}

func (d *fakeAvailabilityDriver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	return d.avail, d.availErr
}

// fakeHangingAvailabilityDriver's Availability ignores ctx entirely and
// blocks forever — the worst case sampleAvailability's hard deadline must
// still bound, since not every third-party AvailabilityReporter
// implementation is guaranteed to respect context cancellation.
type fakeHangingAvailabilityDriver struct {
	*fakeDriver
}

func (d *fakeHangingAvailabilityDriver) Availability(ctx context.Context) (*providersdk.ResourceAvailability, error) {
	select {}
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

type fakeStreamingDriver struct {
	*fakeDriver
}

func (d *fakeStreamingDriver) UpdateStream(ctx context.Context, id string, op providersdk.Operation, sink eventstream.Sink) (*providersdk.Result, error) {
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte("live")}); err != nil {
		return nil, err
	}
	return &providersdk.Result{Outputs: map[string]string{"exit_code": "0"}}, nil
}

func TestExecuteStreamingCommandForwardsDataAndCompletion(t *testing.T) {
	var sent []*boxyagentv1.AgentMessage
	driver := &fakeStreamingDriver{fakeDriver: &fakeDriver{providerType: "docker"}}
	operation, _ := json.Marshal(&providersdk.ExecOperation{Command: []string{"echo", "hi"}})
	cmd := &boxyagentv1.Command{
		CommandId:    "stream-1",
		ProviderType: "docker",
		Op: &boxyagentv1.Command_Update{Update: &boxyagentv1.UpdateCommand{
			ResourceId:    "resource-1",
			OperationJson: operation,
			Stream:        true,
		}},
	}
	executeStreamingCommand(context.Background(), DriverSet{"docker": driver}, cmd, func(message *boxyagentv1.AgentMessage) error {
		sent = append(sent, message)
		return nil
	})
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want data and completion", len(sent))
	}
	if got := sent[0].GetResult().GetOperationStream().GetData(); string(got) != "live" {
		t.Fatalf("stream data = %q, want live", got)
	}
	if !sent[1].GetResult().GetOperationStream().GetComplete() {
		t.Fatal("last message is not a completion")
	}
	if got := sent[1].GetResult().GetOperationStream().GetAttributes()["exit_code"]; got != "0" {
		t.Fatalf("exit_code = %q, want 0", got)
	}
}

// TestRemoteStreamSink_ConcurrentSendDoesNotRaceOrDoubleComplete guards a gap
// a GitHub Copilot review on this PR caught: remoteStreamSink.completed was
// a bare bool read/written from Send/complete with no synchronization. Every
// concrete driver in this codebase happens to fan its stream events through
// a single consumer goroutine, so the race was never reachable via the
// drivers actually wired up here — but providersdk.StreamingDriver (and
// eventstream.Sink) is a public interface, and nothing in Sink's contract
// promises Send is only ever called from one goroutine at a time (e.g. a
// driver with independent concurrent stdout/stderr forwarders, the same
// shape pkg/vmsdk/ssh.go's ExecStream uses internally before serializing
// down to one consumer). This test drives Send concurrently the way such a
// driver could, run under `go test -race` in CI (not available on this
// dev machine's windows/arm64 host) to catch the data race directly; it
// also asserts the logical invariant a race could otherwise violate: at
// most one Complete event ever reaches the wire.
func TestRemoteStreamSink_ConcurrentSendDoesNotRaceOrDoubleComplete(t *testing.T) {
	var mu sync.Mutex
	var sent []*boxyagentv1.AgentMessage
	sink := &remoteStreamSink{
		commandID: "cmd-1",
		send: func(message *boxyagentv1.AgentMessage) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, message)
			return nil
		},
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			if i%4 == 0 {
				_ = sink.Send(context.Background(), eventstream.Event{Kind: eventstream.Complete, Completion: &eventstream.Completion{Attributes: map[string]string{"exit_code": "0"}}})
				return
			}
			_ = sink.Send(context.Background(), eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte("x")})
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	completes := 0
	for _, m := range sent {
		if m.GetResult().GetOperationStream().GetComplete() {
			completes++
		}
	}
	if completes != 1 {
		t.Fatalf("got %d Complete events sent, want exactly 1", completes)
	}
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
			createRes:    &providersdk.Resource{ID: "c1", ConnectionInfo: map[string]string{"host": "192.0.2.1"}},
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
		if got := res.GetResource().GetConnectionInfo()["host"]; got != "192.0.2.1" {
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

	t.Run("personalize guest success round-trips typed properties", func(t *testing.T) {
		credential := &providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"username":"admin","password":"rotated"}`)}
		drivers := DriverSet{"hyperv": &fakePersonalizingDriver{
			fakeDriver: &fakeDriver{providerType: "hyperv"},
			personalizeRes: &providersdk.GuestPersonalizationResult{
				AccessDetails:       providersdk.GuestAccessDetails{Properties: map[string]string{"access": "ssh", "host": "192.0.2.9"}},
				EphemeralCredential: credential,
			},
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-10",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestCommand{ResourceId: "vm-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		got := res.GetPersonalizeGuest().GetProperties()
		if got["access"] != "ssh" || got["host"] != "192.0.2.9" {
			t.Fatalf("expected typed properties to round-trip, got %#v", got)
		}
		var gotCredential providersdk.GuestCredential
		if err := json.Unmarshal(res.GetPersonalizeGuest().GetGuestCredentialJson(), &gotCredential); err != nil {
			t.Fatalf("unmarshal guest credential: %v", err)
		}
		if gotCredential.Kind != credential.Kind || string(gotCredential.Data) != string(credential.Data) {
			t.Fatalf("guest credential = %+v, want %+v", gotCredential, *credential)
		}
	})

	t.Run("personalize guest unsupported by driver returns non-error empty result", func(t *testing.T) {
		// This is the regression the whole design depends on: a driver that
		// doesn't implement providersdk.GuestPersonalizer must not produce
		// an AgentError, or internal/pool's fallback-to-Allocate path never
		// runs and every remote pool with a non-personalizing driver turns
		// a previously-successful allocation into a hard failure.
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-11",
			ProviderType: "docker",
			Op:           &boxyagentv1.Command_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestCommand{ResourceId: "c1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("expected no error for an unsupported driver, got %s", res.GetError().GetMessage())
		}
		pg := res.GetPersonalizeGuest()
		if pg == nil {
			t.Fatal("expected a non-nil PersonalizeGuestResult outcome")
		}
		if len(pg.GetProperties()) != 0 {
			t.Fatalf("expected zero-length properties, got %#v", pg.GetProperties())
		}
	})

	t.Run("personalize guest driver returns nil result yields non-error empty result", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakePersonalizingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-12",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestCommand{ResourceId: "vm-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() != nil {
			t.Fatalf("unexpected error: %s", res.GetError().GetMessage())
		}
		pg := res.GetPersonalizeGuest()
		if pg == nil || len(pg.GetProperties()) != 0 {
			t.Fatalf("expected non-nil, zero-length PersonalizeGuestResult, got %#v", pg)
		}
	})

	t.Run("personalize guest driver error is surfaced as AgentError", func(t *testing.T) {
		drivers := DriverSet{"hyperv": &fakePersonalizingDriver{
			fakeDriver:     &fakeDriver{providerType: "hyperv"},
			personalizeErr: errors.New("boom"),
		}}
		cmd := &boxyagentv1.Command{
			CommandId:    "cmd-13",
			ProviderType: "hyperv",
			Op:           &boxyagentv1.Command_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestCommand{ResourceId: "vm-1"}},
		}
		res := executeCommand(context.Background(), drivers, cmd)
		if res.GetError() == nil || res.GetError().GetMessage() != "boom" {
			t.Fatalf("expected AgentError{boom}, got %#v", res.GetOutcome())
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

// --- sampleAvailability ---

func TestSampleAvailability_CollectsPerProviderSkipsMissingReporter(t *testing.T) {
	drivers := DriverSet{
		"hyperv": &fakeAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, avail: &providersdk.ResourceAvailability{MemoryMB: 4096}},
		"docker": &fakeDriver{providerType: "docker"}, // no AvailabilityReporter
	}
	entries := sampleAvailability(context.Background(), drivers, time.Second, nil)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (docker has no reporter), got %#v", entries)
	}
	if entries[0].GetProviderType() != "hyperv" || entries[0].GetMemoryMb() != 4096 {
		t.Fatalf("unexpected entry: %#v", entries[0])
	}
}

func TestSampleAvailability_NoRemainingReportersReturnsNoEntries(t *testing.T) {
	drivers := DriverSet{"docker": &fakeDriver{providerType: "docker"}}
	entries := sampleAvailability(context.Background(), drivers, time.Second, nil)
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %#v", entries)
	}
}

// TestSampleAvailability_ReporterErrorOmitsEntry proves a reporter error
// never fails the whole sample — it just leaves that provider's entry out,
// consistent with providersdk.AvailabilityReporter being an optional,
// best-effort capability.
func TestSampleAvailability_ReporterErrorOmitsEntry(t *testing.T) {
	drivers := DriverSet{
		"hyperv": &fakeAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, availErr: fmt.Errorf("query failed")},
		"docker": &fakeAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "docker"}, avail: &providersdk.ResourceAvailability{MemoryMB: 2048}},
	}
	entries := sampleAvailability(context.Background(), drivers, time.Second, nil)
	if len(entries) != 1 || entries[0].GetProviderType() != "docker" {
		t.Fatalf("expected only the docker entry, got %#v", entries)
	}
}

// TestSampleAvailability_HungReporterBoundedByDeadline is the "cannot be
// indefinitely blocked" requirement's core test: a reporter that ignores
// its context and blocks forever must not stop sampleAvailability from
// returning within its configured deadline, and must not stop a healthy
// reporter's result from being reported alongside it.
func TestSampleAvailability_HungReporterBoundedByDeadline(t *testing.T) {
	drivers := DriverSet{
		"hyperv": &fakeHangingAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}},
		"docker": &fakeAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "docker"}, avail: &providersdk.ResourceAvailability{MemoryMB: 2048}},
	}

	done := make(chan []*boxyagentv1.ProviderAvailability, 1)
	go func() { done <- sampleAvailability(context.Background(), drivers, 30*time.Millisecond, nil) }()

	select {
	case entries := <-done:
		if len(entries) != 1 || entries[0].GetProviderType() != "docker" {
			t.Fatalf("expected only the docker entry, got %#v", entries)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sampleAvailability did not return despite a hung reporter and a bounded deadline")
	}
}

// TestRunSession_BlankAgentVersionFailsFastLocally covers a Copilot finding
// on PR #175: a caller that forgets to set AgentVersion should get an
// immediate, clear local error instead of a wasted round-trip to a server
// that will reject it anyway (with deliberately little detail, since the
// peer isn't authenticated at that point — see server.go's Connect).
func TestRunSession_BlankAgentVersionFailsFastLocally(t *testing.T) {
	stream := newFakeClientStream()

	err := RunSession(context.Background(), stream, RemoteClientConfig{
		AgentName:     "test-agent",
		Token:         testRegistrationToken,
		ProviderTypes: []providersdk.Type{"docker"},
		Drivers:       DriverSet{},
	})
	if err == nil {
		t.Fatal("expected an error for a blank AgentVersion")
	}
	// Pin the actual failure reason, not just "something errored" — the
	// config above is also missing other fields (e.g. no Drivers entries),
	// so a weaker assertion would pass even if the AgentVersion check were
	// deleted and some unrelated validation failed instead.
	if !strings.Contains(err.Error(), "AgentVersion") {
		t.Fatalf("expected error to mention AgentVersion, got: %v", err)
	}
	select {
	case sent := <-stream.sentCh:
		t.Fatalf("expected no message sent on the stream, got %#v", sent)
	default:
	}
}

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
			Token:             testRegistrationToken,
			AgentVersion:      "v-test",
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
	if reg == nil || reg.GetRegistrationToken() != testRegistrationToken {
		t.Fatalf("expected RegisterRequest with the configured token, got %#v", registerSent)
	}
	if reg.GetAgentVersion() != "v-test" {
		t.Fatalf("expected RegisterRequest.AgentVersion to be forwarded from config, got %q", reg.GetAgentVersion())
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

// TestRunSession_HeartbeatCarriesAvailability proves the end-to-end client
// wiring: a driver implementing providersdk.AvailabilityReporter shows up
// on the wire inside Heartbeat.availability, and a driver that doesn't
// (docker here) is simply absent, not a zero-value entry.
func TestRunSession_HeartbeatCarriesAvailability(t *testing.T) {
	stream := newFakeClientStream()
	drivers := DriverSet{
		"hyperv": &fakeAvailabilityDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, avail: &providersdk.ResourceAvailability{MemoryMB: 4096}},
		"docker": &fakeDriver{providerType: "docker"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessionErrCh := make(chan error, 1)
	go func() {
		sessionErrCh <- RunSession(ctx, stream, RemoteClientConfig{
			AgentName:         "boxy-test-agent",
			Token:             testRegistrationToken,
			AgentVersion:      "v-test",
			ProviderTypes:     []providersdk.Type{"hyperv", "docker"},
			Drivers:           drivers,
			HeartbeatInterval: 20 * time.Millisecond,
		})
	}()

	<-stream.sentCh // RegisterRequest
	stream.recvCh <- &boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_Registered{Registered: &boxyagentv1.RegisterResponse{AgentId: "agent-xyz"}},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-stream.sentCh:
			hb := msg.GetHeartbeat()
			if hb == nil {
				continue
			}
			if len(hb.GetAvailability()) != 1 {
				t.Fatalf("expected exactly 1 availability entry (docker has no reporter), got %#v", hb.GetAvailability())
			}
			entry := hb.GetAvailability()[0]
			if entry.GetProviderType() != "hyperv" || entry.GetMemoryMb() != 4096 {
				t.Fatalf("unexpected availability entry: %#v", entry)
			}
			stream.close()
			cancel()
			select {
			case <-sessionErrCh:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for RunSession to return after closing the stream")
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for a Heartbeat frame")
		}
	}
}

func TestErrorResult_ClassifiesTypedErrors(t *testing.T) {
	t.Run("capacity error", func(t *testing.T) {
		err := &providersdk.CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512}
		result := errorResult("cmd-1", err.Error(), err)
		ae := result.GetError()
		if ae.GetErrorType() != "capacity" {
			t.Errorf("error_type = %q, want %q", ae.GetErrorType(), "capacity")
		}
		var got providersdk.CapacityError
		if jerr := json.Unmarshal(ae.GetErrorDetailJson(), &got); jerr != nil {
			t.Fatalf("unmarshal detail: %v", jerr)
		}
		if got.RequestedMemoryMB != 2048 || got.AvailableMemoryMB != 512 {
			t.Errorf("detail = %+v, want original fields", got)
		}
	})

	t.Run("orphaned resource error", func(t *testing.T) {
		err := &providersdk.OrphanedResourceError{ID: "guid-1", CauseMessage: "remove-vm failed"}
		result := errorResult("cmd-2", err.Error(), err)
		ae := result.GetError()
		if ae.GetErrorType() != "orphaned_resource" {
			t.Errorf("error_type = %q, want %q", ae.GetErrorType(), "orphaned_resource")
		}
		var got providersdk.OrphanedResourceError
		if jerr := json.Unmarshal(ae.GetErrorDetailJson(), &got); jerr != nil {
			t.Fatalf("unmarshal detail: %v", jerr)
		}
		if got.ID != "guid-1" || got.CauseMessage != "remove-vm failed" {
			t.Errorf("detail = %+v, want original fields", got)
		}
	})

	t.Run("wrapped capacity error keeps its fields", func(t *testing.T) {
		inner := &providersdk.CapacityError{RequestedMemoryMB: 2048, AvailableMemoryMB: 512}
		wrapped := fmt.Errorf("create vm: %w", inner)
		ae := errorResult("cmd-4", wrapped.Error(), wrapped).GetError()
		if ae.GetErrorType() != "capacity" {
			t.Fatalf("error_type = %q, want %q", ae.GetErrorType(), "capacity")
		}
		var got providersdk.CapacityError
		if jerr := json.Unmarshal(ae.GetErrorDetailJson(), &got); jerr != nil {
			t.Fatalf("unmarshal detail: %v", jerr)
		}
		if got.RequestedMemoryMB != 2048 || got.AvailableMemoryMB != 512 {
			t.Errorf("detail = %+v, want original fields", got)
		}
	})

	t.Run("untyped error carries no error_type", func(t *testing.T) {
		result := errorResult("cmd-3", "boom", errors.New("boom"))
		if result.GetError().GetErrorType() != "" {
			t.Errorf("error_type = %q, want empty for an untyped error", result.GetError().GetErrorType())
		}
	})
}

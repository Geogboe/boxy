package agentsdk

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// fakeServerStream is a hand-rolled, no-network implementation of
// boxyagentv1.AgentTransportService_ConnectServer for testing RemoteAgent's
// command correlation and teardown behavior in isolation.
type fakeServerStream struct {
	ctx     context.Context
	recvCh  chan *boxyagentv1.AgentMessage
	sentCh  chan *boxyagentv1.ServerMessage
	recvErr error
}

func newFakeServerStream() *fakeServerStream {
	return &fakeServerStream{
		ctx:    context.Background(),
		recvCh: make(chan *boxyagentv1.AgentMessage, 16),
		sentCh: make(chan *boxyagentv1.ServerMessage, 16),
	}
}

func (f *fakeServerStream) Send(m *boxyagentv1.ServerMessage) error {
	f.sentCh <- m
	return nil
}

func (f *fakeServerStream) Recv() (*boxyagentv1.AgentMessage, error) {
	m, ok := <-f.recvCh
	if !ok {
		if f.recvErr != nil {
			return nil, f.recvErr
		}
		return nil, io.EOF
	}
	return m, nil
}

func (f *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeServerStream) SetTrailer(metadata.MD)       {}
func (f *fakeServerStream) Context() context.Context     { return f.ctx }
func (f *fakeServerStream) SendMsg(m any) error          { return nil }
func (f *fakeServerStream) RecvMsg(m any) error          { return nil }

// closeWith simulates the underlying connection ending: Recv will return
// err (or io.EOF if err is nil) once the channel drains.
func (f *fakeServerStream) closeWith(err error) {
	f.recvErr = err
	close(f.recvCh)
}

func (f *fakeServerStream) feedResult(res *boxyagentv1.CommandResult) {
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

func TestRemoteAgent_CreateRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.Resource
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.Create(context.Background(), "docker", map[string]any{"image": "kali"})
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	create := cmd.GetCreate()
	if create == nil {
		t.Fatalf("expected a CreateCommand, got %#v", cmd)
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_Resource{Resource: &boxyagentv1.ResourceResult{
			Id:             "container-123",
			ConnectionInfo: map[string]string{"host": "10.0.0.5"},
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("Create returned error: %v", r.err)
		}
		if r.res.ID != "container-123" {
			t.Fatalf("expected resource id container-123, got %q", r.res.ID)
		}
		if r.res.ConnectionInfo["host"] != "10.0.0.5" {
			t.Fatalf("expected connection info to round-trip, got %#v", r.res.ConnectionInfo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Create to return")
	}
}

func TestRemoteAgent_ListRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		statuses []providersdk.ResourceStatus
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		statuses, err := a.List(context.Background(), "docker")
		resultCh <- result{statuses, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	if cmd.GetList() == nil {
		t.Fatalf("expected a ListCommand, got %#v", cmd)
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_List{List: &boxyagentv1.ListResult{
			Resources: []*boxyagentv1.ResourceStatusResult{
				{Id: "container-1", State: "running"},
				{Id: "container-2", State: "exited"},
			},
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("List returned error: %v", r.err)
		}
		if len(r.statuses) != 2 {
			t.Fatalf("expected 2 statuses, got %d", len(r.statuses))
		}
		if r.statuses[0].ID != "container-1" || r.statuses[0].State != "running" {
			t.Fatalf("unexpected first status: %+v", r.statuses[0])
		}
		if r.statuses[1].ID != "container-2" || r.statuses[1].State != "exited" {
			t.Fatalf("unexpected second status: %+v", r.statuses[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for List to return")
	}
}

func TestRemoteAgent_ListErrorPropagates(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	resultCh := make(chan error, 1)
	go func() {
		_, err := a.List(context.Background(), "hyperv")
		resultCh <- err
	}()

	cmd := recvCommand(t, stream.sentCh)
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_Error{Error: &boxyagentv1.AgentError{Message: "list not supported by driver \"hyperv\""}},
	})

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for List to return")
	}
}

func TestRemoteAgent_ConcurrentCallsResolveDistinctWaiters(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	const n = 5
	type result struct {
		idx int
		res *providersdk.Resource
		err error
	}
	resultCh := make(chan result, n)
	for i := range n {
		go func(i int) {
			res, err := a.Create(context.Background(), "docker", map[string]any{"n": i})
			resultCh <- result{i, res, err}
		}(i)
	}

	// Collect all n sent commands before responding to any of them, then
	// respond in reverse order — proves correlation is by command_id, not
	// by send/response ordering.
	cmds := make([]*boxyagentv1.Command, 0, n)
	for range n {
		cmds = append(cmds, recvCommand(t, stream.sentCh))
	}
	for i := len(cmds) - 1; i >= 0; i-- {
		stream.feedResult(&boxyagentv1.CommandResult{
			CommandId: cmds[i].GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Resource{Resource: &boxyagentv1.ResourceResult{Id: cmds[i].GetCommandId()}},
		})
	}

	seen := make(map[string]bool, n)
	for range n {
		select {
		case r := <-resultCh:
			if r.err != nil {
				t.Fatalf("call %d returned error: %v", r.idx, r.err)
			}
			// Each result's resource ID was set to its own command_id, so
			// a caller receiving the wrong waiter's result would show up
			// as a duplicate or a value nobody else claims.
			if seen[r.res.ID] {
				t.Fatalf("command_id %s delivered to more than one waiter", r.res.ID)
			}
			seen[r.res.ID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent calls to resolve")
		}
	}
}

func TestRemoteAgent_ContextCancelCleansUpPendingEntry(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := a.Create(ctx, "docker", nil)
		errCh <- err
	}()

	recvCommand(t, stream.sentCh) // wait until the command is actually sent/pending
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled call to return")
	}

	// Poll briefly: cleanup happens just after the select fires, so allow
	// a short window for the goroutine to finish removing its entry.
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.Lock()
		n := len(a.pending)
		a.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending map still has %d entries after context cancellation", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoteAgent_StreamDropFailsAllPendingWaiters(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	const n = 3
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := a.Create(context.Background(), "docker", nil)
			errCh <- err
		}()
	}

	for range n {
		recvCommand(t, stream.sentCh)
	}

	stream.closeWith(io.EOF) // simulate the underlying connection dropping

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: at least one call blocked forever after stream drop")
	}

	for range n {
		if err := <-errCh; err == nil {
			t.Fatal("expected every pending call to fail after stream drop, got nil error")
		}
	}
}

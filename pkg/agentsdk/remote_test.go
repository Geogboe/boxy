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
	"github.com/Geogboe/boxy/pkg/eventstream"
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

func TestRemoteAgent_PersonalizeGuestRoundTrip(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.GuestPersonalizationResult
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.PersonalizeGuest(context.Background(), "hyperv", "vm-1")
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	personalize := cmd.GetPersonalizeGuest()
	if personalize == nil {
		t.Fatalf("expected a PersonalizeGuestCommand, got %#v", cmd)
	}
	if personalize.GetResourceId() != "vm-1" {
		t.Fatalf("expected resource id vm-1, got %q", personalize.GetResourceId())
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{
			Properties: map[string]string{"access": "ssh", "host": "10.0.0.5"},
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("PersonalizeGuest returned error: %v", r.err)
		}
		if r.res == nil {
			t.Fatal("expected a non-nil GuestPersonalizationResult")
		}
		if r.res.AccessDetails.Properties["access"] != "ssh" || r.res.AccessDetails.Properties["host"] != "10.0.0.5" {
			t.Fatalf("expected typed properties to round-trip, got %#v", r.res.AccessDetails.Properties)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PersonalizeGuest to return")
	}
}

// TestRemoteAgent_PersonalizeGuestUnsupportedReturnsNilNotError proves the
// empty-PersonalizeGuestResult-means-nil convention holds through
// RemoteAgent: a driver that doesn't implement providersdk.GuestPersonalizer
// produces an empty result on the wire (see executeCommand), which must
// collapse to nil, nil here — never an error — so
// internal/pool.AgentProvisioner.Allocate falls back to the generic
// Allocate path instead of failing the whole allocation.
func TestRemoteAgent_PersonalizeGuestUnsupportedReturnsNilNotError(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.GuestPersonalizationResult
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.PersonalizeGuest(context.Background(), "docker", "c1")
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome:   &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("expected no error for an unsupported driver, got %v", r.err)
		}
		if r.res != nil {
			t.Fatalf("expected a nil result for an unsupported driver, got %#v", r.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PersonalizeGuest to return")
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

// discardSink is a no-op eventstream.Sink used where a test only cares about
// UpdateStream's return value, not the events it forwards.
type discardSink struct{}

func (discardSink) Send(context.Context, eventstream.Event) error { return nil }

// recordingSink records every event it receives, in order, for tests that
// need to assert on the events UpdateStream forwards to its caller's sink.
type recordingSink struct {
	mu     sync.Mutex
	events []eventstream.Event
}

func (s *recordingSink) Send(_ context.Context, event eventstream.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingSink) snapshot() []eventstream.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]eventstream.Event(nil), s.events...)
}

// TestRemoteAgent_UpdateStreamForwardsDataAndCompletion exercises the
// server-side half of remote streaming end to end: two non-terminal
// OperationStreamEvents fed in over the fake stream must reach the caller's
// sink in order with their channel/payload intact, and a terminal event
// carrying attributes must both reach the sink as a Complete event and
// produce a matching *providersdk.Result from UpdateStream's return value —
// proving exit_code (and other attributes) survive the wire round trip.
func TestRemoteAgent_UpdateStreamForwardsDataAndCompletion(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	sink := &recordingSink{}
	type result struct {
		res *providersdk.Result
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.UpdateStream(context.Background(), "docker", "res-1", &providersdk.ExecOperation{Command: []string{"echo", "hi"}}, sink)
		resultCh <- result{res, err}
	}()

	cmd := recvCommand(t, stream.sentCh)
	update := cmd.GetUpdate()
	if update == nil || !update.GetStream() {
		t.Fatalf("expected a streaming UpdateCommand, got %#v", cmd)
	}

	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
			Channel: "stdout",
			Data:    []byte("hello "),
		}},
	})
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
			Channel: "stdout",
			Data:    []byte("world"),
		}},
	})
	stream.feedResult(&boxyagentv1.CommandResult{
		CommandId: cmd.GetCommandId(),
		Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
			Complete:   true,
			Attributes: map[string]string{"exit_code": "127"},
		}},
	})

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("UpdateStream returned error: %v", r.err)
		}
		if r.res == nil || r.res.Outputs["exit_code"] != "127" {
			t.Fatalf("expected exit_code 127 in result outputs, got %#v", r.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UpdateStream to return")
	}

	events := sink.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 events delivered to sink, got %d: %#v", len(events), events)
	}
	if events[0].Kind != eventstream.Data || string(events[0].Payload) != "hello " || events[0].Channel != "stdout" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Kind != eventstream.Data || string(events[1].Payload) != "world" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Kind != eventstream.Complete || events[2].Completion == nil || events[2].Completion.Attributes["exit_code"] != "127" {
		t.Fatalf("unexpected third (completion) event: %+v", events[2])
	}
}

// TestRemoteAgent_CloseDoesNotPanicSendOnStreamChannel guards against a
// TOCTOU race in deliver()/Close(): deliver() reads a channel reference out
// of streamPending, releases a.mu, and only then sends the result on it. A
// non-terminal OperationStreamEvent (data, not complete/error) leaves the
// entry in streamPending — since Close() is called for real from
// Server.Revoke on a goroutine other than the one running Serve(), it can
// land in exactly that gap: acquire a.mu, delete the entry, and close(ch)
// out from under deliver()'s pending send. Reproducing that interleaving by
// launching goroutines and hoping the scheduler cooperates is unreliable
// (the send after feedResult completes long before Close() can be
// scheduled), so this test drives the same sequence deliver() takes
// directly and deterministically instead.
func TestRemoteAgent_CloseDoesNotPanicSendOnStreamChannel(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)

	const cmdID = "cmd-1"
	waiter := &streamWaiter{ch: make(chan *boxyagentv1.CommandResult, 32), done: make(chan struct{})}
	a.mu.Lock()
	a.streamPending[cmdID] = waiter
	a.mu.Unlock()

	// Mirrors deliver()'s read of the waiter reference for a non-terminal
	// event: the entry stays in streamPending because only Complete/Error
	// events delete it.
	a.mu.Lock()
	got, ok := a.streamPending[cmdID]
	a.mu.Unlock()
	if !ok {
		t.Fatal("expected streamPending entry to still be present")
	}

	// Close() runs here — in the window deliver() leaves open between
	// reading the channel and sending on it, exactly what a concurrent
	// Revoke() can hit.
	a.Close()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close() left the stream channel in a state where a late send panics: %v", r)
		}
	}()
	got.ch <- &boxyagentv1.CommandResult{CommandId: cmdID}
}

// TestRemoteAgent_CloseUnblocksLiveUpdateStreamWaiter proves the behavioral
// guarantee Close() must still provide once it no longer closes individual
// streamPending channels (see TestRemoteAgent_CloseDoesNotPanicSendOnStreamChannel):
// a goroutine genuinely blocked inside UpdateStream, waiting on a
// mid-command stream, must still unblock with an error as soon as Close()
// runs — via the `case <-a.closed` select arm — rather than hanging until
// its context deadline.
func TestRemoteAgent_CloseUnblocksLiveUpdateStreamWaiter(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)
	go func() { _ = a.Serve() }()

	type result struct {
		res *providersdk.Result
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		res, err := a.UpdateStream(context.Background(), "docker", "res-1", &providersdk.ExecOperation{Command: []string{"true"}}, discardSink{})
		resultCh <- result{res, err}
	}()

	recvCommand(t, stream.sentCh) // wait until the streaming command is actually sent/pending

	a.Close()

	select {
	case r := <-resultCh:
		if r.err == nil {
			t.Fatal("expected UpdateStream to return an error once Close() runs, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: UpdateStream did not unblock after Close()")
	}
}

// TestRemoteAgent_DeliverStreamSendDoesNotBlockForeverOnFullBufferAfterClosed
// guards the other half of the deliver()/Close() TOCTOU fix (see
// TestRemoteAgent_CloseDoesNotPanicSendOnStreamChannel): deliver()'s send on
// a streamPending channel is a bare `streamCh <- result` with only a 32-slot
// buffer. If the waiter already gave up (via `case <-a.closed`, e.g. because
// Close() ran) but hasn't drained the buffer, and the buffer is full — a
// slow HTTP client can plausibly leave 32 unread events — that bare send
// blocks forever, and since deliver() runs inside Serve()'s single receive
// loop, the whole agent connection's Serve() goroutine never returns.
//
// close(a.closed) is used directly instead of a.Close() so streamPending
// keeps its entry (Close() would otherwise clear it before deliver() has a
// chance to reach its send step) — deliver() must still find the entry to
// exercise the code path under test.
func TestRemoteAgent_DeliverStreamSendDoesNotBlockForeverOnFullBufferAfterClosed(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)

	const cmdID = "cmd-1"
	waiter := &streamWaiter{ch: make(chan *boxyagentv1.CommandResult, 1), done: make(chan struct{})}
	a.mu.Lock()
	a.streamPending[cmdID] = waiter
	a.mu.Unlock()
	waiter.ch <- &boxyagentv1.CommandResult{CommandId: cmdID} // fill the buffer: nothing will ever drain it

	close(a.closed) // the waiter already gave up via `case <-a.closed`; waiter.done is deliberately left open here

	done := make(chan struct{})
	go func() {
		a.deliver(&boxyagentv1.CommandResult{
			CommandId: cmdID,
			Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
				Channel: "stdout",
				Data:    []byte("x"),
			}},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliver() blocked forever sending to a full streamPending channel whose waiter already gave up")
	}
}

// TestRemoteAgent_DeliverStreamSendDoesNotBlockForeverWhenWaiterDoneClosesWithConnectionHealthy
// guards the gap the code review on this PR caught in the fix above: a.closed
// only covers the whole *connection* closing. A single UpdateStream call can
// also give up on its own — its exec context expires (default 30s, max 5m,
// see internal/server/api_exec.go) or its sink errors — entirely
// independently of connection health, and a.closed is never closed in that
// case. Before streamWaiter.done existed, deliver() had no way to observe
// that and could block forever on a full buffer even though the connection
// was perfectly healthy — wedging Serve()'s single receive loop (and every
// other command routed to that agent) with no automatic recovery, only an
// operator-issued `boxy agent revoke`. Confirmed to actually hang against
// the pre-fix code (a plain `select { case ch<-: case <-a.closed: }`) before
// this test was written.
func TestRemoteAgent_DeliverStreamSendDoesNotBlockForeverWhenWaiterDoneClosesWithConnectionHealthy(t *testing.T) {
	stream := newFakeServerStream()
	a := NewRemoteAgent(AgentInfo{ID: "agent-1"}, stream)

	const cmdID = "cmd-1"
	waiter := &streamWaiter{ch: make(chan *boxyagentv1.CommandResult, 1), done: make(chan struct{})}
	a.mu.Lock()
	a.streamPending[cmdID] = waiter
	a.mu.Unlock()
	waiter.ch <- &boxyagentv1.CommandResult{CommandId: cmdID} // fill the buffer: nothing will ever drain it

	// This specific call gives up — e.g. its exec context expired — but the
	// connection itself stays up: a.closed is deliberately left open.
	close(waiter.done)

	done := make(chan struct{})
	go func() {
		a.deliver(&boxyagentv1.CommandResult{
			CommandId: cmdID,
			Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
				Channel: "stdout",
				Data:    []byte("x"),
			}},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliver() blocked forever sending to a full streamPending channel whose specific waiter gave up, even though the connection stayed healthy")
	}
}

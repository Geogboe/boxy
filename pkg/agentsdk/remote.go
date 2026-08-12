package agentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// RemoteAgent is the server-side proxy for one connected remote agent. It
// implements Agent by sending Commands down the agent's gRPC stream and
// correlating asynchronous CommandResults back to the caller via a
// command_id. See docs/adr/0005-remote-agent-transport-and-registration.md.
//
// One RemoteAgent instance corresponds to exactly one live stream. A
// reconnect from the same agent identity after a drop creates a *new*
// RemoteAgent (a fresh stream, fresh pending map) — callers holding a
// reference to the old instance will see every in-flight call fail once
// Close is called on it.
type RemoteAgent struct {
	info   AgentInfo
	stream boxyagentv1.AgentTransportService_ConnectServer

	mu            sync.Mutex
	pending       map[string]chan *boxyagentv1.CommandResult
	streamPending map[string]*streamWaiter

	// sendMu serializes Send calls: a single gRPC stream is not safe for
	// concurrent use by multiple goroutines on the send side.
	sendMu sync.Mutex

	// lastSeen is stored as UnixNano, not Unix seconds: whole-second
	// resolution is too coarse for heartbeat intervals under ~1s (common
	// in tests and for fast failure detection) and can make a just-arrived
	// heartbeat appear stale by up to a second due to truncation.
	lastSeen atomic.Int64

	closed    chan struct{}
	closeOnce sync.Once
}

// streamWaiter is one UpdateStream call's registration in streamPending.
// done is closed by that specific call's cleanup (deferred, so it runs on
// every exit path — a full result, ctx expiry, a.closed, or a sink error)
// and exists so deliver() can bound its send by "this particular waiter
// gave up" in addition to "the whole connection closed" (a.closed): the
// exec context's own deadline (default 30s, max 5m — see
// internal/server/api_exec.go) is entirely independent of the connection's
// health, so a.closed alone left deliver() with no way to notice a timed-out
// (but otherwise healthy) waiter and could block forever on a full ch.
type streamWaiter struct {
	ch   chan *boxyagentv1.CommandResult
	done chan struct{}
}

var _ GuestPersonalizingAgent = (*RemoteAgent)(nil)

// NewRemoteAgent wraps a server-side stream handle for one connected agent.
// The caller must run Serve in its own goroutine to pump incoming frames.
func NewRemoteAgent(info AgentInfo, stream boxyagentv1.AgentTransportService_ConnectServer) *RemoteAgent {
	a := &RemoteAgent{
		info:          info,
		stream:        stream,
		pending:       make(map[string]chan *boxyagentv1.CommandResult),
		streamPending: make(map[string]*streamWaiter),
		closed:        make(chan struct{}),
	}
	a.lastSeen.Store(time.Now().UnixNano())
	return a
}

func (a *RemoteAgent) Info() AgentInfo {
	return a.info
}

// LastSeen returns the time of the most recent Heartbeat (or connection
// start, if none has arrived yet).
func (a *RemoteAgent) LastSeen() time.Time {
	return time.Unix(0, a.lastSeen.Load())
}

// Serve reads AgentMessages off the stream until it ends for any reason,
// dispatching Heartbeats to LastSeen and CommandResults to pending callers.
// It must be run in its own goroutine, one per connection. When it returns,
// Close has already been called, failing every still-pending call.
func (a *RemoteAgent) Serve() error {
	defer a.Close()
	for {
		msg, err := a.stream.Recv()
		if err != nil {
			return err
		}
		switch payload := msg.GetPayload().(type) {
		case *boxyagentv1.AgentMessage_Heartbeat:
			a.lastSeen.Store(time.Now().UnixNano())
		case *boxyagentv1.AgentMessage_Result:
			a.deliver(payload.Result)
		default:
			// A RegisterRequest arriving again (or an empty payload) after
			// the connection is already established is a protocol
			// violation from a well-behaved agent, but not fatal to the
			// stream — ignore and keep serving.
		}
	}
}

// Close tears down this agent's view of the connection: every call
// currently blocked waiting on a CommandResult fails immediately rather
// than hanging until its context deadline. Safe to call multiple times.
//
// It deliberately does not close the individual per-command channels in
// pending/streamPending — only clears the maps. Closing a.closed already
// unblocks every call()/UpdateStream waiter via their `case <-a.closed`
// select arm, so closing the per-command channels too would be redundant,
// and for streamPending it's actively unsafe: deliver() reads a channel
// reference out of the map, releases a.mu, and only then sends on it, so a
// concurrent Close() (called for real from Server.Revoke on a different
// goroutine than the one running Serve()) could delete-and-close that same
// channel in the gap, panicking deliver()'s send with "send on closed
// channel". Leaving the channels open removes that race entirely: a late
// send from deliver() after Close() just lands in an unread buffer that's
// garbage collected once the waiter (already gone via a.closed) drops its
// reference.
//
// It also never closes a streamWaiter's done channel — that's exclusively
// UpdateStream's own deferred cleanup's job, exactly once per call. a.closed
// already gives deliver() a connection-teardown signal; closing done here
// too would risk a double-close panic if UpdateStream's own cleanup runs
// concurrently.
func (a *RemoteAgent) Close() {
	a.closeOnce.Do(func() {
		close(a.closed)
		a.mu.Lock()
		for id := range a.pending {
			delete(a.pending, id)
		}
		for id := range a.streamPending {
			delete(a.streamPending, id)
		}
		a.mu.Unlock()
	})
}

func (a *RemoteAgent) deliver(result *boxyagentv1.CommandResult) {
	a.mu.Lock()
	waiter, streamOK := a.streamPending[result.GetCommandId()]
	if streamOK {
		if result.GetOperationStream().GetComplete() || result.GetError() != nil {
			delete(a.streamPending, result.GetCommandId())
		}
	}
	ch, ok := a.pending[result.GetCommandId()]
	if ok {
		delete(a.pending, result.GetCommandId())
	}
	a.mu.Unlock()
	if streamOK {
		// A non-terminal event's specific waiter may already have given up
		// — not just via the whole connection closing (a.closed), but via
		// its own exec context expiring or its sink erroring, both entirely
		// independent of connection health (see streamWaiter's doc comment).
		// With the 32-slot buffer full and nothing left to drain it, a send
		// bound only by a.closed could block forever on a healthy
		// connection, and since deliver() runs inside Serve()'s single
		// receive loop, that would wedge the whole agent connection, not
		// just this one command. waiter.done — closed by UpdateStream's
		// deferred cleanup on every exit path — closes that gap.
		select {
		case waiter.ch <- result:
		case <-a.closed:
		case <-waiter.done:
		}
		return
	}
	if ok {
		ch <- result
	}
	// If no waiter is found, the caller already gave up (context cancelled
	// or the command_id is otherwise unknown) — the late result is dropped.
}

// call sends cmd down the stream and blocks until its correlated
// CommandResult arrives, ctx is done, or the connection closes.
func (a *RemoteAgent) call(ctx context.Context, cmd *boxyagentv1.Command) (*boxyagentv1.CommandResult, error) {
	cmd.CommandId = uuid.NewString()

	ch := make(chan *boxyagentv1.CommandResult, 1)
	a.mu.Lock()
	a.pending[cmd.CommandId] = ch
	a.mu.Unlock()

	cleanup := func() {
		a.mu.Lock()
		delete(a.pending, cmd.CommandId)
		a.mu.Unlock()
	}

	a.sendMu.Lock()
	err := a.stream.Send(&boxyagentv1.ServerMessage{
		Payload: &boxyagentv1.ServerMessage_Command{Command: cmd},
	})
	a.sendMu.Unlock()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("agent %q: send command: %w", a.info.ID, err)
	}

	select {
	case res, ok := <-ch:
		if !ok || res == nil {
			return nil, fmt.Errorf("agent %q: connection closed while waiting for command %s", a.info.ID, cmd.CommandId)
		}
		return res, nil
	case <-a.closed:
		cleanup()
		return nil, fmt.Errorf("agent %q: connection closed while waiting for command %s", a.info.ID, cmd.CommandId)
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	}
}

func (a *RemoteAgent) Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error) {
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: marshal create config: %w", a.info.ID, err)
	}
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_Create{Create: &boxyagentv1.CreateCommand{ConfigJson: cfgJSON}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	rr := res.GetResource()
	if rr == nil {
		return nil, fmt.Errorf("agent %q: unexpected result for create", a.info.ID)
	}
	return &providersdk.Resource{
		ID:             rr.GetId(),
		ConnectionInfo: rr.GetConnectionInfo(),
		Metadata:       rr.GetMetadata(),
	}, nil
}

func (a *RemoteAgent) Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_Read{Read: &boxyagentv1.ReadCommand{ResourceId: id}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	st := res.GetStatus()
	if st == nil {
		return nil, fmt.Errorf("agent %q: unexpected result for read", a.info.ID)
	}
	return &providersdk.ResourceStatus{ID: st.GetId(), State: st.GetState()}, nil
}

func (a *RemoteAgent) Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error) {
	opJSON, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("agent %q: marshal update operation: %w", a.info.ID, err)
	}
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_Update{Update: &boxyagentv1.UpdateCommand{ResourceId: id, OperationJson: opJSON}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	out := res.GetOperation()
	if out == nil {
		return nil, fmt.Errorf("agent %q: unexpected result for update", a.info.ID)
	}
	return &providersdk.Result{Outputs: out.GetOutputs()}, nil
}

func (a *RemoteAgent) UpdateStream(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation, sink eventstream.Sink) (*providersdk.Result, error) {
	if sink == nil {
		return nil, fmt.Errorf("agent %q: stream sink is required", a.info.ID)
	}
	opJSON, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("agent %q: marshal streaming update operation: %w", a.info.ID, err)
	}
	cmd := &boxyagentv1.Command{
		ProviderType: string(provider),
		Op: &boxyagentv1.Command_Update{Update: &boxyagentv1.UpdateCommand{
			ResourceId:    id,
			OperationJson: opJSON,
			Stream:        true,
		}},
	}
	cmd.CommandId = uuid.NewString()
	waiter := &streamWaiter{ch: make(chan *boxyagentv1.CommandResult, 32), done: make(chan struct{})}
	a.mu.Lock()
	a.streamPending[cmd.CommandId] = waiter
	a.mu.Unlock()
	cleanup := func() {
		a.mu.Lock()
		delete(a.streamPending, cmd.CommandId)
		a.mu.Unlock()
		// Closed last, and only here: this is the single deferred call for
		// this UpdateStream invocation, so it runs exactly once no matter
		// which return path is taken — deliver() relies on that (a second
		// close would panic exactly like the bug fixed two rounds ago).
		close(waiter.done)
	}
	defer cleanup()

	a.sendMu.Lock()
	err = a.stream.Send(&boxyagentv1.ServerMessage{Payload: &boxyagentv1.ServerMessage_Command{Command: cmd}})
	a.sendMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("agent %q: send streaming command: %w", a.info.ID, err)
	}

	for {
		select {
		case result, ok := <-waiter.ch:
			if !ok || result == nil {
				return nil, fmt.Errorf("agent %q: connection closed while streaming command %s", a.info.ID, cmd.CommandId)
			}
			if agentErr := result.GetError(); agentErr != nil {
				return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
			}
			if event := result.GetOperationStream(); event != nil {
				if event.GetComplete() {
					if event.GetError() != "" {
						return nil, fmt.Errorf("agent %q: %s", a.info.ID, event.GetError())
					}
					completion := eventstream.Completion{Attributes: event.GetAttributes()}
					if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Complete, Completion: &completion}); err != nil {
						return nil, err
					}
					return &providersdk.Result{Outputs: event.GetAttributes()}, nil
				}
				if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel(event.GetChannel()), Payload: append([]byte(nil), event.GetData()...)}); err != nil {
					return nil, err
				}
				continue
			}
			if result.GetOperation() != nil {
				outputs := result.GetOperation().GetOutputs()
				completion := eventstream.Completion{Attributes: outputs}
				if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Complete, Completion: &completion}); err != nil {
					return nil, err
				}
				return &providersdk.Result{Outputs: outputs}, nil
			}
		case <-a.closed:
			return nil, fmt.Errorf("agent %q: connection closed while streaming command %s", a.info.ID, cmd.CommandId)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (a *RemoteAgent) Delete(ctx context.Context, provider providersdk.Type, id string) error {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_Delete{Delete: &boxyagentv1.DeleteCommand{ResourceId: id}},
	})
	if err != nil {
		return err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	return nil
}

// List satisfies ResourceListingAgent by sending a ListCommand. The remote
// agent's executeCommand returns an AgentError if its driver doesn't
// implement providersdk.ResourceLister — same error path as any other
// command failure, deliberately not distinguished from a transient failure
// (see docs/adr/0005-remote-agent-transport-and-registration.md's
// discussion of #133).
func (a *RemoteAgent) List(ctx context.Context, provider providersdk.Type) ([]providersdk.ResourceStatus, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_List{List: &boxyagentv1.ListCommand{}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	lr := res.GetList()
	if lr == nil {
		return nil, fmt.Errorf("agent %q: unexpected result for list", a.info.ID)
	}
	statuses := make([]providersdk.ResourceStatus, 0, len(lr.GetResources()))
	for _, r := range lr.GetResources() {
		statuses = append(statuses, providersdk.ResourceStatus{ID: r.GetId(), State: r.GetState()})
	}
	return statuses, nil
}

// Allocate carries only generic JSON properties over the wire. Callers that
// want typed guest personalization should prefer PersonalizeGuest (below)
// via a GuestPersonalizingAgent type-assertion, falling back to Allocate
// when the remote driver doesn't implement providersdk.GuestPersonalizer.
func (a *RemoteAgent) Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_Allocate{Allocate: &boxyagentv1.AllocateCommand{ResourceId: id}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	ar := res.GetAllocate()
	if ar == nil || len(ar.GetPropertiesJson()) == 0 {
		return nil, nil
	}
	var props map[string]any
	if err := json.Unmarshal(ar.GetPropertiesJson(), &props); err != nil {
		return nil, fmt.Errorf("agent %q: unmarshal allocate properties: %w", a.info.ID, err)
	}
	return props, nil
}

// PersonalizeGuest satisfies GuestPersonalizingAgent by sending a
// PersonalizeGuestCommand. An empty PersonalizeGuestResult (zero properties)
// means either the remote driver doesn't implement
// providersdk.GuestPersonalizer or it does but had nothing to report — both
// collapse to nil, nil here so callers fall back to the generic Allocate
// path exactly as EmbeddedAgent's callers do (see
// internal/pool/provisioner_agent.go's AgentProvisioner.Allocate).
func (a *RemoteAgent) PersonalizeGuest(ctx context.Context, provider providersdk.Type, id string) (*providersdk.GuestPersonalizationResult, error) {
	res, err := a.call(ctx, &boxyagentv1.Command{
		ProviderType: string(provider),
		Op:           &boxyagentv1.Command_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestCommand{ResourceId: id}},
	})
	if err != nil {
		return nil, err
	}
	if agentErr := res.GetError(); agentErr != nil {
		return nil, fmt.Errorf("agent %q: %s", a.info.ID, agentErr.GetMessage())
	}
	pg := res.GetPersonalizeGuest()
	if pg == nil || len(pg.GetProperties()) == 0 {
		return nil, nil
	}
	return &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{Properties: pg.GetProperties()},
	}, nil
}

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

	mu      sync.Mutex
	pending map[string]chan *boxyagentv1.CommandResult

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

// NewRemoteAgent wraps a server-side stream handle for one connected agent.
// The caller must run Serve in its own goroutine to pump incoming frames.
func NewRemoteAgent(info AgentInfo, stream boxyagentv1.AgentTransportService_ConnectServer) *RemoteAgent {
	a := &RemoteAgent{
		info:    info,
		stream:  stream,
		pending: make(map[string]chan *boxyagentv1.CommandResult),
		closed:  make(chan struct{}),
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
func (a *RemoteAgent) Close() {
	a.closeOnce.Do(func() {
		close(a.closed)
		a.mu.Lock()
		for id, ch := range a.pending {
			delete(a.pending, id)
			close(ch)
		}
		a.mu.Unlock()
	})
}

func (a *RemoteAgent) deliver(result *boxyagentv1.CommandResult) {
	a.mu.Lock()
	ch, ok := a.pending[result.GetCommandId()]
	if ok {
		delete(a.pending, result.GetCommandId())
	}
	a.mu.Unlock()
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

// Allocate does not implement GuestPersonalizingAgent: the transport's
// AllocateCommand/AllocateResult only carries generic JSON properties, not
// providersdk.GuestAccessDetails' richer typed shape. A remote driver that
// implements providersdk.GuestPersonalizer still works through this
// baseline path (properties still convey credentials/connection info) but
// loses the typed richness embedded-agent pools get. Tracked as a known
// gap, not solved in this pass.
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

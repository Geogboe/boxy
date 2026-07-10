package agentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/emptypb"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// DriverSet maps provider type to the local driver instance that serves it,
// mirroring EmbeddedAgent's internal driver map, for the agent-side (client)
// half of the remote agent protocol.
type DriverSet map[providersdk.Type]providersdk.Driver

// RemoteClientConfig configures one boxy agent process's connection to a
// server. Token is the single-use registration token; it should only be set
// for the very first connection attempt of a process's lifetime — every
// subsequent reconnect (whether from Run's own backoff loop or a future
// process restart) authenticates via the client certificate issued in
// OnRegistered instead.
type RemoteClientConfig struct {
	AgentName         string
	Token             string
	ProviderTypes     []providersdk.Type
	Drivers           DriverSet
	HeartbeatInterval time.Duration // default 15s if zero; overridden by the server's RegisterResponse if set

	// OnRegistered is invoked once per successful registration (both the
	// first, token-based registration and any later cert-based reconnect)
	// with the server's RegisterResponse. The caller is responsible for
	// persisting ClientCertificatePem/CaCertificatePem to disk on the
	// first, token-based registration so future process restarts can
	// reconnect without a token.
	OnRegistered func(*boxyagentv1.RegisterResponse)

	Logger *slog.Logger
}

// Dialer opens one new AgentTransportService.Connect stream. Supplied by
// the caller (internal/cli's `boxy agent serve`, see Phase 5/6a) so this
// package stays transport/TLS-setup agnostic and independently testable.
type Dialer func(ctx context.Context) (boxyagentv1.AgentTransportService_ConnectClient, error)

// Run dials, registers, and serves indefinitely, reconnecting with capped
// exponential backoff (10s base, doubling, capped at 5 minutes — the same
// shape as internal/pool/manager.go's provisionBackoffState) whenever a
// session ends for any reason other than ctx being done. Only the first
// attempt uses cfg.Token; every reconnect after a successful registration
// clears it, since the agent's identity is carried by its TLS client
// certificate from that point on.
func Run(ctx context.Context, dial Dialer, cfg RemoteClientConfig) error {
	const (
		backoffBase = 10 * time.Second
		backoffCap  = 5 * time.Minute
	)
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	backoff := backoffBase
	token := cfg.Token

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		stream, err := dial(ctx)
		if err != nil {
			log.Warn("agent: dial failed, will retry", "error", err, "backoff", backoff)
		} else {
			sessionCfg := cfg
			sessionCfg.Token = token
			registered := false
			sessionCfg.OnRegistered = func(reg *boxyagentv1.RegisterResponse) {
				registered = true
				token = "" // never resend a token once registration has succeeded
				if cfg.OnRegistered != nil {
					cfg.OnRegistered(reg)
				}
			}

			if err := RunSession(ctx, stream, sessionCfg); err != nil && ctx.Err() == nil {
				log.Warn("agent: session ended, will reconnect", "error", err, "backoff", backoff)
			}
			if registered {
				backoff = backoffBase // a session that got as far as registering resets backoff
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
		if backoff > backoffCap {
			backoff = backoffCap
		}
	}
}

// RunSession drives one already-open stream to completion: sends the
// initial RegisterRequest, then runs a heartbeat sender and a
// command-dispatch receiver concurrently until the stream ends or ctx is
// cancelled. Returns the first error from either.
func RunSession(ctx context.Context, stream boxyagentv1.AgentTransportService_ConnectClient, cfg RemoteClientConfig) error {
	providerTypes := make([]string, len(cfg.ProviderTypes))
	for i, t := range cfg.ProviderTypes {
		providerTypes[i] = string(t)
	}

	sess := &clientSession{stream: stream}

	if err := sess.send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: cfg.Token,
			AgentName:         cfg.AgentName,
			ProviderTypes:     providerTypes,
		}},
	}); err != nil {
		return fmt.Errorf("send register request: %w", err)
	}

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register response: %w", err)
	}
	reg := first.GetRegistered()
	if reg == nil {
		return fmt.Errorf("expected RegisterResponse as first server frame")
	}
	if cfg.OnRegistered != nil {
		cfg.OnRegistered(reg)
	}

	interval := cfg.HeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if reg.GetHeartbeatIntervalSeconds() > 0 {
		interval = time.Duration(reg.GetHeartbeatIntervalSeconds()) * time.Second
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return sess.sendHeartbeats(gctx, reg.GetAgentId(), providerTypes, interval) })
	g.Go(func() error { return sess.dispatchCommands(gctx, cfg.Drivers) })
	return g.Wait()
}

// clientSession serializes writes to one stream: gRPC streams are not safe
// for concurrent Send from multiple goroutines, and heartbeats and
// per-command results are sent from different goroutines.
type clientSession struct {
	stream boxyagentv1.AgentTransportService_ConnectClient
	sendMu sync.Mutex
}

func (s *clientSession) send(msg *boxyagentv1.AgentMessage) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.stream.Send(msg)
}

func (s *clientSession) sendHeartbeats(ctx context.Context, agentID string, providerTypes []string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.send(&boxyagentv1.AgentMessage{
				Payload: &boxyagentv1.AgentMessage_Heartbeat{Heartbeat: &boxyagentv1.Heartbeat{
					AgentId:       agentID,
					UnixTime:      time.Now().Unix(),
					ProviderTypes: providerTypes,
				}},
			}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}

// dispatchCommands reads pushed Commands and executes each in its own
// goroutine (so a slow Create doesn't block subsequent Commands from being
// picked up), sending each CommandResult back through the shared,
// mutex-serialized sender. Returns when the stream's receive side ends.
func (s *clientSession) dispatchCommands(ctx context.Context, drivers DriverSet) error {
	for {
		msg, err := s.stream.Recv()
		if err != nil {
			return err
		}
		cmd := msg.GetCommand()
		if cmd == nil {
			continue
		}
		go func(cmd *boxyagentv1.Command) {
			result := executeCommand(ctx, drivers, cmd)
			// Best effort: if the stream is already gone, this Send fails
			// and is silently dropped — the Recv loop above observes the
			// same broken stream and tears the session down via its own
			// returned error.
			_ = s.send(&boxyagentv1.AgentMessage{
				Payload: &boxyagentv1.AgentMessage_Result{Result: result},
			})
		}(cmd)
	}
}

func executeCommand(ctx context.Context, drivers DriverSet, cmd *boxyagentv1.Command) *boxyagentv1.CommandResult {
	d, ok := drivers[providersdk.Type(cmd.GetProviderType())]
	if !ok {
		return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q not available", cmd.GetProviderType()))
	}

	switch op := cmd.GetOp().(type) {
	case *boxyagentv1.Command_Create:
		var cfg map[string]any
		if len(op.Create.GetConfigJson()) > 0 {
			if err := json.Unmarshal(op.Create.GetConfigJson(), &cfg); err != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("unmarshal create config: %v", err))
			}
		}
		res, err := d.Create(ctx, cfg)
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome: &boxyagentv1.CommandResult_Resource{Resource: &boxyagentv1.ResourceResult{
				Id:             res.ID,
				ConnectionInfo: res.ConnectionInfo,
				Metadata:       res.Metadata,
			}},
		}

	case *boxyagentv1.Command_Read:
		st, err := d.Read(ctx, op.Read.GetResourceId())
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Status{Status: &boxyagentv1.ResourceStatusResult{Id: st.ID, State: st.State}},
		}

	case *boxyagentv1.Command_Update:
		var opv map[string]any
		if len(op.Update.GetOperationJson()) > 0 {
			if err := json.Unmarshal(op.Update.GetOperationJson(), &opv); err != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("unmarshal update operation: %v", err))
			}
		}
		res, err := d.Update(ctx, op.Update.GetResourceId(), opv)
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Operation{Operation: &boxyagentv1.OperationResult{Outputs: res.Outputs}},
		}

	case *boxyagentv1.Command_Delete:
		if err := d.Delete(ctx, op.Delete.GetResourceId()); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Deleted{Deleted: &emptypb.Empty{}},
		}

	case *boxyagentv1.Command_List:
		lister, ok := d.(providersdk.ResourceLister)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("list not supported by driver %q", cmd.GetProviderType()))
		}
		statuses, err := lister.List(ctx)
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		resources := make([]*boxyagentv1.ResourceStatusResult, 0, len(statuses))
		for _, st := range statuses {
			resources = append(resources, &boxyagentv1.ResourceStatusResult{Id: st.ID, State: st.State})
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_List{List: &boxyagentv1.ListResult{Resources: resources}},
		}

	case *boxyagentv1.Command_Allocate:
		props, err := d.Allocate(ctx, op.Allocate.GetResourceId())
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error())
		}
		var propsJSON []byte
		if props != nil {
			var merr error
			propsJSON, merr = json.Marshal(props)
			if merr != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("marshal allocate properties: %v", merr))
			}
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Allocate{Allocate: &boxyagentv1.AllocateResult{PropertiesJson: propsJSON}},
		}

	default:
		return errorResult(cmd.GetCommandId(), "unknown command op")
	}
}

func errorResult(commandID, msg string) *boxyagentv1.CommandResult {
	return &boxyagentv1.CommandResult{
		CommandId: commandID,
		Outcome:   &boxyagentv1.CommandResult_Error{Error: &boxyagentv1.AgentError{Message: msg}},
	}
}

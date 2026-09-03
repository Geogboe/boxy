package agentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/emptypb"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/eventstream"
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
	AgentName string
	Token     string
	// AgentVersion is this agent binary's version string, sent on every
	// RegisterRequest so the server can refuse a version-mismatched
	// connection (see #167) rather than let skewed agent/server builds
	// talk an ambiguous protocol to each other.
	AgentVersion      string
	ProviderTypes     []providersdk.Type
	Drivers           DriverSet
	HeartbeatInterval time.Duration // default 15s if zero; overridden by the server's RegisterResponse if set

	// AvailabilitySampleTimeout bounds each individual
	// providersdk.AvailabilityReporter query sampled before a Heartbeat is
	// sent — see sampleAvailability. Zero uses
	// defaultAvailabilitySampleTimeout; tests override it to avoid real
	// sleeps, mirroring hyperv.Driver.memoryQueryTimeout's pattern.
	AvailabilitySampleTimeout time.Duration

	// LogShipper optionally buffers safe agent diagnostics. RunSession flushes
	// one bounded batch over the authenticated agent stream after each
	// heartbeat; a failed flush is retained for retry.
	LogShipper *diagnostics.Shipper

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
	// Fail fast, locally: the server rejects a blank agent_version the same
	// as any other mismatch (see #167), but its rejection deliberately
	// omits detail since the peer isn't authenticated at that point yet
	// (internal/agentserver/server.go's Connect). Catching this here, before
	// the stream is used at all, turns a misconfigured caller's first
	// symptom from an opaque round-trip failure into an immediate, clear
	// local error.
	if cfg.AgentVersion == "" {
		return fmt.Errorf("agentsdk: RemoteClientConfig.AgentVersion must be set")
	}

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
			AgentVersion:      cfg.AgentVersion,
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

	availabilityTimeout := cfg.AvailabilitySampleTimeout
	if availabilityTimeout <= 0 {
		availabilityTimeout = defaultAvailabilitySampleTimeout
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return sess.sendHeartbeats(gctx, reg.GetAgentId(), providerTypes, cfg.Drivers, interval, availabilityTimeout, cfg.LogShipper, log)
	})
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

func (s *clientSession) sendHeartbeats(ctx context.Context, agentID string, providerTypes []string, drivers DriverSet, interval, availabilityTimeout time.Duration, shipper *diagnostics.Shipper, log *slog.Logger) error {
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
					Availability:  sampleAvailability(ctx, drivers, availabilityTimeout, log),
				}},
			}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
			if shipper != nil {
				if err := shipper.Flush(ctx, diagnostics.BatchSinkFunc(s.sendLogBatch)); err != nil {
					code, summary := diagnostics.DescribeError(fmt.Errorf("agent log ship: %w", err))
					log.Warn("agent: log batch flush failed, retaining events for retry", "agent_id", agentID, "operation", "agent_log_ship", "error_code", code, "error_summary", summary)
				}
			}
		}
	}
}

func (s *clientSession) sendLogBatch(ctx context.Context, events []diagnostics.Event) error {
	items := make([]*boxyagentv1.LogEvent, 0, len(events))
	for _, event := range events {
		timestamp := event.Timestamp
		if timestamp.IsZero() {
			timestamp = time.Now().UTC()
		}
		items = append(items, &boxyagentv1.LogEvent{
			UnixNano:     timestamp.UnixNano(),
			Level:        event.Level,
			Component:    event.Component,
			Message:      event.Message,
			Operation:    event.Operation,
			ErrorCode:    event.ErrorCode,
			ErrorSummary: event.ErrorSummary,
			Pool:         event.Pool,
			Resource:     event.Resource,
			Request:      event.Request,
		})
	}
	if len(items) == 0 {
		return nil
	}
	return s.send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_LogBatch{
		LogBatch: &boxyagentv1.LogBatch{Events: items},
	}})
}

// defaultAvailabilitySampleTimeout is the per-provider bound applied to a
// providersdk.AvailabilityReporter query when RemoteClientConfig doesn't
// override it. availabilitySampleGrace is added on top of that bound for
// sampleAvailability's own hard deadline, giving a well-behaved reporter
// that respects ctx cancellation a brief window to return its ctx.Err()
// rather than being raced against the exact same deadline twice.
const (
	defaultAvailabilitySampleTimeout = 5 * time.Second
	availabilitySampleGrace          = 500 * time.Millisecond
)

// sampleAvailability queries every driver in drivers that implements
// providersdk.AvailabilityReporter, concurrently, and returns one
// ProviderAvailability entry per provider that answered successfully within
// the deadline. It is deliberately best-effort and hard-bounded:
//
//   - A driver with no reporter is silently skipped — not every provider
//     implements this optional capability.
//   - A reporter that returns an error is logged and skipped, not treated
//     as a heartbeat-sending failure — availability reporting must never
//     mark an otherwise-healthy agent offline.
//   - Total wall-clock time is capped at timeout+availabilitySampleGrace
//     regardless of how many reporters there are or whether one hangs
//     forever ignoring its context (see fakeHangingAvailabilityDriver in
//     tests) — this bound exists specifically so a stuck reporter can never
//     indefinitely delay heartbeat delivery. Command dispatch runs on an
//     entirely separate goroutine/stream-read loop and is never touched by
//     this call at all.
//
// A hung reporter's goroutine is abandoned, not killed (Go has no
// mechanism to forcibly cancel a goroutine that ignores ctx) — it writes
// into the buffered channel whenever it eventually returns and is then
// garbage collected; it does not block any future call.
func sampleAvailability(ctx context.Context, drivers DriverSet, timeout time.Duration, log *slog.Logger) []*boxyagentv1.ProviderAvailability {
	type sample struct {
		providerType providersdk.Type
		avail        *providersdk.ResourceAvailability
	}

	reporters := make(map[providersdk.Type]providersdk.AvailabilityReporter, len(drivers))
	for pt, d := range drivers {
		if r, ok := d.(providersdk.AvailabilityReporter); ok {
			reporters[pt] = r
		}
	}
	if len(reporters) == 0 {
		return nil
	}

	results := make(chan sample, len(reporters))
	for pt, r := range reporters {
		go func(pt providersdk.Type, r providersdk.AvailabilityReporter) {
			qctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			avail, err := r.Availability(qctx)
			if err != nil {
				if log != nil {
					code, summary := diagnostics.DescribeError(fmt.Errorf("%s availability probe: %w", pt, err))
					log.Warn("agent: availability reporter failed, omitting from this heartbeat", "provider_type", pt, "operation", "availability_probe", "error_code", code, "error_summary", summary)
				}
				results <- sample{providerType: pt}
				return
			}
			results <- sample{providerType: pt, avail: avail}
		}(pt, r)
	}

	deadline := time.After(timeout + availabilitySampleGrace)
	entries := make([]*boxyagentv1.ProviderAvailability, 0, len(reporters))
	for i := 0; i < len(reporters); i++ {
		select {
		case r := <-results:
			if r.avail != nil {
				entries = append(entries, &boxyagentv1.ProviderAvailability{
					ProviderType: string(r.providerType),
					MemoryMb:     r.avail.MemoryMB,
				})
			}
		case <-deadline:
			if log != nil {
				log.Warn("agent: availability sampling deadline exceeded, some providers omitted from this heartbeat")
			}
			return entries
		}
	}
	return entries
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
			if update := cmd.GetUpdate(); update != nil && update.GetStream() {
				executeStreamingCommand(ctx, drivers, cmd, s.send)
				return
			}
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

func executeStreamingCommand(ctx context.Context, drivers DriverSet, cmd *boxyagentv1.Command, send func(*boxyagentv1.AgentMessage) error) {
	d, ok := drivers[providersdk.Type(cmd.GetProviderType())]
	if !ok {
		_ = send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{Result: errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q not available", cmd.GetProviderType()), nil)}})
		return
	}
	update := cmd.GetUpdate()
	var opv map[string]any
	if len(update.GetOperationJson()) > 0 {
		if err := json.Unmarshal(update.GetOperationJson(), &opv); err != nil {
			_ = send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{Result: errorResult(cmd.GetCommandId(), fmt.Sprintf("unmarshal update operation: %v", err), err)}})
			return
		}
	}
	streamSink := &remoteStreamSink{commandID: cmd.GetCommandId(), send: send}
	streamer, ok := d.(providersdk.StreamingDriver)
	if !ok {
		_ = streamSink.complete(nil, fmt.Sprintf("provider %q does not support streaming operations", cmd.GetProviderType()))
		return
	}
	result, err := streamer.UpdateStream(ctx, update.GetResourceId(), decodeUpdateOperation(opv), streamSink)
	if err != nil {
		_ = streamSink.complete(nil, err.Error())
		return
	}
	if !streamSink.isCompleted() {
		var outputs map[string]string
		if result != nil {
			outputs = result.Outputs
		}
		_ = streamSink.complete(outputs, "")
	}
}

// remoteStreamSink is the agent-side eventstream.Sink that forwards a
// driver's stream events back to the server over the shared gRPC stream.
// completed is guarded by mu rather than left as a bare bool: every
// concrete driver in this codebase happens to funnel its sink calls through
// a single consumer goroutine, but providersdk.StreamingDriver (and thus
// eventstream.Sink) is a public interface external implementations can
// satisfy, and nothing in Sink's contract forbids calling Send from more
// than one goroutine (e.g. independent stdout/stderr forwarders). Without
// the lock, concurrent Send/complete calls would race on completed — an
// actual data race, not just a logic bug — and could let more than one
// goroutine past the "already completed" check.
type remoteStreamSink struct {
	commandID string
	send      func(*boxyagentv1.AgentMessage) error

	mu        sync.Mutex
	completed bool
}

// isCompleted reports whether a Complete event has already been sent.
// executeStreamingCommand uses this (rather than reading the completed
// field directly) to decide whether it still needs to send its own
// synthetic completion after UpdateStream returns — a direct field read
// there would be exactly the same unsynchronized access Send/complete
// guard against, just from outside the type instead of within it.
func (s *remoteStreamSink) isCompleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed
}

func (s *remoteStreamSink) Send(_ context.Context, event eventstream.Event) error {
	isComplete := event.Kind == eventstream.Complete

	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return eventstream.ErrCompleted
	}
	if isComplete {
		s.completed = true
	}
	s.mu.Unlock()

	// The actual send (network I/O) deliberately happens outside the lock:
	// only the completed check-and-set needs to be atomic, not the
	// downstream write, and clientSession.send has its own sendMu to
	// serialize the underlying stream regardless.
	message := &boxyagentv1.OperationStreamEvent{Channel: string(event.Channel), Data: append([]byte(nil), event.Payload...)}
	if isComplete {
		message.Complete = true
		if event.Completion != nil {
			message.Attributes = cloneStringMap(event.Completion.Attributes)
			if event.Completion.Err != nil {
				message.Error = event.Completion.Err.Error()
			}
		}
	}
	return s.send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{Result: &boxyagentv1.CommandResult{
		CommandId: s.commandID,
		Outcome:   &boxyagentv1.CommandResult_OperationStream{OperationStream: message},
	}}})
}

func (s *remoteStreamSink) complete(attributes map[string]string, errMsg string) error {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return nil
	}
	s.completed = true
	s.mu.Unlock()

	return s.send(&boxyagentv1.AgentMessage{Payload: &boxyagentv1.AgentMessage_Result{Result: &boxyagentv1.CommandResult{
		CommandId: s.commandID,
		Outcome: &boxyagentv1.CommandResult_OperationStream{OperationStream: &boxyagentv1.OperationStreamEvent{
			Complete:   true,
			Attributes: cloneStringMap(attributes),
			Error:      errMsg,
		}},
	}}})
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func executeCommand(ctx context.Context, drivers DriverSet, cmd *boxyagentv1.Command) *boxyagentv1.CommandResult {
	d, ok := drivers[providersdk.Type(cmd.GetProviderType())]
	if !ok {
		return errorResult(cmd.GetCommandId(), fmt.Sprintf("provider %q not available", cmd.GetProviderType()), nil)
	}

	switch op := cmd.GetOp().(type) {
	case *boxyagentv1.Command_Create:
		var cfg map[string]any
		if len(op.Create.GetConfigJson()) > 0 {
			if err := json.Unmarshal(op.Create.GetConfigJson(), &cfg); err != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("unmarshal create config: %v", err), err)
			}
		}
		res, err := d.Create(ctx, cfg)
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
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
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Status{Status: &boxyagentv1.ResourceStatusResult{Id: st.ID, State: st.State}},
		}

	case *boxyagentv1.Command_Update:
		var opv map[string]any
		if len(op.Update.GetOperationJson()) > 0 {
			if err := json.Unmarshal(op.Update.GetOperationJson(), &opv); err != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("unmarshal update operation: %v", err), err)
			}
		}
		res, err := d.Update(ctx, op.Update.GetResourceId(), decodeUpdateOperation(opv))
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Operation{Operation: &boxyagentv1.OperationResult{Outputs: res.Outputs}},
		}

	case *boxyagentv1.Command_Delete:
		if err := d.Delete(ctx, op.Delete.GetResourceId()); err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Deleted{Deleted: &emptypb.Empty{}},
		}

	case *boxyagentv1.Command_List:
		lister, ok := d.(providersdk.ResourceLister)
		if !ok {
			return errorResult(cmd.GetCommandId(), fmt.Sprintf("list not supported by driver %q", cmd.GetProviderType()), nil)
		}
		statuses, err := lister.List(ctx)
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
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
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		var propsJSON []byte
		if props != nil {
			var merr error
			propsJSON, merr = json.Marshal(props)
			if merr != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("marshal allocate properties: %v", merr), merr)
			}
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome:   &boxyagentv1.CommandResult_Allocate{Allocate: &boxyagentv1.AllocateResult{PropertiesJson: propsJSON}},
		}

	case *boxyagentv1.Command_PersonalizeGuest:
		gp, ok := d.(providersdk.GuestPersonalizer)
		if !ok {
			return &boxyagentv1.CommandResult{
				CommandId: cmd.GetCommandId(),
				Outcome:   &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{}},
			}
		}
		result, err := gp.PersonalizeGuest(ctx, op.PersonalizeGuest.GetResourceId())
		if err != nil {
			return errorResult(cmd.GetCommandId(), err.Error(), err)
		}
		if result == nil {
			return &boxyagentv1.CommandResult{
				CommandId: cmd.GetCommandId(),
				Outcome:   &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{}},
			}
		}
		var credentialJSON []byte
		if result.EphemeralCredential != nil {
			var marshalErr error
			credentialJSON, marshalErr = json.Marshal(result.EphemeralCredential)
			if marshalErr != nil {
				return errorResult(cmd.GetCommandId(), fmt.Sprintf("marshal guest credential: %v", marshalErr), marshalErr)
			}
		}
		return &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome: &boxyagentv1.CommandResult_PersonalizeGuest{PersonalizeGuest: &boxyagentv1.PersonalizeGuestResult{
				Properties:          result.AccessDetails.Properties,
				GuestCredentialJson: credentialJSON,
			}},
		}

	default:
		return errorResult(cmd.GetCommandId(), "unknown command op", nil)
	}
}

func decodeUpdateOperation(raw map[string]any) providersdk.Operation {
	if len(raw) == 0 {
		return raw
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	var execOp providersdk.ExecOperation
	if err := json.Unmarshal(encoded, &execOp); err == nil && (len(execOp.Command) > 0 || execOp.CommandText != "" || execOp.Script != nil) {
		return &execOp
	}
	return raw
}

func errorResult(commandID, msg string, err error) *boxyagentv1.CommandResult {
	ae := &boxyagentv1.AgentError{Message: msg}
	var et providersdk.ErrorTyper
	if err != nil && errors.As(err, &et) {
		// Marshal et (what errors.As actually found), not err: err may be a
		// wrapper (e.g. fmt.Errorf("...: %w", et)) with no exported fields
		// of its own, which would silently JSON-marshal to "{}" and zero out
		// every field on the far side of reconstructAgentError. ErrorType is
		// only set alongside a successful marshal — an ErrorType with no
		// usable ErrorDetailJson would make reconstructAgentError's
		// json.Unmarshal fail and fall back to the plain message anyway, so
		// leaving both unset keeps the two fields consistent instead of
		// half-populating one of them.
		if detail, jerr := json.Marshal(et); jerr == nil {
			ae.ErrorType = et.ErrorType()
			ae.ErrorDetailJson = detail
		}
	}
	return &boxyagentv1.CommandResult{CommandId: commandID, Outcome: &boxyagentv1.CommandResult_Error{Error: ae}}
}

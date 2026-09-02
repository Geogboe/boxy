package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/google/uuid"
)

const executionRetention = 24 * time.Hour

// ErrResourceBusy is returned before a second execution can be persisted for
// the same provider resource.
var ErrResourceBusy = errors.New("resource has an active execution")

type resourceBusyError struct {
	ExecutionID model.ExecutionID
}

func (e *resourceBusyError) Error() string {
	return fmt.Sprintf("resource has an active execution %q", e.ExecutionID)
}

func (e *resourceBusyError) Unwrap() error { return ErrResourceBusy }

// executionManager owns the only non-durable part of an execution: its
// provider operation and cancellation function. The durable record is always
// authoritative for readers and survives process crashes without replaying
// the operation.
type executionManager struct {
	store    store.Store
	executor SandboxExecutor

	mu         sync.Mutex
	active     map[model.ResourceID]model.ExecutionID
	cancel     map[model.ExecutionID]context.CancelFunc
	operations map[model.ExecutionID]providersdk.ExecOperation
}

func (s *Server) executionService() *executionManager {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.executions == nil {
		s.executions = newExecutionManager(s.store, s.executor)
	}
	return s.executions
}

func newExecutionManager(st store.Store, executor SandboxExecutor) *executionManager {
	m := &executionManager{
		store:      st,
		executor:   executor,
		active:     make(map[model.ResourceID]model.ExecutionID),
		cancel:     make(map[model.ExecutionID]context.CancelFunc),
		operations: make(map[model.ExecutionID]providersdk.ExecOperation),
	}
	// A process restart cannot safely recover the opaque operation payload.
	// Marking records interrupted is therefore deliberately the only recovery
	// action; workers are never reconstructed from persisted metadata.
	_ = m.markInterrupted(context.Background())
	return m
}

func (m *executionManager) markInterrupted(ctx context.Context) error {
	executions, err := m.store.ListExecutions(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, execution := range executions {
		if execution.Status != model.ExecutionStatusPending && execution.Status != model.ExecutionStatusRunning {
			continue
		}
		execution.Status = model.ExecutionStatusInterrupted
		execution.Error = "execution interrupted because the Boxy server restarted"
		execution.FinishedAt = timePtr(now)
		if err := m.store.PutExecution(ctx, execution); err != nil {
			return err
		}
	}
	return nil
}

func (m *executionManager) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.cancel {
		cancel()
	}
}

func (m *executionManager) submit(ctx context.Context, sb model.Sandbox, resource model.Resource, operation providersdk.ExecOperation, inputKind model.ExecutionInputKind, actor string, timeout time.Duration) (model.Execution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existingID, ok := m.active[resource.ID]; ok {
		return model.Execution{}, &resourceBusyError{ExecutionID: existingID}
	}
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	now := time.Now().UTC()
	execution := model.Execution{
		ID:                 model.ExecutionID(uuid.NewString()),
		SandboxID:          sb.ID,
		ResourceID:         resource.ID,
		ActorID:            actor,
		Status:             model.ExecutionStatusRunning,
		InputKind:          inputKind,
		RequestFingerprint: executionFingerprint(operation, inputKind),
		CreatedAt:          now,
		StartedAt:          timePtr(now),
		DeadlineAt:         now.Add(timeout),
	}
	if err := m.store.PutExecution(ctx, execution); err != nil {
		return model.Execution{}, fmt.Errorf("persist execution: %w", err)
	}
	m.active[resource.ID] = execution.ID
	m.operations[execution.ID] = cloneExecOperation(operation)
	workerCtx, cancel := context.WithTimeout(context.Background(), timeout)
	m.cancel[execution.ID] = cancel
	go func() {
		defer cancel()
		// Durable workers intentionally use the server-owned context so a client
		// disconnect cannot cancel the provider operation.
		m.run(workerCtx, execution, resource, cloneExecOperation(operation)) //nolint:gosec // see the durable worker context above.
	}()
	return execution, nil
}

func (m *executionManager) run(ctx context.Context, execution model.Execution, resource model.Resource, operation providersdk.ExecOperation) {
	sink := &durableExecutionSink{manager: m, executionID: execution.ID}
	var result *providersdk.Result
	var runErr error
	if m.executor == nil {
		runErr = errors.New("sandbox command execution is not available")
	} else {
		result, runErr = m.executor.ExecuteSandbox(ctx, resource, operation, sink)
	}
	if result != nil {
		// A few legacy providers return captured output in Result.Outputs
		// instead of using the optional stream. Preserve that output without
		// changing the provider contract, while never persisting other result
		// values as command input.
		for _, channel := range []string{"stdout", "stderr"} {
			if value := result.Outputs[channel]; value != "" {
				_ = sink.Send(context.Background(), eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel(channel), Payload: []byte(value)})
			}
		}
	}
	m.finish(execution.ID, result, runErr)
}

func (m *executionManager) finish(id model.ExecutionID, result *providersdk.Result, runErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	execution, err := m.store.GetExecution(context.Background(), id)
	if err != nil {
		delete(m.cancel, id)
		delete(m.operations, id)
		return
	}
	now := time.Now().UTC()
	execution.FinishedAt = timePtr(now)
	if result != nil {
		if code, parseErr := strconv.Atoi(result.Outputs["exit_code"]); parseErr == nil {
			execution.ExitCode = intPtr(code)
		}
	}
	switch {
	case errors.Is(runErr, context.Canceled):
		execution.Status = model.ExecutionStatusCancelled
		execution.Error = "execution cancelled"
	case errors.Is(runErr, context.DeadlineExceeded):
		execution.Status = model.ExecutionStatusFailed
		execution.Error = "execution timed out"
	case runErr != nil:
		execution.Status = model.ExecutionStatusFailed
		execution.Error = safeExecutionError(runErr)
	case execution.ExitCode != nil && *execution.ExitCode != 0:
		execution.Status = model.ExecutionStatusFailed
	default:
		execution.Status = model.ExecutionStatusSucceeded
	}
	// A failed store write cannot be reported to the provider caller anymore;
	// retaining the active guard until this point is safer than allowing a
	// second provider operation to race the old one.
	_ = m.store.PutExecution(context.Background(), execution)
	delete(m.cancel, id)
	delete(m.operations, id)
	if m.active[execution.ResourceID] == id {
		delete(m.active, execution.ResourceID)
	}
}

func safeExecutionError(err error) string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Error()) == "" {
		return "provider execution failed"
	}
	// Provider errors are not trusted input. Never persist provider text because
	// it may repeat command arguments, script bytes, environment values, or
	// credential data supplied for this execution.
	return "provider execution failed"
}

func (m *executionManager) cancelExecution(id model.ExecutionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel, ok := m.cancel[id]
	if !ok {
		execution, err := m.store.GetExecution(context.Background(), id)
		if err != nil {
			return err
		}
		if execution.Status.IsTerminal() {
			return nil
		}
		return errors.New("execution worker is unavailable")
	}
	cancel()
	return nil
}

func (m *executionManager) get(ctx context.Context, id model.ExecutionID) (model.Execution, error) {
	execution, err := m.store.GetExecution(ctx, id)
	if err != nil {
		return model.Execution{}, err
	}
	if execution.Status.IsTerminal() && !execution.FinishedAt.IsZero() && time.Since(execution.FinishedAt.UTC()) > executionRetention {
		if deleteErr := m.store.DeleteExecution(ctx, id); deleteErr != nil && !errors.Is(deleteErr, store.ErrNotFound) {
			return model.Execution{}, deleteErr
		}
		return model.Execution{}, store.ErrNotFound
	}
	return execution, nil
}

type durableExecutionSink struct {
	manager     *executionManager
	executionID model.ExecutionID
}

func (s *durableExecutionSink) Send(_ context.Context, event eventstream.Event) error {
	switch event.Kind {
	case eventstream.Data:
		return s.manager.appendOutput(s.executionID, string(event.Channel), event.Payload)
	case eventstream.Complete:
		return nil
	default:
		return fmt.Errorf("unsupported execution event kind %d", event.Kind)
	}
}

func (m *executionManager) appendOutput(id model.ExecutionID, stream string, data []byte) error {
	if stream == "" {
		return eventstream.ErrInvalidStream
	}
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	execution, err := m.store.GetExecution(context.Background(), id)
	if err != nil {
		return err
	}
	if execution.Truncated {
		return eventstream.ErrLimitExceeded
	}
	used := int64(0)
	for _, chunk := range execution.Chunks {
		if !chunk.Dropped {
			used += int64(len(chunk.Data))
		}
	}
	remaining := int64(maxExecOutput) - used
	if remaining <= 0 {
		execution.Truncated = true
		execution.Chunks = append(execution.Chunks, model.ExecutionChunk{Cursor: nextExecutionCursor(execution), Stream: stream, Dropped: true})
		_ = m.store.PutExecution(context.Background(), execution)
		return eventstream.ErrLimitExceeded
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		execution.Truncated = true
	}
	for len(data) > 0 {
		size := len(data)
		if size > maxExecChunk {
			size = maxExecChunk
		}
		execution.Chunks = append(execution.Chunks, model.ExecutionChunk{
			Cursor: nextExecutionCursor(execution), Stream: stream, Data: append([]byte(nil), data[:size]...),
		})
		data = data[size:]
	}
	if execution.Truncated {
		execution.Chunks = append(execution.Chunks, model.ExecutionChunk{Cursor: nextExecutionCursor(execution), Stream: stream, Dropped: true})
	}
	if err := m.store.PutExecution(context.Background(), execution); err != nil {
		return err
	}
	if execution.Truncated {
		return eventstream.ErrLimitExceeded
	}
	return nil
}

func nextExecutionCursor(execution model.Execution) uint64 {
	if len(execution.Chunks) == 0 {
		return 1
	}
	return execution.Chunks[len(execution.Chunks)-1].Cursor + 1
}

func cloneExecOperation(operation providersdk.ExecOperation) providersdk.ExecOperation {
	operation.Command = append([]string(nil), operation.Command...)
	if operation.Env != nil {
		operation.Env = make(map[string]string, len(operation.Env))
		for key, value := range operation.Env {
			operation.Env[key] = value
		}
	}
	if operation.Script != nil {
		script := *operation.Script
		script.Content = append([]byte(nil), operation.Script.Content...)
		script.Args = append([]string(nil), operation.Script.Args...)
		operation.Script = &script
	}
	return operation
}

func executionFingerprint(operation providersdk.ExecOperation, inputKind model.ExecutionInputKind) string {
	// The digest is a fingerprint only; the payload itself is intentionally
	// absent from the durable model. Credentials and environment values are
	// excluded entirely so this hash cannot become an accidental secret log.
	type fingerprint struct {
		Kind          model.ExecutionInputKind `json:"kind"`
		CommandCount  int                      `json:"command_count"`
		CommandDigest string                   `json:"command_digest,omitempty"`
		TextDigest    string                   `json:"text_digest,omitempty"`
		ScriptDigest  string                   `json:"script_digest,omitempty"`
		ScriptArgs    int                      `json:"script_args,omitempty"`
	}
	f := fingerprint{Kind: inputKind, CommandCount: len(operation.Command), ScriptArgs: lenScriptArgs(operation.Script)}
	if len(operation.Command) > 0 {
		f.CommandDigest = digestJSON(operation.Command)
	}
	if operation.CommandText != "" {
		f.TextDigest = digestString(operation.CommandText)
	}
	if operation.Script != nil {
		f.ScriptDigest = strings.ToLower(operation.Script.Digest)
	}
	return digestJSON(f)
}

func lenScriptArgs(script *providersdk.ScriptSpec) int {
	if script == nil {
		return 0
	}
	return len(script.Args)
}

func digestString(value string) string { return digestBytes([]byte(value)) }

func digestJSON(value any) string {
	b, _ := json.Marshal(value)
	return digestBytes(b)
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// encodeExecutionCursor is intentionally opaque to API consumers. It uses a
// binary cursor rather than exposing the internal chunk index as JSON.
func encodeExecutionCursor(cursor uint64) string {
	var raw [9]byte
	raw[0] = 1
	binary.BigEndian.PutUint64(raw[1:], cursor)
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func decodeExecutionCursor(raw string) (uint64, error) {
	if raw == "" || raw == "0" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 9 || b[0] != 1 {
		return 0, errors.New("from must be an opaque execution cursor")
	}
	return binary.BigEndian.Uint64(b[1:]), nil
}

func timePtr(value time.Time) *time.Time { return &value }
func intPtr(value int) *int              { return &value }

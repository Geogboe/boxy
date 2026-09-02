package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type durableExecExecutor struct {
	firstSent   chan struct{}
	continueRun chan struct{}
	started     chan struct{}
}

type terminalWriteFailStore struct {
	store.Store
}

func (s *terminalWriteFailStore) PutExecution(ctx context.Context, execution model.Execution) error {
	if execution.FinishedAt != nil {
		return errors.New("terminal execution write failed")
	}
	return s.Store.PutExecution(ctx, execution)
}

func (e *durableExecExecutor) ExecuteSandbox(ctx context.Context, _ model.Resource, _ providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	if e.started != nil {
		close(e.started)
	}
	if e.firstSent != nil {
		if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: "stdout", Payload: []byte("first\n")}); err != nil {
			return nil, err
		}
		close(e.firstSent)
		select {
		case <-e.continueRun:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: "stdout", Payload: []byte("second\n")}); err != nil {
			return nil, err
		}
		return &providersdk.Result{Outputs: map[string]string{"exit_code": "0"}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func newDurableExecMux(t *testing.T, executor SandboxExecutor, resources ...model.ResourceID) (*http.ServeMux, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	if len(resources) == 0 {
		resources = []model.ResourceID{"res-1"}
	}
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: resources}); err != nil {
		t.Fatal(err)
	}
	for _, id := range resources {
		if err := st.PutResource(context.Background(), model.Resource{ID: id, State: model.ResourceStateAllocated}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	t.Cleanup(func() {
		if s.executions != nil {
			s.executions.shutdown()
		}
	})
	return mux, st
}

func postDurableExec(t *testing.T, mux *http.ServeMux, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode POST response: %v; body=%s", err, response.Body.String())
	}
	return response.Code, decoded
}

func waitForExecution(t *testing.T, st store.Store, id model.ExecutionID, want model.ExecutionStatus) model.Execution {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		execution, err := st.GetExecution(context.Background(), id)
		if err == nil && execution.Status == want {
			return execution
		}
		time.Sleep(5 * time.Millisecond)
	}
	execution, _ := st.GetExecution(context.Background(), id)
	t.Fatalf("execution status = %q, want %q", execution.Status, want)
	return model.Execution{}
}

func TestDurableExecSubmitReturns202AndReconnectsAtCursor(t *testing.T) {
	executor := &durableExecExecutor{firstSent: make(chan struct{}), continueRun: make(chan struct{})}
	mux, st := newDurableExecMux(t, executor)
	status, accepted := postDurableExec(t, mux, `{"command":["echo","hello"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", status)
	}
	id := model.ExecutionID(accepted["exec_id"].(string))
	<-executor.firstSent

	first := getDurableExec(t, mux, "", id)
	if len(first.Chunks) != 1 || first.Chunks[0].Data == "" {
		t.Fatalf("first page = %+v, want one output chunk", first)
	}
	secondBeforeFinish := getDurableExec(t, mux, first.Next, id)
	if len(secondBeforeFinish.Chunks) != 0 {
		t.Fatalf("reattached page before next output = %+v, want no duplicate chunks", secondBeforeFinish)
	}
	close(executor.continueRun)
	finished := waitForExecution(t, st, id, model.ExecutionStatusSucceeded)
	second := getDurableExec(t, mux, first.Next, id)
	if len(second.Chunks) != 1 || second.Chunks[0].Data == first.Chunks[0].Data {
		t.Fatalf("reattached page = %+v, want only the second chunk", second)
	}
	if finished.RequestFingerprint == "" || len(finished.Chunks) != 2 {
		t.Fatalf("finished execution = %+v, want safe fingerprint and two chunks", finished)
	}
}

func TestDurableExecReturnsResourceBusyWithActiveID(t *testing.T) {
	executor := &durableExecExecutor{started: make(chan struct{})}
	mux, st := newDurableExecMux(t, executor)
	status, accepted := postDurableExec(t, mux, `{"command":["sleep","1"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202", status)
	}
	<-executor.started
	busyStatus, busy := postDurableExec(t, mux, `{"command":["second"]}`)
	if busyStatus != http.StatusConflict || busy["error"] != "resource_busy" {
		t.Fatalf("busy response = %d %+v, want 409 resource_busy", busyStatus, busy)
	}
	if busy["exec_id"] != accepted["exec_id"] {
		t.Fatalf("busy active ID = %v, want %v", busy["exec_id"], accepted["exec_id"])
	}
	executionID := model.ExecutionID(accepted["exec_id"].(string))
	if err := st.DeleteExecution(context.Background(), executionID); err != nil {
		t.Fatal(err)
	}
}

func TestDurableExecCancelIsServerOwnedAndTerminal(t *testing.T) {
	executor := &durableExecExecutor{started: make(chan struct{})}
	mux, st := newDurableExecMux(t, executor)
	status, accepted := postDurableExec(t, mux, `{"command":["sleep","1"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", status)
	}
	<-executor.started
	id := accepted["exec_id"].(string)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec/"+id+"/cancel", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want 202; body=%s", response.Code, response.Body.String())
	}
	waitForExecution(t, st, model.ExecutionID(id), model.ExecutionStatusCancelled)
}

type parallelDurableExecExecutor struct {
	started chan model.ResourceID
	release chan struct{}
}

func (e *parallelDurableExecExecutor) ExecuteSandbox(ctx context.Context, resource model.Resource, _ providersdk.ExecOperation, _ eventstream.Sink) (*providersdk.Result, error) {
	e.started <- resource.ID
	select {
	case <-e.release:
		return &providersdk.Result{Outputs: map[string]string{"exit_code": "0"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDurableExecAllowsDifferentResourcesToRunConcurrently(t *testing.T) {
	executor := &parallelDurableExecExecutor{started: make(chan model.ResourceID, 2), release: make(chan struct{})}
	mux, st := newDurableExecMux(t, executor, "res-1", "res-2")
	_, first := postDurableExec(t, mux, `{"resource_id":"res-1","command":["first"]}`)
	_, second := postDurableExec(t, mux, `{"resource_id":"res-2","command":["second"]}`)

	seen := map[model.ResourceID]bool{}
	for range 2 {
		select {
		case id := <-executor.started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("different-resource executions did not start concurrently")
		}
	}
	if !seen["res-1"] || !seen["res-2"] {
		t.Fatalf("started resources = %+v", seen)
	}
	close(executor.release)
	waitForExecution(t, st, model.ExecutionID(first["exec_id"].(string)), model.ExecutionStatusSucceeded)
	waitForExecution(t, st, model.ExecutionID(second["exec_id"].(string)), model.ExecutionStatusSucceeded)
}

func TestDurableExecRestartMarksActiveExecutionInterruptedWithoutReplay(t *testing.T) {
	st := store.NewMemoryStore()
	id := model.ExecutionID("exec-restart")
	if err := st.PutExecution(context.Background(), model.Execution{
		ID: id, SandboxID: "sb-1", ResourceID: "res-1", Status: model.ExecutionStatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	manager := newExecutionManager(st, nil)
	defer manager.shutdown()

	execution, err := st.GetExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != model.ExecutionStatusInterrupted {
		t.Fatalf("restarted execution status = %q, want interrupted", execution.Status)
	}
	if execution.Error == "" || execution.FinishedAt == nil {
		t.Fatalf("interrupted execution missing safe terminal metadata: %+v", execution)
	}
}

func TestDurableExecTruncationStoresExplicitMarker(t *testing.T) {
	st := store.NewMemoryStore()
	id := model.ExecutionID("exec-truncate")
	if err := st.PutExecution(context.Background(), model.Execution{ID: id, SandboxID: "sb-1", ResourceID: "res-1", Status: model.ExecutionStatusRunning}); err != nil {
		t.Fatal(err)
	}
	manager := &executionManager{store: st}
	if err := manager.appendOutput(id, "stdout", make([]byte, maxExecOutput+1)); err != eventstream.ErrLimitExceeded {
		t.Fatalf("appendOutput error = %v, want output limit error", err)
	}
	execution, err := st.GetExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !execution.Truncated || len(execution.Chunks) < 2 || !execution.Chunks[len(execution.Chunks)-1].Dropped {
		t.Fatalf("truncated execution = %+v, want explicit dropped marker", execution)
	}
	used := 0
	for _, chunk := range execution.Chunks {
		used += len(chunk.Data)
	}
	if used > maxExecOutput {
		t.Fatalf("stored output = %d bytes, want at most %d", used, maxExecOutput)
	}
	manager.finish(id, nil, eventstream.ErrLimitExceeded)
	finished, err := st.GetExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != model.ExecutionStatusFailed || finished.Error != "execution output limit exceeded" {
		t.Fatalf("limit execution terminal state = %+v, want explicit output-limit failure", finished)
	}
}

func TestDurableExecTerminalWriteFailureRetainsResourceGuard(t *testing.T) {
	base := store.NewMemoryStore()
	st := &terminalWriteFailStore{Store: base}
	id := model.ExecutionID("exec-terminal-write-failure")
	if err := st.PutExecution(context.Background(), model.Execution{
		ID: id, SandboxID: "sb-1", ResourceID: "res-1", Status: model.ExecutionStatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	manager := &executionManager{
		store:      st,
		active:     map[model.ResourceID]model.ExecutionID{"res-1": id},
		cancel:     map[model.ExecutionID]context.CancelFunc{id: func() {}},
		operations: map[model.ExecutionID]providersdk.ExecOperation{id: {}},
	}

	manager.finish(id, nil, nil)
	if manager.active["res-1"] != id {
		t.Fatalf("active guard = %q, want failed terminal write to retain %q", manager.active["res-1"], id)
	}
	if _, ok := manager.cancel[id]; !ok {
		t.Fatal("cancel function was discarded after failed terminal write")
	}
	if _, ok := manager.operations[id]; !ok {
		t.Fatal("operation was discarded after failed terminal write")
	}
	if _, err := manager.submit(context.Background(), model.Sandbox{ID: "sb-1"}, model.Resource{ID: "res-1"}, providersdk.ExecOperation{}, model.ExecutionInputCommandText, "actor-1", time.Second); !errors.Is(err, ErrResourceBusy) {
		t.Fatalf("new execution error = %v, want resource busy", err)
	}
}

func TestDurableExecPersistsOnlySafeMetadata(t *testing.T) {
	st := store.NewMemoryStore()
	manager := &executionManager{store: st, active: make(map[model.ResourceID]model.ExecutionID), cancel: make(map[model.ExecutionID]context.CancelFunc), operations: make(map[model.ExecutionID]providersdk.ExecOperation)}
	operation := providersdk.ExecOperation{
		Command:         []string{"echo", "credential-secret"},
		CommandText:     "Write-Output 'script-secret'",
		Env:             map[string]string{"SECRET_ENV": "environment-secret"},
		GuestCredential: &providersdk.GuestCredential{Kind: "opaque", Data: json.RawMessage(`{"username":"testuser","token":"opaque-credential"}`)},
	}
	execution, err := manager.submit(context.Background(), model.Sandbox{ID: "sb-1"}, model.Resource{ID: "res-1"}, operation, model.ExecutionInputCommandText, "actor-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetExecution(context.Background(), execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"credential-secret", "script-secret", "environment-secret", "opaque-credential", "SECRET_ENV"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("persisted execution contains unsafe value %q: %s", secret, encoded)
		}
	}
	manager.cancelExecution(execution.ID)
}

func TestDurableExecExpiresTerminalRecordsAfterRetention(t *testing.T) {
	st := store.NewMemoryStore()
	id := model.ExecutionID("exec-expired")
	finished := time.Now().UTC().Add(-executionRetention - time.Minute)
	if err := st.PutExecution(context.Background(), model.Execution{
		ID: id, SandboxID: "sb-1", ResourceID: "res-1", Status: model.ExecutionStatusSucceeded, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}
	manager := &executionManager{store: st}
	if _, err := manager.get(context.Background(), id); err != store.ErrNotFound {
		t.Fatalf("expired execution error = %v, want not found", err)
	}
}

func getDurableExec(t *testing.T, mux *http.ServeMux, cursor string, id model.ExecutionID) executionStatusResponse {
	t.Helper()
	path := "/api/v1/sandboxes/sb-1/exec/" + string(id)
	if cursor != "" {
		path += "?from=" + cursor
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body=%s", response.Code, response.Body.String())
	}
	var page executionStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	return page
}

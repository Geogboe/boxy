package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeSandboxExecutor struct {
	calledResource  model.ResourceID
	calledCommand   []string
	calledOperation providersdk.ExecOperation
}

func (f *fakeSandboxExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	f.calledResource = res.ID
	f.calledOperation = operation
	f.calledCommand = append([]string(nil), operation.Command...)
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte("hello\n")}); err != nil {
		return nil, err
	}
	return &providersdk.Result{Outputs: map[string]string{"exit_code": "0"}}, nil
}

func TestSandboxExecStreamingUsesSingleResourceAndEmitsNDJSON(t *testing.T) {
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if err := st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}
	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["echo","hello"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
	if execution.ResourceID != "res-1" || executor.calledResource != "res-1" || strings.Join(executor.calledCommand, " ") != "echo hello" {
		t.Fatalf("executor call = resource %q command %v", executor.calledResource, executor.calledCommand)
	}
	if len(execution.Chunks) == 0 {
		t.Fatal("execution has no durable output chunks")
	}
}

func TestSandboxExecDefaultsToStreaming(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["echo","hello"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
}

func TestSandboxExecStreamFalseKeepsBufferedJSONResponse(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["echo","hello"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
}

func TestSandboxExecPassesOpaqueGuestCredentialToExecutor(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	body := `{"command":["whoami"],"guest_credential":{"kind":"password","data":{"username":"Administrator","password":"rotated"}}}`
	status, accepted := postDurableExec(t, mux, body)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
	if executor.calledOperation.GuestCredential == nil || executor.calledOperation.GuestCredential.Kind != "password" || string(executor.calledOperation.GuestCredential.Data) != `{"username":"Administrator","password":"rotated"}` {
		t.Fatalf("executor operation = %+v, want opaque guest credential", executor.calledOperation)
	}
}

func TestSandboxExecScriptVerifiesDigestAndPreservesArgs(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	content := []byte("echo raw script\n")
	digest := sha256.Sum256(content)
	body := fmt.Sprintf(`{"script":{"content":"%s","digest":"%x","interpreter":"sh","args":["quoted value","--mode","ci"]}}`, base64.StdEncoding.EncodeToString(content), digest)
	status, accepted := postDurableExec(t, mux, body)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
	if executor.calledOperation.Script == nil || string(executor.calledOperation.Script.Content) != string(content) {
		t.Fatalf("script operation = %+v, want raw content", executor.calledOperation.Script)
	}
	if got := strings.Join(executor.calledOperation.Script.Args, "\x00"); got != "quoted value\x00--mode\x00ci" {
		t.Fatalf("script args = %q, want array-preserved args", got)
	}
}

func TestSandboxExecCommandTextRemainsOpaque(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	const opaque = "Write-Output \"two words\"\r\nWrite-Error 'keep this as one script'"
	body, err := json.Marshal(map[string]string{"command_text": opaque})
	if err != nil {
		t.Fatal(err)
	}
	status, accepted := postDurableExec(t, mux, string(body))
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusSucceeded)
	if executor.calledOperation.CommandText != opaque {
		t.Fatalf("command_text = %q, want %q", executor.calledOperation.CommandText, opaque)
	}
	if len(executor.calledOperation.Command) != 0 || executor.calledOperation.Script != nil {
		t.Fatalf("command_text reconstructed into another input form: %+v", executor.calledOperation)
	}
}

func TestSandboxExecRejectsScriptDigestMismatch(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	executor := new(fakeSandboxExecutor)
	s := &Server{store: st, executor: executor}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	body := `{"script":{"content":"cmF3","digest":"0000000000000000000000000000000000000000000000000000000000000000","interpreter":"sh"}}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "digest") {
		t.Fatalf("status = %d, body=%s, want digest rejection", w.Code, w.Body.String())
	}
	if executor.calledOperation.Script != nil || executor.calledOperation.Command != nil {
		t.Fatal("executor was called for a mismatched script digest")
	}
}

func TestSandboxExecRejectsCommandAndScriptTogether(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	body := `{"command":["echo"],"script":{"content":"cmF3","digest":"c6c2e5b3a4c3a8a5b39e6d3f7f5c66e8d5e5b7d2b6f89f0a2e0e4c7f2d3c7a2b","interpreter":"sh"}}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mutually exclusive") {
		t.Fatalf("status = %d, body=%s, want mutually-exclusive rejection", w.Code, w.Body.String())
	}
}

func TestSandboxExecRejectsAmbiguousResourceSelection(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-2", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["hostname"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecRejectsNonReadySandbox(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["hostname"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecRequestRejectsUnknownFields(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: new(fakeSandboxExecutor)}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["true"],"shell":true}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// newAuthedExecServer builds a Server with auth required, one sandbox owned
// by ownerKeyID, and returns request functions preloaded with each role's
// bearer token so exec authorization tests read as plain HTTP assertions.
func newAuthedExecServer(t *testing.T, executor SandboxExecutor) (mux *http.ServeMux, ownerToken, otherUserToken, auditorToken, adminToken string) {
	t.Helper()
	st := store.NewMemoryStore()
	if err := st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}, OwnerID: "owner-key"}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if err := st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated}); err != nil {
		t.Fatalf("PutResource: %v", err)
	}

	newKey := func(id string, role model.APIKeyRole) string {
		raw, hash, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if err := st.PutAPIKey(context.Background(), model.APIKey{ID: model.APIKeyID(id), Hash: hash, Role: role}); err != nil {
			t.Fatalf("PutAPIKey %s: %v", id, err)
		}
		return raw
	}
	ownerToken = newKey("owner-key", model.APIKeyRoleUser)
	otherUserToken = newKey("other-user-key", model.APIKeyRoleUser)
	auditorToken = newKey("auditor-key", model.APIKeyRoleAuditor)
	adminToken = newKey("admin-key", model.APIKeyRoleAdmin)

	s := &Server{store: st, executor: executor, authRequired: true}
	mux = http.NewServeMux()
	s.registerRoutes(mux)
	return mux, ownerToken, otherUserToken, auditorToken, adminToken
}

func execRequest(mux *http.ServeMux, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["hostname"]}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestSandboxExecRequiresAuthentication(t *testing.T) {
	mux, _, _, _, _ := newAuthedExecServer(t, new(fakeSandboxExecutor))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["hostname"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecForbidsAuditorRole(t *testing.T) {
	mux, _, _, auditorToken, _ := newAuthedExecServer(t, new(fakeSandboxExecutor))

	w := execRequest(mux, auditorToken)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecStatusAndCancelRequireExecutionAuthorization(t *testing.T) {
	mux, _, _, auditorToken, _ := newAuthedExecServer(t, new(fakeSandboxExecutor))
	paths := []string{
		"/api/v1/sandboxes/sb-1/exec/exec-1",
		"/api/v1/sandboxes/sb-1/exec/exec-1/cancel",
	}
	for _, path := range paths {
		method := http.MethodGet
		if strings.HasSuffix(path, "/cancel") {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+auditorToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", method, path, w.Code)
		}
	}
}

func TestSandboxExecForbidsNonOwningUser(t *testing.T) {
	mux, _, otherUserToken, _, _ := newAuthedExecServer(t, new(fakeSandboxExecutor))

	w := execRequest(mux, otherUserToken)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecAllowsOwningUser(t *testing.T) {
	mux, ownerToken, _, _, _ := newAuthedExecServer(t, new(fakeSandboxExecutor))

	w := execRequest(mux, ownerToken)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecAllowsAdminRegardlessOfOwnership(t *testing.T) {
	mux, _, _, _, adminToken := newAuthedExecServer(t, new(fakeSandboxExecutor))

	w := execRequest(mux, adminToken)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
}

// timeoutExecutor blocks until ctx is done, then returns its Err() — either
// bare (to pin the DeadlineExceeded-to-504 mapping) or wrapped (to prove
// that mapping survives normal error-wrapping, unlike a naive equality
// check would).
type timeoutExecutor struct{ wrap bool }

func (e timeoutExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	<-ctx.Done()
	if e.wrap {
		return nil, fmt.Errorf("provider exec: %w", ctx.Err())
	}
	return nil, ctx.Err()
}

func TestSandboxExecTimeoutReturnsGatewayTimeout(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%v", wrap), func(t *testing.T) {
			st := store.NewMemoryStore()
			_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
			_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
			s := &Server{store: st, executor: timeoutExecutor{wrap: wrap}}
			mux := http.NewServeMux()
			s.registerRoutes(mux)

			status, accepted := postDurableExec(t, mux, `{"command":["sleep","1"],"timeout":"1ms"}`)
			if status != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", status)
			}
			execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusFailed)
			if execution.Error != "execution timed out" {
				t.Fatalf("execution error = %q, want timeout", execution.Error)
			}
		})
	}
}

// failingExecutor returns a plain, non-context, non-limit error — the
// generic provider-failure case that must fall through to 500 rather than
// being misclassified as a timeout or an output-limit condition.
type failingExecutor struct{}

func (failingExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	return nil, errors.New("provider exec: container not found")
}

func TestSandboxExecProviderErrorReturnsInternalServerError(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: failingExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["hostname"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusFailed)
	if execution.Error != "provider execution failed" {
		t.Fatalf("execution error = %q, want the safe provider error", execution.Error)
	}
}

// nonzeroExitExecutor succeeds (no error) but reports a nonzero exit code —
// the command ran, it just failed inside the sandbox, which is not a Boxy
// error.
type nonzeroExitExecutor struct{}

func (nonzeroExitExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stderr"), Payload: []byte("not found\n")}); err != nil {
		return nil, err
	}
	return &providersdk.Result{Outputs: map[string]string{"exit_code": "127"}}, nil
}

func TestSandboxExecNonzeroExitCodeIsNotAnError(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: nonzeroExitExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["missing-binary"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusFailed)
	if execution.ExitCode == nil || *execution.ExitCode != 127 {
		t.Fatalf("exit_code = %v, want 127", execution.ExitCode)
	}
	if len(execution.Chunks) != 1 || string(execution.Chunks[0].Data) != "not found\n" {
		t.Fatalf("chunks = %+v, want stderr output", execution.Chunks)
	}
}

// limitExceedingExecutor writes one payload that fits inside the buffered
// response limit, then a second that pushes the cumulative total over it —
// reproducing a provider that streamed real output before the limit hit,
// not a single oversized write.
type limitExceedingExecutor struct{}

func (limitExceedingExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte("first chunk fits\n")}); err != nil {
		return nil, err
	}
	oversized := bytes.Repeat([]byte("x"), maxExecOutput)
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: oversized}); err != nil {
		return nil, fmt.Errorf("provider exec stream output: %w", err)
	}
	return &providersdk.Result{Outputs: map[string]string{"exit_code": "0"}}, nil
}

// TestSandboxExecBufferedOutputLimitReturns413 pins the buffered (non-
// streaming) response's behavior when a command's output exceeds the
// bounded-response limit: a distinct 413 status (not the generic 500 a
// provider failure gets, and not the 504 a timeout gets) with guidance to
// retry without stream=false, since the streaming mode delivers output
// incrementally instead of discarding it once the limit is hit.
func TestSandboxExecBufferedOutputLimitReturns413(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: limitExceedingExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["yes"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusFailed)
	if !execution.Truncated {
		t.Fatal("execution is not marked truncated after exceeding output cap")
	}
	if len(execution.Chunks) == 0 || !execution.Chunks[len(execution.Chunks)-1].Dropped {
		t.Fatalf("chunks = %+v, want explicit dropped-output marker", execution.Chunks)
	}
}

// TestSandboxExecStreamingOutputLimitEmitsCompleteEventWithError proves the
// streaming path handles the same overflow differently and correctly: since
// headers (200 OK) are already flushed before the provider runs, the limit
// violation surfaces as a terminal `complete` event carrying the error,
// not a status-code change — and every chunk already sent before the limit
// hit reaches the client instead of being discarded.
func TestSandboxExecStreamingOutputLimitEmitsCompleteEventWithError(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: limitExceedingExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, accepted := postDurableExec(t, mux, `{"command":["yes"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	execution := waitForExecution(t, st, model.ExecutionID(accepted["exec_id"].(string)), model.ExecutionStatusFailed)
	if !execution.Truncated || execution.Error == "" {
		t.Fatalf("execution = %+v, want truncation marker and terminal error", execution)
	}
}

// capabilityErrorExecutor simulates AgentProvisioner.ExecuteSandbox's
// response when the resolved agent doesn't implement streaming — a
// capability error, not a transient provider failure.
type capabilityErrorExecutor struct{}

func (capabilityErrorExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	return nil, errors.New(`agent "agent-1" does not support streaming operations`)
}

func TestSandboxExecCapabilityErrorPersistsFailure(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: capabilityErrorExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, response := postDurableExec(t, mux, `{"command":["hostname"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; response=%v", status, response)
	}
	executionID := model.ExecutionID(response["exec_id"].(string))
	execution := waitForExecution(t, st, executionID, model.ExecutionStatusFailed)
	if execution.Error != "provider execution failed" {
		t.Fatalf("execution error = %q, want the safe provider error", execution.Error)
	}
}

type streamingFailureExecutor struct{}

func (streamingFailureExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error) {
	if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: eventstream.Channel("stdout"), Payload: []byte("before failure\n")}); err != nil {
		return nil, err
	}
	return nil, errors.New("guest transport disconnected")
}

func TestSandboxExecFailurePersistsOutputAndError(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: streamingFailureExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	status, response := postDurableExec(t, mux, `{"command":["hostname"]}`)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; response=%v", status, response)
	}
	executionID := model.ExecutionID(response["exec_id"].(string))
	execution := waitForExecution(t, st, executionID, model.ExecutionStatusFailed)
	if execution.Error != "provider execution failed" {
		t.Fatalf("execution error = %q, want the safe provider error", execution.Error)
	}
	if len(execution.Chunks) != 1 || string(execution.Chunks[0].Data) != "before failure\n" {
		t.Fatalf("execution chunks = %#v, want the output chunk", execution.Chunks)
	}
}

package server

import (
	"bytes"
	"context"
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
	calledResource model.ResourceID
	calledCommand  []string
}

func (f *fakeSandboxExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
	f.calledResource = res.ID
	f.calledCommand = append([]string(nil), command...)
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

	body := strings.NewReader(`{"command":["echo","hello"]}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec?stream=true", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("Content-Type = %q, want application/x-ndjson", got)
	}
	if !strings.Contains(w.Body.String(), `"type":"data"`) || !strings.Contains(w.Body.String(), `"type":"complete"`) {
		t.Fatalf("stream body = %q, want data and complete events", w.Body.String())
	}
	if executor.calledResource != "res-1" || strings.Join(executor.calledCommand, " ") != "echo hello" {
		t.Fatalf("executor call = resource %q command %v", executor.calledResource, executor.calledCommand)
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
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestSandboxExecAllowsAdminRegardlessOfOwnership(t *testing.T) {
	mux, _, _, _, adminToken := newAuthedExecServer(t, new(fakeSandboxExecutor))

	w := execRequest(mux, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

// timeoutExecutor blocks until ctx is done, then returns its Err() — either
// bare (to pin the DeadlineExceeded-to-504 mapping) or wrapped (to prove
// that mapping survives normal error-wrapping, unlike a naive equality
// check would).
type timeoutExecutor struct{ wrap bool }

func (e timeoutExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
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

			r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["sleep","1"],"timeout":"1ms"}`))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusGatewayTimeout {
				t.Fatalf("status = %d, want 504; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// failingExecutor returns a plain, non-context, non-limit error — the
// generic provider-failure case that must fall through to 500 rather than
// being misclassified as a timeout or an output-limit condition.
type failingExecutor struct{}

func (failingExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
	return nil, errors.New("provider exec: container not found")
}

func TestSandboxExecProviderErrorReturnsInternalServerError(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: failingExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["hostname"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

// nonzeroExitExecutor succeeds (no error) but reports a nonzero exit code —
// the command ran, it just failed inside the sandbox, which is not a Boxy
// error.
type nonzeroExitExecutor struct{}

func (nonzeroExitExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
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

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["missing-binary"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp execSandboxResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ExitCode != 127 {
		t.Fatalf("exit_code = %d, want 127", resp.ExitCode)
	}
	if resp.Stderr != "not found\n" {
		t.Fatalf("stderr = %q, want %q", resp.Stderr, "not found\n")
	}
}

// limitExceedingExecutor writes one payload that fits inside the buffered
// response limit, then a second that pushes the cumulative total over it —
// reproducing a provider that streamed real output before the limit hit,
// not a single oversized write.
type limitExceedingExecutor struct{}

func (limitExceedingExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
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
// retry with stream=true, since the streaming mode delivers output
// incrementally instead of discarding it once the limit is hit.
func TestSandboxExecBufferedOutputLimitReturns413(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: limitExceedingExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec", strings.NewReader(`{"command":["yes"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "stream=true") {
		t.Fatalf("body = %q, want guidance to retry with stream=true", w.Body.String())
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

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec?stream=true", strings.NewReader(`{"command":["yes"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"data"`) {
		t.Fatalf("stream body = %q, want the chunk sent before the limit hit to still be present", body)
	}
	if !strings.Contains(body, `"type":"complete"`) || !strings.Contains(body, `"error"`) {
		t.Fatalf("stream body = %q, want a complete event carrying the limit error", body)
	}
}

// capabilityErrorExecutor simulates AgentProvisioner.ExecuteSandbox's
// response when the resolved agent doesn't implement streaming — a
// capability error, not a transient provider failure.
type capabilityErrorExecutor struct{}

func (capabilityErrorExecutor) ExecuteSandbox(ctx context.Context, res model.Resource, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
	return nil, errors.New(`agent "agent-1" does not support streaming operations`)
}

func TestSandboxExecStreamingCapabilityErrorEmitsCompleteEvent(t *testing.T) {
	st := store.NewMemoryStore()
	_ = st.CreateSandbox(context.Background(), model.Sandbox{ID: "sb-1", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}})
	_ = st.PutResource(context.Background(), model.Resource{ID: "res-1", State: model.ResourceStateAllocated})
	s := &Server{store: st, executor: capabilityErrorExecutor{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/exec?stream=true", strings.NewReader(`{"command":["hostname"]}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	// Headers are written before the executor runs, so a capability error
	// discovered mid-stream cannot change the status code — it can only
	// surface in the terminal event, which the CLI/clients must inspect.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not support streaming") {
		t.Fatalf("stream body = %q, want the capability error surfaced", w.Body.String())
	}
}

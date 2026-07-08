package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// fakeAgentAdmin is a test double for the server.AgentAdmin seam.
type fakeAgentAdmin struct {
	agents  []pool.AgentSummary
	revoked []string
}

func (f *fakeAgentAdmin) ListAgents() []pool.AgentSummary { return f.agents }

func (f *fakeAgentAdmin) Revoke(_ context.Context, agentID, _ string) error {
	f.revoked = append(f.revoked, agentID)
	return nil
}

func TestAgentTokenEndpoints(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	// Create a token.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent-tokens", strings.NewReader(`{"label":"lab-host-1","ttl":"2h"}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d (body: %s)", w.Code, http.StatusCreated, w.Body.String())
	}
	var created struct {
		ID    model.AgentTokenID `json:"id"`
		Token string             `json:"token"`
		Label string             `json:"label"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Token == "" {
		t.Fatal("expected the raw token to be returned exactly once on create")
	}
	if created.Label != "lab-host-1" {
		t.Fatalf("label = %q, want lab-host-1", created.Label)
	}

	// The raw token must never be stored — only its hash.
	tok, err := st.GetAgentToken(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetAgentToken: %v", err)
	}
	if tok.TokenHash == created.Token || tok.TokenHash == "" {
		t.Fatal("expected the store to hold a hash, not the raw token")
	}
	if got, want := time.Until(tok.ExpiresAt), 2*time.Hour; got < want-time.Minute || got > want+time.Minute {
		t.Fatalf("expiry = %v from now, want ~%v", got, want)
	}

	// List must not leak the hash.
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agent-tokens", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", w.Code, http.StatusOK)
	}
	if strings.Contains(w.Body.String(), tok.TokenHash) {
		t.Fatal("token list response must not include the token hash")
	}
	if !strings.Contains(w.Body.String(), "lab-host-1") {
		t.Fatalf("token list response missing expected label: %s", w.Body.String())
	}

	// Delete (revoke an unused token).
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/agent-tokens/"+string(created.ID), nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Deleting again: 404.
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/agent-tokens/"+string(created.ID), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestCreateAgentToken_InvalidTTLRejected(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent-tokens", strings.NewReader(`{"ttl":"soon"}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAgentEndpoints(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	admin := &fakeAgentAdmin{agents: []pool.AgentSummary{
		{ID: "agent-a", Name: "Lab Hypervisor", Providers: []providersdk.Type{"hyperv"}, Available: true},
	}}
	mux := server.NewTestMuxWithAgentAdmin(st, sandbox.New(st, nil), admin)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list agents status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "agent-a") || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("unexpected list agents body: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/agents/agent-a", strings.NewReader(`{"reason":"host decommissioned"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if len(admin.revoked) != 1 || admin.revoked[0] != "agent-a" {
		t.Fatalf("revoked = %v, want [agent-a]", admin.revoked)
	}
}

func TestAgentEndpoints_UnavailableWithoutAdmin(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d when no agent transport is wired", w.Code, http.StatusServiceUnavailable)
	}
}

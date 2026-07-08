package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newAgentTestServer(t *testing.T) (*httptest.Server, *agentTestState) {
	t.Helper()

	state := &agentTestState{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/agent-tokens", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Label string `json:"label"`
			TTL   string `json:"ttl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.createdLabel = req.Label
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "tok-id-1",
			"token":      "raw-secret-token",
			"label":      req.Label,
			"expires_at": time.Now().Add(time.Hour).UTC(),
		})
	})
	mux.HandleFunc("GET /api/v1/agent-tokens", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "tok-id-1", "label": "lab-hv-1", "created_at": time.Now().UTC(), "expires_at": time.Now().Add(time.Hour).UTC(), "used": false},
		})
	})
	mux.HandleFunc("DELETE /api/v1/agent-tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			http.Error(w, `{"error":"agent token not found"}`, http.StatusNotFound)
			return
		}
		state.deletedToken = r.PathValue("id")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "agent-a", "name": "Lab HV", "providers": []string{"hyperv"}, "available": true},
		})
	})
	mux.HandleFunc("DELETE /api/v1/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.revokedAgent = r.PathValue("id")
		state.revokeReason = req.Reason
		w.WriteHeader(http.StatusNoContent)
	})

	return httptest.NewServer(mux), state
}

type agentTestState struct {
	createdLabel string
	deletedToken string
	revokedAgent string
	revokeReason string
}

func TestAgentTokenCreate(t *testing.T) {
	srv, state := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "token", "create", "--label", "lab-hv-1", "--ttl", "2h"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "raw-secret-token") {
		t.Fatalf("output = %q, want the raw token printed once", output)
	}
	if state.createdLabel != "lab-hv-1" {
		t.Fatalf("label sent = %q, want lab-hv-1", state.createdLabel)
	}
}

func TestAgentTokenCreate_InvalidTTLFailsBeforeRequest(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "token", "create", "--ttl", "soon"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected an error for an unparseable --ttl")
	}
}

func TestAgentTokenList(t *testing.T) {
	srv, _ := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "token", "list"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "tok-id-1") || !strings.Contains(output, "unused") {
		t.Fatalf("output = %q, want token listing with state", output)
	}
}

func TestAgentTokenRevoke(t *testing.T) {
	srv, state := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "token", "revoke", "tok-id-1"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "revoked token tok-id-1") {
		t.Fatalf("output = %q, want revoke confirmation", output)
	}
	if state.deletedToken != "tok-id-1" {
		t.Fatalf("deleted token = %q, want tok-id-1", state.deletedToken)
	}
}

func TestAgentTokenRevoke_NotFound(t *testing.T) {
	srv, _ := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "token", "revoke", "missing"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected an error revoking an unknown token")
	}
}

func TestAgentList(t *testing.T) {
	srv, _ := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "list"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "agent-a") || !strings.Contains(output, "available") {
		t.Fatalf("output = %q, want agent listing", output)
	}
}

func TestAgentRevoke(t *testing.T) {
	srv, state := newAgentTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"agent", "--server", srv.URL, "revoke", "agent-a", "--reason", "host decommissioned"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "revoked agent agent-a") {
		t.Fatalf("output = %q, want revoke confirmation", output)
	}
	if state.revokedAgent != "agent-a" || state.revokeReason != "host decommissioned" {
		t.Fatalf("revoked = %q reason = %q, want agent-a / host decommissioned", state.revokedAgent, state.revokeReason)
	}
}

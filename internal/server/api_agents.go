package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/internal/agentserver"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// createAgentTokenRequest is the request body for POST /api/v1/agent-tokens.
type createAgentTokenRequest struct {
	Label string `json:"label,omitempty"`
	TTL   string `json:"ttl,omitempty"` // Go duration string; default agentserver.DefaultTokenTTL
}

// createAgentTokenResponse carries the raw token exactly once — it is never
// persisted (only its hash) and can never be retrieved again.
type createAgentTokenResponse struct {
	ID        model.AgentTokenID `json:"id"`
	Token     string             `json:"token"`
	Label     string             `json:"label,omitempty"`
	ExpiresAt time.Time          `json:"expires_at"`
}

// agentTokenSummary is a listing view of a token — deliberately omits the
// hash (and obviously the raw token, which the server doesn't have).
type agentTokenSummary struct {
	ID        model.AgentTokenID `json:"id"`
	Label     string             `json:"label,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	ExpiresAt time.Time          `json:"expires_at"`
	Used      bool               `json:"used"`
}

func (s *Server) handleCreateAgentToken(w http.ResponseWriter, r *http.Request) {
	var req createAgentTokenRequest
	if r.Body != nil && r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			httpjson.Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	var ttl time.Duration
	if req.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(req.TTL)
		if err != nil || ttl <= 0 {
			httpjson.Error(w, http.StatusBadRequest, "invalid ttl: must be a positive Go duration (e.g. \"1h\")")
			return
		}
	}

	raw, tok, err := agentserver.MintToken(r.Context(), s.store, req.Label, ttl)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to create agent token")
		return
	}
	httpjson.Write(w, http.StatusCreated, createAgentTokenResponse{
		ID:        tok.ID,
		Token:     raw,
		Label:     tok.Label,
		ExpiresAt: tok.ExpiresAt,
	})
}

func (s *Server) handleListAgentTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.ListAgentTokens(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list agent tokens")
		return
	}
	out := make([]agentTokenSummary, 0, len(toks))
	for _, tok := range toks {
		out = append(out, agentTokenSummary{
			ID:        tok.ID,
			Label:     tok.Label,
			CreatedAt: tok.CreatedAt,
			ExpiresAt: tok.ExpiresAt,
			Used:      tok.Used(),
		})
	}
	httpjson.Write(w, http.StatusOK, out)
}

func (s *Server) handleDeleteAgentToken(w http.ResponseWriter, r *http.Request) {
	id := model.AgentTokenID(r.PathValue("id"))
	err := s.store.DeleteAgentToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "agent token not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to delete agent token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if s.agentAdmin == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "agent transport not available")
		return
	}
	httpjson.Write(w, http.StatusOK, s.agentAdmin.ListAgents())
}

// revokeAgentRequest is the optional request body for
// DELETE /api/v1/agents/{id}.
type revokeAgentRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleRevokeAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentAdmin == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "agent transport not available")
		return
	}
	var req revokeAgentRequest
	if r.Body != nil && r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			httpjson.Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if err := s.agentAdmin.Revoke(r.Context(), r.PathValue("id"), req.Reason); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to revoke agent")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

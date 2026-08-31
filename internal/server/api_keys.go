package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/google/uuid"
)

type createAPIKeyRequest struct {
	Name    string           `json:"name,omitempty"`
	Role    model.APIKeyRole `json:"role,omitempty"`
	Expires string           `json:"expires,omitempty"`
}

type createAPIKeyResponse struct {
	ID        model.APIKeyID   `json:"id"`
	Key       string           `json:"key"`
	Name      string           `json:"name,omitempty"`
	Role      model.APIKeyRole `json:"role"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

type apiKeySummary struct {
	ID        model.APIKeyID   `json:"id"`
	Name      string           `json:"name,omitempty"`
	Role      model.APIKeyRole `json:"role"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
	RevokedAt *time.Time       `json:"revoked_at,omitempty"`
}

func (s *Server) handleBootstrapAPIKey(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		httpjson.Error(w, http.StatusForbidden, "API-key bootstrap is local-only")
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to inspect API-key bootstrap state")
		return
	}
	if len(keys) != 0 {
		httpjson.Error(w, http.StatusConflict, "API-key bootstrap has already been completed")
		return
	}

	var req createAPIKeyRequest
	if r.Body != nil && r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			httpjson.Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	req.Role = model.APIKeyRoleAdmin
	response, err := s.createAPIKey(r, req)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	var req createAPIKeyRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	response, err := s.createAPIKey(r, req)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (s *Server) createAPIKey(r *http.Request, req createAPIKeyRequest) (createAPIKeyResponse, error) {
	role := req.Role
	if !role.Valid() {
		return createAPIKeyResponse{}, errors.New("role must be one of user, auditor, or admin")
	}
	var expiresAt *time.Time
	if strings.TrimSpace(req.Expires) != "" {
		d, err := time.ParseDuration(req.Expires)
		if err != nil || d <= 0 {
			return createAPIKeyResponse{}, errors.New("expires must be a positive Go duration (e.g. 24h)")
		}
		expires := time.Now().Add(d)
		expiresAt = &expires
	}
	raw, hash, err := auth.GenerateAPIKey()
	if err != nil {
		return createAPIKeyResponse{}, err
	}
	createdAt := time.Now().UTC()
	key := model.APIKey{
		ID:        model.APIKeyID(uuid.NewString()),
		Hash:      hash,
		Role:      role,
		Name:      strings.TrimSpace(req.Name),
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Kind:      model.APIKeyKindService,
	}
	if err := s.store.PutAPIKey(r.Context(), key); err != nil {
		return createAPIKeyResponse{}, err
	}
	return createAPIKeyResponse{
		ID:        key.ID,
		Key:       raw,
		Name:      key.Name,
		Role:      key.Role,
		CreatedAt: key.CreatedAt,
		ExpiresAt: key.ExpiresAt,
	}, nil
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	out, err := s.serviceAPIKeySummaries(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list API keys")
		return
	}
	httpjson.Write(w, http.StatusOK, out)
}

func (s *Server) serviceAPIKeySummaries(ctx context.Context) ([]apiKeySummary, error) {
	keys, err := s.store.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiKeySummary, 0, len(keys))
	for _, key := range keys {
		if key.EffectiveKind() != model.APIKeyKindService {
			continue
		}
		out = append(out, apiKeySummary{
			ID: key.ID, Name: key.Name, Role: key.Role, CreatedAt: key.CreatedAt,
			ExpiresAt: key.ExpiresAt, RevokedAt: key.RevokedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Server) revokeAPIKey(ctx context.Context, id model.APIKeyID) error {
	key, err := s.store.GetAPIKey(ctx, id)
	if err != nil {
		return err
	}
	if key.Revoked() {
		return nil
	}
	now := time.Now().UTC()
	key.RevokedAt = &now
	return s.store.PutAPIKey(ctx, key)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	id := model.APIKeyID(r.PathValue("id"))
	key, err := s.store.GetAPIKey(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "API key not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get API key")
		return
	}
	if key.EffectiveKind() != model.APIKeyKindService {
		httpjson.Error(w, http.StatusNotFound, "API key not found")
		return
	}
	if err := s.revokeAPIKey(r.Context(), key.ID); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to revoke API key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

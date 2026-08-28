package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/google/uuid"
)

type cliOIDCConfigResponse struct {
	Issuer      string `json:"issuer"`
	CLIClientID string `json:"cli_client_id"`
}

// handleCLIOIDCConfig tells `boxy login --oidc` where to run its own
// device-code grant. Deliberately unauthenticated (there's no session or
// API key yet at this point) and deliberately minimal: only the issuer and
// the public CLI client ID, never the confidential web client's secret.
func (s *Server) handleCLIOIDCConfig(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || s.oidc.CLIClientID == "" {
		httpjson.Error(w, http.StatusNotFound, "CLI OIDC login is not configured")
		return
	}
	httpjson.Write(w, http.StatusOK, cliOIDCConfigResponse{
		Issuer:      s.oidc.Issuer,
		CLIClientID: s.oidc.CLIClientID,
	})
}

type oidcExchangeRequest struct {
	IDToken string `json:"id_token"`
}

// handleOIDCKeyExchange mints a self-service personal API key for a
// caller who has just completed `boxy login --oidc`'s device-code grant
// and holds a real ID token as proof of identity, but has no boxy
// credential yet -- this endpoint IS the credential-issuance step, so
// it's deliberately unauthenticated by any existing boxy bearer key,
// exactly like the loopback API-key bootstrap endpoint is unauthenticated
// for the same structural reason. The ID token is independently
// re-verified here (signature, issuer, audience, expiry) with the same
// verifier the web login callback uses; the CLI is not trusted to have
// verified it correctly itself.
func (s *Server) handleOIDCKeyExchange(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || s.oidc.CLIVerifier == nil {
		httpjson.Error(w, http.StatusNotFound, "CLI OIDC login is not configured")
		return
	}
	var req oidcExchangeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.IDToken) == "" {
		httpjson.Error(w, http.StatusBadRequest, "id_token is required")
		return
	}

	idToken, err := s.oidc.CLIVerifier.Verify(r.Context(), req.IDToken)
	if err != nil {
		httpjson.Error(w, http.StatusUnauthorized, "invalid id_token")
		return
	}
	if idToken.Subject == "" {
		httpjson.Error(w, http.StatusUnauthorized, "id_token has no subject")
		return
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to decode id_token claims")
		return
	}
	role := resolveOIDCRole(s.oidc, claims)
	if role == "" {
		httpjson.Error(w, http.StatusForbidden, "no boxy role resolved for this identity")
		return
	}

	resp, err := s.mintPersonalAPIKey(r.Context(), "oidc:"+idToken.Subject, role, "cli-oidc-login", s.oidc.PersonalKeyMaxTTL)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpjson.Write(w, http.StatusCreated, resp)
}

// mintPersonalAPIKey creates a self-service model.APIKeyKindPersonal key
// tied to subject (the stable identity a resource's OwnerID resolves to --
// see docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md's
// Decision 5), expiring after ttl. Shared by the CLI's device-code exchange
// (handleOIDCKeyExchange) and the web UI's profile-page self-service button
// (handleMintPersonalKey) -- both mint the same kind of key, just from a
// different proof of identity (a verified ID token vs. an existing browser
// session).
func (s *Server) mintPersonalAPIKey(ctx context.Context, subject string, role model.APIKeyRole, name string, ttl time.Duration) (createAPIKeyResponse, error) {
	raw, hash, err := auth.GenerateAPIKey()
	if err != nil {
		return createAPIKeyResponse{}, fmt.Errorf("failed to generate key: %w", err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	key := model.APIKey{
		ID:        model.APIKeyID(uuid.NewString()),
		Hash:      hash,
		Role:      role,
		Name:      name,
		CreatedAt: now,
		ExpiresAt: &expiresAt,
		Kind:      model.APIKeyKindPersonal,
		Subject:   subject,
	}
	if err := s.store.PutAPIKey(ctx, key); err != nil {
		return createAPIKeyResponse{}, fmt.Errorf("failed to persist key: %w", err)
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

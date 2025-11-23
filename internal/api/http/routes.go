package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v1/sandboxes", s.handleSandboxes)
	mux.HandleFunc("/api/v1/sandboxes/", s.handleSandbox)
	mux.HandleFunc("/api/v1/pools", s.handlePools)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSandboxes(w, r)
	case http.MethodPost:
		s.createSandbox(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSandbox(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/sandboxes/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]

	// /api/v1/sandboxes/{id}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.getSandbox(w, r, id)
		case http.MethodDelete:
			s.destroySandbox(w, r, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/v1/sandboxes/{id}/extend
	if len(parts) == 2 && parts[1] == "extend" && r.Method == http.MethodPost {
		s.extendSandbox(w, r, id)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var payload CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	req, err := payload.ToDomain()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sb, err := s.sandbox.Create(r.Context(), req)
	if err != nil {
		s.logger.WithError(err).Warn("sandbox creation failed")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := SandboxResponseFromDomain(sb, nil)
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := s.sandbox.List(r.Context())
	if err != nil {
		http.Error(w, "failed to list sandboxes", http.StatusInternalServerError)
		return
	}

	var resp []*SandboxResponse
	for _, sb := range sandboxes {
		resp = append(resp, SandboxResponseFromDomain(sb, nil))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request, id string) {
	sb, err := s.sandbox.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}

	// Optionally include resources with connection info
	var resources []*ResourceResponse
	if res, err := s.sandbox.GetResourcesForSandbox(r.Context(), id); err == nil {
		resources = ResourcesToResponse(res)
	} else {
		s.logger.WithError(err).WithField("sandbox_id", id).Warn("failed to fetch resources for sandbox")
	}

	resp := SandboxResponseFromDomain(sb, resources)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) destroySandbox(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.sandbox.Destroy(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) extendSandbox(w http.ResponseWriter, r *http.Request, id string) {
	var payload ExtendSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	dur, err := time.ParseDuration(payload.Duration)
	if err != nil {
		http.Error(w, "invalid duration", http.StatusBadRequest)
		return
	}

	if err := s.sandbox.Extend(r.Context(), id, dur); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats, err := s.pools.List()
	if err != nil {
		http.Error(w, "failed to list pools", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

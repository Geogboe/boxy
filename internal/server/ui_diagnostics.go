package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/diagnostics"
)

func (s *Server) handleRequestAgentLogsUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUIAdmin(w, r); !ok || !requireUICSRF(w, r) {
		return
	}
	if s.agentAdmin == nil {
		http.Error(w, "agent transport not available", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if strings.TrimSpace(agentID) == "" {
		http.Error(w, "agent id is required", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request form", http.StatusBadRequest)
		return
	}
	rawSince := strings.TrimSpace(r.FormValue("since"))
	var since time.Time
	if rawSince != "" {
		var err error
		since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil {
			http.Error(w, "since must be an RFC3339 timestamp", http.StatusBadRequest)
			return
		}
	}
	limit := diagnostics.HardMaxLimit
	if rawLimit := strings.TrimSpace(r.FormValue("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > diagnostics.HardMaxLimit {
			http.Error(w, fmt.Sprintf("limit must be between 1 and %d", diagnostics.HardMaxLimit), http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	requestID, err := s.agentAdmin.RequestAgentLogs(r.Context(), agentID, since, limit)
	if err != nil {
		http.Error(w, "agent is not connected", http.StatusServiceUnavailable)
		return
	}
	values := url.Values{"agent": {agentID}, "log_request": {requestID}}
	if rawSince != "" {
		values.Set("since", rawSince)
	}
	http.Redirect(w, r, "/ui/diagnostics?"+values.Encode(), http.StatusSeeOther)
}

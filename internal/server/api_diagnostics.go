package server

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
)

func (s *Server) handleListDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	if s.diagnostics == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "diagnostics are unavailable")
		return
	}
	query, audit, err := parseDiagnosticsQuery(r)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.diagnostics.Query(r.Context(), query)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to query diagnostics")
		return
	}
	audit.ResultCount = len(page.Events)
	if s.audit != nil {
		if err := s.audit.RecordDiagnosticsQuery(r.Context(), audit); err != nil {
			httpjson.Error(w, http.StatusInternalServerError, "failed to record diagnostics query")
			return
		}
	}
	httpjson.Write(w, http.StatusOK, page)
}

func (s *Server) handleExportDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	if s.diagnostics == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "diagnostics are unavailable")
		return
	}
	query, audit, err := parseDiagnosticsQuery(r)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.diagnostics.Query(r.Context(), query)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to query diagnostics")
		return
	}
	archive, err := diagnostics.BuildExport(page.Events, diagnostics.ExportOptions{
		GeneratedAt: time.Now().UTC(),
		Components: []diagnostics.ComponentSpec{
			{Name: "control-plane", Description: "Boxy server and reconciliation diagnostics"},
			{Name: "agent", Description: "authenticated provider-agent diagnostics"},
		},
	})
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to build diagnostics export")
		return
	}
	audit.ResultCount = len(page.Events)
	if s.audit != nil {
		if err := s.audit.RecordDiagnosticsQuery(r.Context(), audit); err != nil {
			httpjson.Error(w, http.StatusInternalServerError, "failed to record diagnostics query")
			return
		}
	}
	var body bytes.Buffer
	if err := diagnostics.WriteExport(&body, archive); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to encode diagnostics export")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="boxy-diagnostics.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func parseDiagnosticsQuery(r *http.Request) (diagnostics.Query, diagnostics.QueryAudit, error) {
	values := r.URL.Query()
	query := diagnostics.Query{
		Level:     strings.ToUpper(strings.TrimSpace(values.Get("level"))),
		Component: strings.TrimSpace(values.Get("component")),
		Pool:      strings.TrimSpace(values.Get("pool")),
		Agent:     strings.TrimSpace(values.Get("agent")),
		Resource:  strings.TrimSpace(values.Get("resource")),
		Cursor:    strings.TrimSpace(values.Get("cursor")),
	}
	if query.Level != "" {
		switch query.Level {
		case "DEBUG", "INFO", "WARN", "ERROR":
		default:
			return diagnostics.Query{}, diagnostics.QueryAudit{}, errors.New("level must be debug, info, warn, or error")
		}
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return diagnostics.Query{}, diagnostics.QueryAudit{}, errors.New("since must be an RFC3339 timestamp")
		}
		query.Since = since.UTC()
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return diagnostics.Query{}, diagnostics.QueryAudit{}, errors.New("limit must be a positive integer")
		}
		query.Limit = limit
	}
	if query.Limit == 0 {
		query.Limit = diagnostics.DefaultLimit
	}
	if query.Limit < 1 || query.Limit > diagnostics.HardMaxLimit {
		return diagnostics.Query{}, diagnostics.QueryAudit{}, errors.New("limit must be between 1 and 1000")
	}
	principal := principalFromRequest(r)
	actor := principal.Subject
	if actor == "" {
		actor = string(principal.KeyID)
	}
	if actor == "" {
		actor = "local"
	}
	audit := diagnostics.QueryAudit{
		Actor: actor, Level: query.Level, Component: query.Component,
		Pool: query.Pool, Agent: query.Agent, Resource: query.Resource,
		Limit: query.Limit,
	}
	if !query.Since.IsZero() {
		audit.Since = query.Since.Format(time.RFC3339Nano)
	}
	return query, audit, nil
}

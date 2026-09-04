package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
)

func (s *Server) ensureJobRunner() (*jobs.Runner, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.jobRunner != nil {
		return s.jobRunner, nil
	}
	if s.store == nil {
		return nil, fmt.Errorf("job store is not configured")
	}
	runner, err := jobs.NewRunner(jobs.Config{Store: s.store})
	if err != nil {
		return nil, err
	}
	s.jobRunner = runner
	return runner, nil
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	runner, err := s.ensureJobRunner()
	if err != nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "job service is not available")
		return
	}
	job, err := runner.Get(r.Context(), jobs.ID(r.PathValue("id")))
	if errors.Is(err, jobs.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}
	httpjson.Write(w, http.StatusOK, job)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	runner, err := s.ensureJobRunner()
	if err != nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "job service is not available")
		return
	}
	job, err := runner.Cancel(r.Context(), jobs.ID(r.PathValue("id")))
	if errors.Is(err, jobs.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusConflict, err.Error())
		return
	}
	httpjson.Write(w, http.StatusAccepted, job)
}

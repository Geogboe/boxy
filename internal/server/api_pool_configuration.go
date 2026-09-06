package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type updatePoolConfigurationRequest struct {
	MinReady int    `json:"min_ready"`
	MaxTotal int    `json:"max_total"`
	MaxAge   string `json:"max_age,omitempty"`
}

func validatePoolConfiguration(req updatePoolConfigurationRequest) (string, error) {
	if req.MinReady < 0 || req.MaxTotal < 0 {
		return "", errors.New("min_ready and max_total must not be negative")
	}
	if req.MaxTotal == 0 && req.MinReady > 0 {
		return "", errors.New("max_total 0 drains the pool, so min_ready must also be 0")
	}
	if strings.TrimSpace(req.MaxAge) == "" {
		return "", nil
	}
	maxAge, err := time.ParseDuration(strings.TrimSpace(req.MaxAge))
	if err != nil || maxAge < 0 {
		return "", errors.New("max_age must be a non-negative duration such as 24h")
	}
	return maxAge.String(), nil
}

func (s *Server) handleUpdatePoolConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	var req updatePoolConfigurationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pool, err := s.applyPoolConfiguration(r.Context(), model.PoolName(r.PathValue("name")), req)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "pool not found")
		return
	}
	if err != nil {
		var busy *jobs.TargetBusyError
		if errors.As(err, &busy) {
			httpjson.Error(w, http.StatusConflict, busy.Error())
			return
		}
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.Write(w, http.StatusOK, pool)
}

func (s *Server) applyPoolConfiguration(ctx context.Context, name model.PoolName, req updatePoolConfigurationRequest) (model.Pool, error) {
	maxAge, err := validatePoolConfiguration(req)
	if err != nil {
		return model.Pool{}, err
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	allJobs, err := s.store.List(ctx)
	if err != nil {
		return model.Pool{}, fmt.Errorf("list pool jobs: %w", err)
	}
	target := "pool:" + string(name)
	for _, job := range allJobs {
		if job.Target == target && !job.Status.IsTerminal() {
			return model.Pool{}, &jobs.TargetBusyError{Target: target, ActiveJobID: job.ID}
		}
	}

	pool, err := s.store.GetPool(ctx, name)
	if err != nil {
		return model.Pool{}, err
	}
	pool.Policies.Preheat.MinReady = req.MinReady
	pool.Policies.Preheat.MaxTotal = req.MaxTotal
	pool.Policies.Recycle.MaxAge = maxAge
	pool.Drain.ConfigDeclared = req.MaxTotal == 0
	pool.Configuration.Provenance = "web"
	pool.Configuration.Pending = true
	pool.Configuration.UpdatedAt = time.Now().UTC()
	if err := s.store.PutPool(ctx, pool); err != nil {
		return model.Pool{}, fmt.Errorf("save pool configuration: %w", err)
	}

	pending := true
	if reconciler, ok := s.poolMaintenance.(interface {
		Reconcile(context.Context, model.PoolName) error
	}); ok {
		pending = reconciler.Reconcile(ctx, name) != nil
	}
	latest, err := s.store.GetPool(ctx, name)
	if err != nil {
		return model.Pool{}, err
	}
	latest.Configuration = pool.Configuration
	latest.Configuration.Pending = pending
	if err := s.store.PutPool(ctx, latest); err != nil {
		return model.Pool{}, fmt.Errorf("record pool configuration state: %w", err)
	}
	return latest, nil
}

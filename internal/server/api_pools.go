package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/jobs"
	"github.com/Geogboe/boxy/pkg/model"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

// logPoolJobStep emits a structured diagnostics event for one pool job step.
// component/operation/job/pool/resource/step/status/attempt/error_code are
// all attribute keys pkg/diagnostics/handler.go's safeField recognizes, so
// this reaches the diagnostics store automatically through the process-wide
// slog default boxy serve installs (see internal/cli/serve.go) -- no direct
// dependency on pkg/diagnostics is needed here. kind is the job kind (e.g.
// "pool.fill"); resourceID is empty for a pool-level job.
func logPoolJobStep(kind string, jobID jobs.ID, poolName model.PoolName, resourceID model.ResourceID, step jobs.Step) {
	level := slog.LevelInfo
	if step.Status == jobs.StepFailed {
		level = slog.LevelWarn
	}
	attrs := []any{
		"component", "pool", "operation", kind, "job", string(jobID),
		"pool", string(poolName), "step", step.Code, "status", string(step.Status),
	}
	if resourceID != "" {
		attrs = append(attrs, "resource", string(resourceID))
	}
	if step.Attempt > 0 {
		attrs = append(attrs, "attempt", step.Attempt)
	}
	if step.ErrorCode != "" {
		attrs = append(attrs, "error_code", step.ErrorCode)
	}
	slog.Log(context.Background(), level, "pool job step", attrs...)
}

// registerAPIRoutes wires the JSON REST API endpoints into the mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/api-keys/bootstrap", s.handleBootstrapAPIKey)
	mux.HandleFunc("POST /api/v1/api-keys/oidc-exchange", s.handleOIDCKeyExchange)
	mux.HandleFunc("POST /api/v1/api-keys", s.handleCreateAPIKey)
	mux.HandleFunc("GET /api/v1/api-keys", s.handleListAPIKeys)
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", s.handleRevokeAPIKey)
	mux.HandleFunc("GET /api/v1/pools", s.handleListPools)
	mux.HandleFunc("GET /api/v1/pools/{name}", s.handleGetPool)
	mux.HandleFunc("PUT /api/v1/pools/{name}/configuration", s.handleUpdatePoolConfiguration)
	mux.HandleFunc("POST /api/v1/pools/{name}/drain", s.handleDrainPool)
	mux.HandleFunc("POST /api/v1/pools/{name}/fill", s.handleFillPool)
	mux.HandleFunc("POST /api/v1/pools/{name}/resources/{id}/retry", s.handleRetryPoolResource)
	mux.HandleFunc("DELETE /api/v1/pools/{name}/resources/{id}", s.handleDestroyPoolResource)
	mux.HandleFunc("POST /api/v1/pools/{name}/guest-credential", s.handleSetPoolGuestCredential)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.handleCancelJob)
	mux.HandleFunc("GET /api/v1/resources", s.handleListResources)
	mux.HandleFunc("GET /api/v1/resources/{id}", s.handleGetResource)
	mux.HandleFunc("POST /api/v1/resources/purge", s.handlePurgeResources)
	mux.HandleFunc("GET /api/v1/sandboxes", s.handleListSandboxes)
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", s.handleGetSandbox)
	mux.HandleFunc("POST /api/v1/sandboxes", s.handleCreateSandbox)
	mux.HandleFunc("DELETE /api/v1/sandboxes/{id}", s.handleDeleteSandbox)
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/extend", s.handleExtendSandbox)
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/exec", s.handleSandboxExec)
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/exec/{exec_id}", s.handleGetSandboxExecution)
	mux.HandleFunc("POST /api/v1/sandboxes/{id}/exec/{exec_id}/cancel", s.handleCancelSandboxExecution)
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/guest-credential", s.handleGuestCredential)
	mux.HandleFunc("POST /api/v1/agent-tokens", s.handleCreateAgentToken)
	mux.HandleFunc("GET /api/v1/agent-tokens", s.handleListAgentTokens)
	mux.HandleFunc("DELETE /api/v1/agent-tokens/{id}", s.handleDeleteAgentToken)
	mux.HandleFunc("GET /api/v1/agents", s.handleListAgents)
	mux.HandleFunc("DELETE /api/v1/agents/{id}", s.handleRevokeAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/logs", s.handleRequestAgentLogs)
	mux.HandleFunc("GET /api/v1/diagnostics/logs", s.handleListDiagnostics)
	mux.HandleFunc("GET /api/v1/diagnostics/export", s.handleExportDiagnostics)
}

type setPoolGuestCredentialRequest struct {
	Value string `json:"value"`
}

func (s *Server) handleSetPoolGuestCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	name := model.PoolName(r.PathValue("name"))
	if _, err := s.store.GetPool(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "pool not found")
		return
	} else if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get pool")
		return
	}

	var req setPoolGuestCredentialRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		httpjson.Error(w, http.StatusBadRequest, "guest credential value must not be blank")
		return
	}
	if s.guestSecrets != nil {
		if err := s.guestSecrets.Put(r.Context(), boxysecrets.PoolBootstrapKey(string(name)), []byte(req.Value)); err != nil {
			httpjson.Error(w, http.StatusInternalServerError, "failed to store pool guest credential")
			return
		}
	} else if err := s.store.PutPoolGuestCredential(r.Context(), name, req.Value); err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to store pool guest credential")
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]any{"pool": name, "configured": true})
}

// handleListPools returns all pools as JSON.
func (s *Server) handleListPools(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	pools, err := s.store.ListPools(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list pools")
		return
	}
	httpjson.Write(w, http.StatusOK, pools)
}

// handleGetPool returns a single pool by name.
func (s *Server) handleGetPool(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	name := model.PoolName(r.PathValue("name"))
	pool, err := s.store.GetPool(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "pool not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get pool")
		return
	}
	httpjson.Write(w, http.StatusOK, pool)
}

func (s *Server) handleDrainPool(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	if s.poolMaintenance == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "pool maintenance is not available")
		return
	}
	s.submitPoolMaintenanceJob(w, r, "pool.drain", model.PoolName(r.PathValue("name")), s.poolMaintenance.Drain)
}

func (s *Server) handleFillPool(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	if s.poolMaintenance == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "pool maintenance is not available")
		return
	}
	s.submitPoolMaintenanceJob(w, r, "pool.fill", model.PoolName(r.PathValue("name")), s.poolMaintenance.Fill)
}

func (s *Server) handleRetryPoolResource(w http.ResponseWriter, r *http.Request) {
	s.handlePoolResourceJob(w, r, "pool.retry", func(ctx context.Context, maintenance PoolResourceMaintenance, resource model.Resource) error {
		return maintenance.RetryResource(ctx, resource)
	})
}

func (s *Server) handleDestroyPoolResource(w http.ResponseWriter, r *http.Request) {
	s.handlePoolResourceJob(w, r, "pool.destroy", func(ctx context.Context, maintenance PoolResourceMaintenance, resource model.Resource) error {
		if err := maintenance.DestroyResource(ctx, resource); err != nil {
			return err
		}
		if reconciler, ok := s.poolMaintenance.(interface {
			Reconcile(context.Context, model.PoolName) error
		}); ok {
			return reconciler.Reconcile(ctx, resource.OriginPool)
		}
		return nil
	})
}

func (s *Server) handlePoolResourceJob(w http.ResponseWriter, r *http.Request, kind string, operation func(context.Context, PoolResourceMaintenance, model.Resource) error) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	poolName := model.PoolName(r.PathValue("name"))
	resourceID := model.ResourceID(r.PathValue("id"))
	job, err := s.startPoolResourceJob(r.Context(), kind, poolName, resourceID, operation)
	if err != nil {
		var busy *jobs.TargetBusyError
		if errors.As(err, &busy) {
			httpjson.Error(w, http.StatusConflict, busy.Error())
			return
		}
		httpjson.Error(w, http.StatusInternalServerError, "failed to submit pool resource job")
		return
	}
	httpjson.Write(w, http.StatusAccepted, job)
}

func (s *Server) startPoolResourceJob(ctx context.Context, kind string, poolName model.PoolName, resourceID model.ResourceID, operation func(context.Context, PoolResourceMaintenance, model.Resource) error) (jobs.Job, error) {
	maintenance, ok := s.poolMaintenance.(PoolResourceMaintenance)
	if !ok {
		return jobs.Job{}, fmt.Errorf("pool resource maintenance is not available")
	}
	runner, err := s.ensureJobRunner()
	if err != nil {
		return jobs.Job{}, err
	}
	return runner.Submit(ctx, jobs.Request{Kind: kind, Target: "pool:" + string(poolName)}, jobs.HandlerFuncs{
		RunFunc: func(ctx context.Context, reporter jobs.Reporter) error {
			step := jobs.Step{Code: kind, Subject: string(resourceID), Status: jobs.StepStarted, Attempt: 1}
			if err := reporter.Record(ctx, step); err != nil {
				return err
			}
			logPoolJobStep(kind, reporter.JobID(), poolName, resourceID, step)
			resource, err := s.store.GetResource(ctx, resourceID)
			if err == nil && resource.OriginPool != poolName {
				err = fmt.Errorf("resource does not belong to pool")
			}
			if err == nil {
				err = operation(ctx, maintenance, resource)
			}
			if err != nil {
				step.Status = jobs.StepFailed
				step.ErrorCode = "pool_resource_operation_failed"
				_ = reporter.Record(context.Background(), step)
				logPoolJobStep(kind, reporter.JobID(), poolName, resourceID, step)
				return &jobs.Failure{Code: step.ErrorCode}
			}
			step.Status = jobs.StepSucceeded
			err = reporter.Record(ctx, step)
			logPoolJobStep(kind, reporter.JobID(), poolName, resourceID, step)
			return err
		},
		CleanupFunc: func(ctx context.Context, _ jobs.Reporter) error {
			if reconciler, ok := s.poolMaintenance.(interface {
				Reconcile(context.Context, model.PoolName) error
			}); ok {
				return reconciler.Reconcile(ctx, poolName)
			}
			return nil
		},
	})
}

func (s *Server) submitPoolMaintenanceJob(w http.ResponseWriter, r *http.Request, kind string, poolName model.PoolName, operation func(context.Context, model.PoolName) (model.Pool, error)) {
	job, err := s.startPoolMaintenanceJob(r.Context(), kind, poolName, operation)
	if err != nil {
		var busy *jobs.TargetBusyError
		if errors.As(err, &busy) {
			httpjson.Error(w, http.StatusConflict, busy.Error())
			return
		}
		httpjson.Error(w, http.StatusInternalServerError, "failed to submit pool job")
		return
	}
	httpjson.Write(w, http.StatusAccepted, job)
}

func (s *Server) startPoolMaintenanceJob(ctx context.Context, kind string, poolName model.PoolName, operation func(context.Context, model.PoolName) (model.Pool, error)) (jobs.Job, error) {
	runner, err := s.ensureJobRunner()
	if err != nil {
		return jobs.Job{}, err
	}
	target := "pool:" + string(poolName)
	return runner.Submit(ctx, jobs.Request{Kind: kind, Target: target}, jobs.HandlerFuncs{
		RunFunc: func(ctx context.Context, reporter jobs.Reporter) error {
			step := jobs.Step{Code: kind, Subject: string(poolName), Status: jobs.StepStarted, Attempt: 1}
			if err := reporter.Record(ctx, step); err != nil {
				return err
			}
			logPoolJobStep(kind, reporter.JobID(), poolName, "", step)
			_, err := operation(ctx, poolName)
			if err != nil {
				step.Status = jobs.StepFailed
				step.ErrorCode = poolJobErrorCode(err)
				_ = reporter.Record(context.Background(), step)
				logPoolJobStep(kind, reporter.JobID(), poolName, "", step)
				return &jobs.Failure{Code: step.ErrorCode}
			}
			step.Status = jobs.StepSucceeded
			err = reporter.Record(ctx, step)
			logPoolJobStep(kind, reporter.JobID(), poolName, "", step)
			return err
		},
		CleanupFunc: func(ctx context.Context, _ jobs.Reporter) error {
			if reconciler, ok := s.poolMaintenance.(interface {
				Reconcile(context.Context, model.PoolName) error
			}); ok {
				return reconciler.Reconcile(ctx, poolName)
			}
			return nil
		},
	})
}

func poolJobErrorCode(err error) string {
	var blocked *pool.BlockedPoolError
	if errors.As(err, &blocked) {
		return "quarantine_exhausted"
	}
	var drained *pool.ConfigDeclaredDrainError
	if errors.As(err, &drained) {
		return "pool_config_drained"
	}
	return "pool_operation_failed"
}

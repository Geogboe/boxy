package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

const (
	defaultExecTimeout = 30 * time.Second
	maxExecTimeout     = 5 * time.Minute
	maxExecOutput      = 1 << 20
	maxExecChunk       = 64 << 10
)

type execSandboxRequest struct {
	Command         []string                     `json:"command"`
	CommandText     string                       `json:"command_text,omitempty"`
	Script          *providersdk.ScriptSpec      `json:"script,omitempty"`
	ResourceID      string                       `json:"resource_id,omitempty"`
	Timeout         string                       `json:"timeout,omitempty"`
	GuestCredential *providersdk.GuestCredential `json:"guest_credential,omitempty"`
}

func (s *Server) handleSandboxExec(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleUser, model.APIKeyRoleAdmin) {
		return
	}
	id := model.SandboxID(r.PathValue("id"))
	sb, err := s.store.GetSandbox(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}
	if !s.authorizeSandbox(w, r, sb, true) {
		return
	}
	if sb.Status != model.SandboxStatusReady {
		httpjson.Error(w, http.StatusConflict, "sandbox is not ready for command execution")
		return
	}

	var req execSandboxRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		httpjson.Error(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	inputCount := 0
	if len(req.Command) != 0 {
		inputCount++
	}
	if strings.TrimSpace(req.CommandText) != "" {
		inputCount++
	}
	if req.Script != nil {
		inputCount++
	}
	if inputCount > 1 {
		httpjson.Error(w, http.StatusBadRequest, "command, command_text, and script are mutually exclusive")
		return
	}
	if inputCount != 1 {
		httpjson.Error(w, http.StatusBadRequest, "exactly one of command, command_text, or script is required")
		return
	}
	if req.Script != nil {
		if req.Script.Interpreter == "" {
			req.Script.Interpreter = providersdk.ScriptInterpreterAuto
		}
		if err := req.Script.VerifyDigest(); err != nil {
			httpjson.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if len(req.Command) != 0 && strings.TrimSpace(req.Command[0]) == "" {
		httpjson.Error(w, http.StatusBadRequest, "command must contain at least one non-empty argument")
		return
	}
	resourceID, err := selectExecResource(req.ResourceID, sb.Resources)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	resource, err := s.store.GetResource(r.Context(), resourceID)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox resource not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get sandbox resource")
		return
	}
	if resource.State != model.ResourceStateAllocated {
		httpjson.Error(w, http.StatusConflict, "sandbox resource is not allocated")
		return
	}
	timeout, err := parseExecTimeout(req.Timeout)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	operation := providersdk.ExecOperation{Command: req.Command, CommandText: req.CommandText, Script: req.Script, GuestCredential: req.GuestCredential}
	inputKind := model.ExecutionInputCommand
	if req.Script != nil {
		inputKind = model.ExecutionInputScript
	} else if strings.TrimSpace(req.CommandText) != "" {
		inputKind = model.ExecutionInputCommandText
	}
	execution, err := s.executionService().submit(context.Background(), sb, resource, operation, inputKind, principalFromRequest(r).OwnerIdentity(), timeout)
	if err != nil {
		var busy *resourceBusyError
		if errors.As(err, &busy) {
			writeResourceBusy(w, busy.ExecutionID)
			return
		}
		httpjson.Error(w, http.StatusInternalServerError, "failed to start sandbox command execution")
		return
	}
	httpjson.Write(w, http.StatusAccepted, map[string]any{"exec_id": execution.ID, "status": execution.Status})
}

type executionChunkResponse struct {
	Cursor    string `json:"cursor"`
	Stream    string `json:"stream,omitempty"`
	Data      string `json:"data,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type executionStatusResponse struct {
	ExecID    model.ExecutionID        `json:"exec_id"`
	Status    model.ExecutionStatus    `json:"status"`
	Chunks    []executionChunkResponse `json:"chunks,omitempty"`
	Next      string                   `json:"next"`
	ExitCode  *int                     `json:"exit_code,omitempty"`
	Error     string                   `json:"error,omitempty"`
	Truncated bool                     `json:"truncated,omitempty"`
}

func (s *Server) handleGetSandboxExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleUser, model.APIKeyRoleAdmin) {
		return
	}
	execution, ok := s.executionForRequest(w, r, false)
	if !ok {
		return
	}
	from, err := decodeExecutionCursor(r.URL.Query().Get("from"))
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	response := executionStatusResponse{ExecID: execution.ID, Status: execution.Status, Next: encodeExecutionCursor(from), ExitCode: execution.ExitCode, Error: execution.Error, Truncated: execution.Truncated}
	var pageBytes int
	for _, chunk := range execution.Chunks {
		if chunk.Cursor <= from {
			continue
		}
		if len(response.Chunks) > 0 && pageBytes+len(chunk.Data) > maxExecChunk {
			break
		}
		response.Chunks = append(response.Chunks, executionChunkResponse{
			Cursor: encodeExecutionCursor(chunk.Cursor), Stream: chunk.Stream,
			Data: base64.StdEncoding.EncodeToString(chunk.Data), Truncated: chunk.Dropped,
		})
		pageBytes += len(chunk.Data)
		response.Next = encodeExecutionCursor(chunk.Cursor)
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (s *Server) handleCancelSandboxExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleUser, model.APIKeyRoleAdmin) {
		return
	}
	execution, ok := s.executionForRequest(w, r, true)
	if !ok {
		return
	}
	if err := s.executionService().cancelExecution(execution.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusConflict, "execution cannot be cancelled: "+err.Error())
		return
	}
	httpjson.Write(w, http.StatusAccepted, map[string]any{"exec_id": execution.ID, "status": execution.Status})
}

func (s *Server) executionForRequest(w http.ResponseWriter, r *http.Request, mutate bool) (model.Execution, bool) {
	sandboxID := model.SandboxID(r.PathValue("id"))
	sb, err := s.store.GetSandbox(r.Context(), sandboxID)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox not found")
		return model.Execution{}, false
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get sandbox")
		return model.Execution{}, false
	}
	if !s.authorizeSandbox(w, r, sb, mutate) {
		return model.Execution{}, false
	}
	execID := model.ExecutionID(r.PathValue("exec_id"))
	execution, err := s.executionService().get(r.Context(), execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpjson.Error(w, http.StatusNotFound, "execution not found")
			return model.Execution{}, false
		}
		httpjson.Error(w, http.StatusInternalServerError, "failed to get execution")
		return model.Execution{}, false
	}
	if execution.SandboxID != sandboxID {
		httpjson.Error(w, http.StatusNotFound, "execution not found")
		return model.Execution{}, false
	}
	return execution, true
}

func writeResourceBusy(w http.ResponseWriter, id model.ExecutionID) {
	httpjson.Write(w, http.StatusConflict, map[string]any{
		"error":               "resource_busy",
		"exec_id":             id,
		"active_id":           id,
		"active_execution_id": id,
	})
}

func selectExecResource(requested string, resources []model.ResourceID) (model.ResourceID, error) {
	if strings.TrimSpace(requested) != "" {
		for _, id := range resources {
			if string(id) == requested {
				return id, nil
			}
		}
		return "", errors.New("resource_id is not allocated to this sandbox")
	}
	if len(resources) != 1 {
		return "", errors.New("resource_id is required for a multi-resource sandbox")
	}
	return resources[0], nil
}

func parseExecTimeout(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultExecTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, errors.New("timeout must be a positive Go duration")
	}
	if d > maxExecTimeout {
		return 0, fmt.Errorf("timeout must not exceed %s", maxExecTimeout)
	}
	return d, nil
}

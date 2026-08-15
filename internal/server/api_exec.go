package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/eventstream"
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
	ResourceID      string                       `json:"resource_id,omitempty"`
	Timeout         string                       `json:"timeout,omitempty"`
	GuestCredential *providersdk.GuestCredential `json:"guest_credential,omitempty"`
}

type execSandboxResponse struct {
	ResourceID string `json:"resource_id"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
}

type execStreamEvent struct {
	Type       string            `json:"type"`
	Stream     string            `json:"stream,omitempty"`
	Data       string            `json:"data,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Error      string            `json:"error,omitempty"`
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
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		httpjson.Error(w, http.StatusBadRequest, "command must contain at least one non-empty argument")
		return
	}

	streaming, err := parseExecStreaming(r)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
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
	if s.executor == nil {
		httpjson.Error(w, http.StatusNotImplemented, "sandbox command execution is not available")
		return
	}

	timeout, err := parseExecTimeout(req.Timeout)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if streaming {
		s.handleStreamingExec(w, ctx, resource, providersdk.ExecOperation{Command: req.Command, GuestCredential: req.GuestCredential})
		return
	}
	s.handleBufferedExec(w, ctx, resource, providersdk.ExecOperation{Command: req.Command, GuestCredential: req.GuestCredential})
}

func parseExecStreaming(r *http.Request) (bool, error) {
	value := r.URL.Query().Get("stream")
	if value == "" {
		return false, nil
	}
	streaming, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("stream must be true or false")
	}
	return streaming, nil
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

type boundedEventSink struct {
	publisher *eventstream.Publisher
}

func (s boundedEventSink) Send(ctx context.Context, event eventstream.Event) error {
	switch event.Kind {
	case eventstream.Data:
		return s.publisher.Write(ctx, event.Channel, event.Payload)
	case eventstream.Complete:
		if event.Completion == nil {
			return s.publisher.Complete(ctx, eventstream.Completion{})
		}
		return s.publisher.Complete(ctx, *event.Completion)
	default:
		return fmt.Errorf("unsupported event kind %d", event.Kind)
	}
}

type bufferedExecSink struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func (s *bufferedExecSink) Send(_ context.Context, event eventstream.Event) error {
	if event.Kind != eventstream.Data {
		return nil
	}
	switch event.Channel {
	case eventstream.Channel("stdout"):
		_, _ = s.stdout.Write(event.Payload)
	case eventstream.Channel("stderr"):
		_, _ = s.stderr.Write(event.Payload)
	}
	return nil
}

func (s *Server) handleBufferedExec(w http.ResponseWriter, ctx context.Context, resource model.Resource, operation providersdk.ExecOperation) {
	collector := new(bufferedExecSink)
	publisher := eventstream.NewPublisher(collector, eventstream.Limits{MaxChunkBytes: maxExecChunk, MaxTotalBytes: maxExecOutput})
	result, err := s.executor.ExecuteSandbox(ctx, resource, operation, boundedEventSink{publisher: publisher})
	if err != nil {
		writeExecError(w, err)
		return
	}
	if err := completePublisher(ctx, publisher, result); err != nil {
		writeExecError(w, err)
		return
	}
	response := execSandboxResponse{ResourceID: string(resource.ID), Stdout: collector.stdout.String(), Stderr: collector.stderr.String()}
	response.ExitCode = resultExitCode(result)
	httpjson.Write(w, http.StatusOK, response)
}

func (s *Server) handleStreamingExec(w http.ResponseWriter, ctx context.Context, resource model.Resource, operation providersdk.ExecOperation) {
	stream := &ndjsonExecSink{encoder: json.NewEncoder(w), flusher: http.NewResponseController(w)}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	publisher := eventstream.NewPublisher(stream, eventstream.Limits{MaxChunkBytes: maxExecChunk, MaxTotalBytes: maxExecOutput})
	result, err := s.executor.ExecuteSandbox(ctx, resource, operation, boundedEventSink{publisher: publisher})
	if err != nil {
		_ = stream.Send(ctx, eventstream.Event{Kind: eventstream.Complete, Completion: &eventstream.Completion{Err: err}})
		return
	}
	_ = completePublisher(ctx, publisher, result)
}

type ndjsonExecSink struct {
	encoder *json.Encoder
	flusher *http.ResponseController
}

func (s *ndjsonExecSink) Send(_ context.Context, event eventstream.Event) error {
	out := execStreamEvent{}
	switch event.Kind {
	case eventstream.Data:
		out.Type = "data"
		out.Stream = string(event.Channel)
		out.Data = base64.StdEncoding.EncodeToString(event.Payload)
	case eventstream.Complete:
		out.Type = "complete"
		if event.Completion != nil {
			out.Attributes = event.Completion.Attributes
			if event.Completion.Err != nil {
				out.Error = event.Completion.Err.Error()
			}
			if raw := out.Attributes["exit_code"]; raw != "" {
				if code, err := strconv.Atoi(raw); err == nil {
					out.ExitCode = &code
				}
			}
		}
	default:
		return fmt.Errorf("unsupported event kind %d", event.Kind)
	}
	if err := s.encoder.Encode(out); err != nil {
		return err
	}
	return s.flusher.Flush()
}

func completePublisher(ctx context.Context, publisher *eventstream.Publisher, result *providersdk.Result) error {
	attributes := map[string]string(nil)
	if result != nil {
		attributes = result.Outputs
	}
	err := publisher.Complete(ctx, eventstream.Completion{Attributes: attributes})
	if errors.Is(err, eventstream.ErrCompleted) {
		return nil
	}
	return err
}

func resultExitCode(result *providersdk.Result) int {
	if result == nil {
		return 0
	}
	code, err := strconv.Atoi(result.Outputs["exit_code"])
	if err != nil {
		return 0
	}
	return code
}

func writeExecError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		httpjson.Error(w, http.StatusGatewayTimeout, "sandbox command execution failed: "+err.Error())
	case errors.Is(err, eventstream.ErrLimitExceeded):
		// The buffered response mode has already discarded whatever partial
		// output it collected by the time this error surfaces (the
		// eventstream.Publisher only reports the limit, it doesn't hand
		// back what it already forwarded), so there's nothing partial to
		// return here. Unlike a generic provider failure this is a client
		// choice-of-mode problem, not a server error — 413 says so, and
		// points at the streaming mode that delivers output incrementally
		// instead of buffering it all before responding.
		httpjson.Error(w, http.StatusRequestEntityTooLarge, "sandbox command output exceeded the buffered response limit; retry with stream=true to receive output incrementally instead of buffered")
	default:
		httpjson.Error(w, http.StatusInternalServerError, "sandbox command execution failed: "+err.Error())
	}
}

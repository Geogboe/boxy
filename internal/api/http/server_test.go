package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/pkg/provider"
)

func TestHealthz(t *testing.T) {
	srv := newTestServer(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCreateSandbox(t *testing.T) {
	sb := sandbox.NewSandbox("api-created", time.Hour)

	svc := SandboxServiceFunc{
		CreateFn: func(ctx context.Context, req *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
			return sb, nil
		},
		ListFn:                   func(context.Context) ([]*sandbox.Sandbox, error) { return nil, nil },
		GetFn:                    func(context.Context, string) (*sandbox.Sandbox, error) { return sb, nil },
		DestroyFn:                func(context.Context, string) error { return nil },
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}

	srv := newTestServer(svc, PoolStatFetcher(func() ([]PoolStatus, error) { return nil, nil }))

	body := `{"name":"api-created","duration":"1h","resources":[{"pool":"dev","count":1}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), sb.ID) {
		t.Fatalf("response did not include sandbox id")
	}
}

func TestExtendSandboxInvalidDuration(t *testing.T) {
	svc := SandboxServiceFunc{
		CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
			return sandbox.NewSandbox("x", time.Hour), nil
		},
		ListFn:                   func(context.Context) ([]*sandbox.Sandbox, error) { return nil, nil },
		GetFn:                    func(context.Context, string) (*sandbox.Sandbox, error) { return nil, nil },
		DestroyFn:                func(context.Context, string) error { return nil },
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}
	srv := newTestServer(svc, PoolStatFetcher(func() ([]PoolStatus, error) { return nil, nil }))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", strings.NewReader(`{"duration":"not-a-duration"}`))
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestExtendSandboxSuccess(t *testing.T) {
	svc := SandboxServiceFunc{
		CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
			return sandbox.NewSandbox("x", time.Hour), nil
		},
		ListFn:                   func(context.Context) ([]*sandbox.Sandbox, error) { return nil, nil },
		GetFn:                    func(context.Context, string) (*sandbox.Sandbox, error) { return nil, nil },
		DestroyFn:                func(context.Context, string) error { return nil },
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}
	srv := newTestServer(svc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/sb-1/extend", strings.NewReader(`{"duration":"30m"}`))
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
}

func TestListSandboxes(t *testing.T) {
	sb := sandbox.NewSandbox("list-me", time.Hour)

	svc := SandboxServiceFunc{
		CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) { return sb, nil },
		ListFn: func(context.Context) ([]*sandbox.Sandbox, error) {
			return []*sandbox.Sandbox{sb}, nil
		},
		GetFn:                    func(context.Context, string) (*sandbox.Sandbox, error) { return sb, nil },
		DestroyFn:                func(context.Context, string) error { return nil },
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}

	srv := newTestServer(svc, PoolStatFetcher(func() ([]PoolStatus, error) { return nil, nil }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), sb.ID) {
		t.Fatalf("response missing sandbox id")
	}
}

func TestGetSandboxWithResources(t *testing.T) {
	sb := sandbox.NewSandbox("with-resources", time.Hour)
	res := &sandbox.ResourceWithConnection{
		Resource: &provider.Resource{
			ID:           "res-1",
			PoolID:       "pool-a",
			Type:         provider.ResourceTypeContainer,
			State:        provider.StateReady,
			ProviderType: "docker",
			Metadata:     map[string]interface{}{"ip": "1.1.1.1"},
		},
		Connection: &provider.ConnectionInfo{Type: "ssh", Host: "1.1.1.1", Port: 22},
	}

	svc := SandboxServiceFunc{
		CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) { return sb, nil },
		ListFn:   func(context.Context) ([]*sandbox.Sandbox, error) { return []*sandbox.Sandbox{sb}, nil },
		GetFn: func(context.Context, string) (*sandbox.Sandbox, error) {
			return sb, nil
		},
		DestroyFn: func(context.Context, string) error { return nil },
		ExtendFn:  func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) {
			return []*sandbox.ResourceWithConnection{res}, nil
		},
	}

	srv := newTestServer(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/"+sb.ID, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var parsed SandboxResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(parsed.Details) != 1 {
		t.Fatalf("expected 1 resource detail, got %d", len(parsed.Details))
	}
	if parsed.Details[0].Connection == nil || parsed.Details[0].Connection.Host != "1.1.1.1" {
		t.Fatalf("connection info missing or incorrect")
	}
}

func TestCreateSandboxValidationError(t *testing.T) {
	svc := SandboxServiceFunc{
		CreateFn: func(ctx context.Context, req *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
			return nil, sandbox.ErrNoResourcesRequested
		},
		ListFn:                   func(context.Context) ([]*sandbox.Sandbox, error) { return nil, nil },
		GetFn:                    func(context.Context, string) (*sandbox.Sandbox, error) { return nil, nil },
		DestroyFn:                func(context.Context, string) error { return nil },
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}
	srv := newTestServer(svc, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{"duration":"1h","resources":[]}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty resources, got %d", rr.Code)
	}
}

func TestDestroySandboxErrorBubbles(t *testing.T) {
	svc := SandboxServiceFunc{
		CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) { return nil, nil },
		ListFn:   func(context.Context) ([]*sandbox.Sandbox, error) { return nil, nil },
		GetFn:    func(context.Context, string) (*sandbox.Sandbox, error) { return nil, nil },
		DestroyFn: func(context.Context, string) error {
			return errors.New("cannot destroy")
		},
		ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
		GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
	}
	srv := newTestServer(svc, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-err", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPoolsList(t *testing.T) {
	pools := PoolStatFetcher(func() ([]PoolStatus, error) {
		return []PoolStatus{{Name: "pool-a", Backend: "docker", MinReady: 1}}, nil
	})
	srv := newTestServer(nil, pools)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pools", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "pool-a") {
		t.Fatalf("expected pool name in response, got %s", rr.Body.String())
	}
}

func TestMethodsNotAllowed(t *testing.T) {
	srv := newTestServer(nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/sandboxes", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func newTestServer(svc SandboxService, pools PoolStatsProvider) *Server {
	if svc == nil {
		svc = SandboxServiceFunc{
			CreateFn: func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
				return sandbox.NewSandbox("default", time.Hour), nil
			},
			ListFn: func(context.Context) ([]*sandbox.Sandbox, error) {
				return []*sandbox.Sandbox{}, nil
			},
			GetFn: func(context.Context, string) (*sandbox.Sandbox, error) {
				return sandbox.NewSandbox("default", time.Hour), nil
			},
			DestroyFn:                func(context.Context, string) error { return nil },
			ExtendFn:                 func(context.Context, string, time.Duration) error { return nil },
			GetResourcesForSandboxFn: func(context.Context, string) ([]*sandbox.ResourceWithConnection, error) { return nil, nil },
		}
	}
	if pools == nil {
		pools = PoolStatFetcher(func() ([]PoolStatus, error) { return nil, nil })
	}

	logger := logrus.New()
	logger.SetOutput(bytes.NewBuffer(nil))

	return NewServer(
		":0",
		svc,
		pools,
		logger,
		Timeouts{
			Read:  5 * time.Second,
			Write: 5 * time.Second,
			Idle:  5 * time.Second,
		},
	)
}

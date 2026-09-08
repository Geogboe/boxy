package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminPoolList_success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"name":"web","inventory":{"resources":[{},{}]}}]`)
	})
	mux.HandleFunc("GET /api/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pool", "--server", srv.URL, "list"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "web\t2 ready\t0 quarantined\tmax_total=-") {
		t.Fatalf("output = %q, want pool inventory", output)
	}
}

func TestAdminPools_pluralAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"name":"web","inventory":{"resources":[{}]}}]`)
	})
	mux.HandleFunc("GET /api/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pools", "--server", srv.URL, "list"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "web\t1 ready\t0 quarantined\tmax_total=-") {
		t.Fatalf("output = %q, want plural pool alias success", output)
	}
}

// TestAdminPoolList_reportsQuarantinedAndBlocked covers #328: a pool whose
// quarantined (ResourceStateError) resources alone reach max_total must be
// visibly distinguishable from a converged idle pool -- "0 ready" alone
// looked identical for both cases before this fix.
func TestAdminPoolList_reportsQuarantinedAndBlocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"name":"web","inventory":{"resources":[]},"policies":{"preheat":{"min_ready":2,"max_total":2}}}]`)
	})
	mux.HandleFunc("GET /api/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"id":"r1","origin_pool":"web","state":"error"},{"id":"r2","origin_pool":"web","state":"error"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pool", "--server", srv.URL, "list"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "web\t0 ready\t2 quarantined\tmax_total=2") {
		t.Fatalf("output = %q, want quarantined count and max_total", output)
	}
	if !strings.Contains(output, "BLOCKED") {
		t.Fatalf("output = %q, want a BLOCKED marker when quarantined resources exhaust max_total", output)
	}
}

func TestAdminResourcePurge_success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/resources/purge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := printJSONTo(w, map[string]any{
			"dry_run":         true,
			"force":           false,
			"candidate_count": 2,
			"candidate_ids":   []string{"resource-a", "resource-b"},
			"cleaned_ids":     []string{},
			"skipped_ids":     []any{},
			"errors":          []any{},
		}); err != nil {
			t.Fatalf("encode report: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "resource", "--server", srv.URL, "purge", "--dry-run"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "resource purge: candidates=2 cleaned=0 skipped=0 errors=0 dry-run=true force=false") {
		t.Fatalf("output = %q, want purge report", output)
	}
}

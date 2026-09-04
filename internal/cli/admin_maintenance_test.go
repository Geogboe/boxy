package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

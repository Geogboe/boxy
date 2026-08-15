package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPoolSetGuestCredentialReadsStdinAndDoesNotPrintSecret(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/pools/vm-pool/guest-credential" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		received = body["value"]
		_, _ = io.WriteString(w, `{"pool":"vm-pool","configured":true}`)
	}))
	defer srv.Close()

	cmd := NewRootCommand()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("bootstrap-secret\n"))
	cmd.SetArgs([]string{"pool", "--server", srv.URL, "set-guest-credential", "vm-pool", "--value", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received != "bootstrap-secret" {
		t.Fatalf("received credential = %q, want bootstrap-secret", received)
	}
	if strings.Contains(out.String(), "bootstrap-secret") {
		t.Fatalf("CLI output leaked credential: %s", out.String())
	}
}

func TestPoolSetGuestCredentialRejectsInlineValue(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"pool", "set-guest-credential", "vm-pool", "--value", "inline-secret"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "read from stdin") {
		t.Fatalf("Execute error = %v, want stdin-only validation", err)
	}
}

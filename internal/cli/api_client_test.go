package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWrapConnError_ClassifiesDialFailure(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	_, doErr := client.Do(req) //nolint:bodyclose // Do fails before a body exists
	if doErr == nil {
		t.Fatal("expected a dial error connecting to 127.0.0.1:1")
	}

	wrapped := wrapConnError(doErr, "127.0.0.1:1")
	if !strings.Contains(wrapped.Error(), "boxy serve") {
		t.Fatalf("wrapped error = %v, want mention of `boxy serve`", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "127.0.0.1:1") {
		t.Fatalf("wrapped error = %v, want the unreachable address", wrapped)
	}
	if !errors.Is(wrapped, doErr) {
		t.Fatal("wrapped error should still unwrap to the original dial error")
	}
}

func TestWrapConnError_LeavesNonConnErrorsUnchanged(t *testing.T) {
	orig := errors.New("some other failure")
	if got := wrapConnError(orig, "127.0.0.1:9090"); got != orig { //nolint:err113
		t.Fatalf("wrapConnError changed a non-connection error: %v", got)
	}
}

func TestWrapConnError_Nil(t *testing.T) {
	if wrapConnError(nil, "addr") != nil {
		t.Fatal("expected wrapConnError(nil, ...) to return nil")
	}
}

func TestDoNoContent_WrapsDialFailure(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodDelete, "http://127.0.0.1:1/api/v1/agent-tokens/token-1", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	err = doNoContent(client, req)
	if err == nil {
		t.Fatal("expected a dial error connecting to 127.0.0.1:1")
	}
	if !strings.Contains(err.Error(), "boxy serve") {
		t.Fatalf("error = %v, want a friendly `boxy serve` hint", err)
	}
}

func TestValidatePathID_RejectsEmpty(t *testing.T) {
	if _, err := validatePathID("sandbox id", ""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestValidatePathID_RejectsWhitespaceOnly(t *testing.T) {
	if _, err := validatePathID("sandbox id", "   "); err == nil {
		t.Fatal("expected error for whitespace-only id")
	}
}

func TestValidatePathID_TrimsAndEscapesSlashes(t *testing.T) {
	got, err := validatePathID("sandbox id", " sb/1 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sb%2F1" {
		t.Fatalf("got %q, want escaped id sb%%2F1", got)
	}
}

func TestValidatePathID_AcceptsOrdinaryID(t *testing.T) {
	got, err := validatePathID("sandbox id", "sb-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sb-1" {
		t.Fatalf("got %q, want unchanged id", got)
	}
}

func TestMaintenanceAPIClientHasABoundedTimeout(t *testing.T) {
	// A hung daemon must not block `debug pool drain/fill` forever: the
	// maintenance client needs a timeout, just a longer one than the default
	// client's since drain/fill can legitimately take longer than a status
	// check. It must also be generous enough to survive a realistic worst
	// case: applyDrain destroys pool resources serially, and the Hyper-V
	// driver's Delete waits up to 30s per VM for a transitional power state
	// to clear before tearing down (see ADR-0004) — a pool with just a
	// handful of VMs stuck mid-transition could exceed a short timeout
	// while the drain is still legitimately in progress server-side.
	if maintenanceAPIClient().Timeout == 0 {
		t.Fatal("maintenance client timeout = 0, want a bounded timeout")
	}
	if maintenanceAPIClient().Timeout <= defaultAPIClient().Timeout {
		t.Fatalf("maintenance client timeout = %v, want it longer than the default client's %v", maintenanceAPIClient().Timeout, defaultAPIClient().Timeout)
	}
	if maintenanceAPIClient().Timeout < 5*time.Minute {
		t.Fatalf("maintenance client timeout = %v, want at least 5m to survive a multi-VM serial drain", maintenanceAPIClient().Timeout)
	}
}

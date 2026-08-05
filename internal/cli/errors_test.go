package cli

import (
	"errors"
	"testing"
)

func TestMarkReported_IsReportedRoundTrip(t *testing.T) {
	orig := errors.New("dial tcp 127.0.0.1:9090: connect: connection refused")

	wrapped := MarkReported(orig)

	if !IsReported(wrapped) {
		t.Fatal("expected MarkReported's result to satisfy IsReported")
	}
	if !errors.Is(wrapped, orig) {
		t.Fatal("expected MarkReported's result to still unwrap to the original error")
	}
	if wrapped.Error() != orig.Error() {
		t.Fatalf("wrapped.Error() = %q, want unchanged %q", wrapped.Error(), orig.Error())
	}
}

func TestIsReported_FalseForOrdinaryError(t *testing.T) {
	if IsReported(errors.New("plain error")) {
		t.Fatal("expected an ordinary error to not be marked reported")
	}
}

func TestMarkReported_Nil(t *testing.T) {
	if MarkReported(nil) != nil {
		t.Fatal("expected MarkReported(nil) to return nil")
	}
}

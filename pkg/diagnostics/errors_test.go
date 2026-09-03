package diagnostics

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDescribeErrorClassifiesActionableCategoriesWithoutRawDetails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
		want string
	}{
		{name: "availability", err: errors.New("hyperv query available memory: exit status 1; token=secret"), code: "provider_availability_failed", want: "Hyper-V/WMI available-memory probe exited with an error"},
		{name: "availability denied", err: errors.New("hyperv query available memory: Access is denied"), code: "provider_availability_access_denied", want: "Hyper-V/WMI available-memory probe was denied"},
		{name: "guest auth", err: errors.New("psdirect: broker auth: authentication failed: invalid credentials"), code: "hyperv_guest_authentication_failed", want: "PowerShell Direct guest authentication failed"},
		{name: "timeout", err: context.DeadlineExceeded, code: "operation_timeout", want: "operation timed out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, summary := DescribeError(tt.err)
			if code != tt.code || summary != tt.want {
				t.Fatalf("DescribeError() = (%q, %q), want (%q, %q)", code, summary, tt.code, tt.want)
			}
			if summary == tt.err.Error() || strings.Contains(summary, "secret") {
				t.Fatalf("summary leaked raw error detail: %q", summary)
			}
		})
	}
}

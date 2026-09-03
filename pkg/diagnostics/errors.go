package diagnostics

import (
	"context"
	"errors"
	"strings"
)

// DescribeError returns a stable, non-sensitive category and operator-facing
// summary for an error. Callers should log these fields instead of the raw
// error when the record may be persisted in the diagnostics store.
func DescribeError(err error) (code, summary string) {
	if err == nil {
		return "", ""
	}
	lower := strings.ToLower(err.Error())

	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "context deadline exceeded"):
		return "operation_timeout", "operation timed out"
	case strings.Contains(lower, "hyperv parse available memory"):
		return "provider_availability_parse_failed", "Hyper-V available-memory probe returned invalid output"
	case strings.Contains(lower, "hyperv query available memory") && (strings.Contains(lower, "access is denied") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "unauthorized")):
		return "provider_availability_access_denied", "Hyper-V/WMI available-memory probe was denied"
	case strings.Contains(lower, "hyperv query available memory") && (strings.Contains(lower, "rpc server unavailable") || strings.Contains(lower, "network path") || strings.Contains(lower, "transport")):
		return "provider_availability_transport_failed", "Hyper-V/WMI available-memory probe transport failed"
	case strings.Contains(lower, "hyperv query available memory") && strings.Contains(lower, "exit status"):
		return "provider_availability_failed", "Hyper-V/WMI available-memory probe exited with an error"
	case strings.Contains(lower, "hyperv query available memory") || strings.Contains(lower, "hyperv availability probe"):
		return "provider_availability_failed", "Hyper-V/WMI available-memory probe failed"
	case strings.Contains(lower, "availability probe") || strings.Contains(lower, "availability reporter"):
		return "provider_availability_failed", "provider availability probe failed"
	case strings.Contains(lower, "authentication failed") || strings.Contains(lower, "invalid credentials") || strings.Contains(lower, "broker auth"):
		return "hyperv_guest_authentication_failed", "PowerShell Direct guest authentication failed"
	case strings.Contains(lower, "hvsock connect") || strings.Contains(lower, "powershell direct") || strings.Contains(lower, "psdirect"):
		return "hyperv_guest_operation_failed", "PowerShell Direct guest operation failed"
	case strings.Contains(lower, "capacity") || strings.Contains(lower, "insufficient memory"):
		return "provider_capacity_failed", "provider reported insufficient capacity"
	default:
		return "operation_failed", "operation failed"
	}
}

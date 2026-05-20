package cli

import "testing"

func TestMaintenanceAPIClientHasNoFixedTimeout(t *testing.T) {
	if maintenanceAPIClient().Timeout != 0 {
		t.Fatalf("maintenance client timeout = %v, want no fixed timeout", maintenanceAPIClient().Timeout)
	}
	if defaultAPIClient().Timeout == 0 {
		t.Fatal("default API client should keep a short timeout")
	}
}

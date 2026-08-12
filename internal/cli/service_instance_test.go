// internal/cli/service_instance_test.go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateInstanceName_Empty_Allowed(t *testing.T) {
	if err := validateInstanceName(""); err != nil {
		t.Fatalf("empty instance name (default instance) must be allowed, got: %v", err)
	}
}

func TestValidateInstanceName_ValidCases(t *testing.T) {
	for _, name := range []string{"a", "test1", "lab-hv-1", "A1-b2"} {
		if err := validateInstanceName(name); err != nil {
			t.Errorf("validateInstanceName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateInstanceName_InvalidCases(t *testing.T) {
	for _, name := range []string{
		"-leading-hyphen",
		"has space",
		"has/slash",
		"has_underscore",
		"has.dot",
	} {
		if err := validateInstanceName(name); err == nil {
			t.Errorf("validateInstanceName(%q) = nil, want an error", name)
		}
	}
}

func TestValidateInstanceName_TooLong(t *testing.T) {
	name := strings.Repeat("a", maxInstanceNameLen+1)
	if err := validateInstanceName(name); err == nil {
		t.Fatal("expected an error for an instance name over the length limit")
	}
}

func TestServiceInstanceName_EmptyReturnsBaseUnchanged(t *testing.T) {
	if got := serviceInstanceName("boxy-agent", ""); got != "boxy-agent" {
		t.Errorf("serviceInstanceName(base, \"\") = %q, want unchanged base", got)
	}
}

func TestServiceInstanceName_NamedAppendsSuffix(t *testing.T) {
	if got := serviceInstanceName("boxy-agent", "test1"); got != "boxy-agent-test1" {
		t.Errorf("serviceInstanceName(base, name) = %q, want boxy-agent-test1", got)
	}
}

func TestInstanceDirSuffix(t *testing.T) {
	if got := instanceDirSuffix(""); got != "" {
		t.Errorf("instanceDirSuffix(\"\") = %q, want empty", got)
	}
	if got := instanceDirSuffix("test1"); got != "-test1" {
		t.Errorf("instanceDirSuffix(\"test1\") = %q, want -test1", got)
	}
}

func TestPurgeServiceDataDir_RefusesWithoutServiceYAML(t *testing.T) {
	dir := t.TempDir()
	// No service.yaml written — this directory doesn't look like a boxy
	// service data directory (e.g. it was never created, or points
	// somewhere unrelated because of a mismatched --instance-name).
	err := purgeServiceDataDir(dir)
	if err == nil {
		t.Fatal("expected an error when service.yaml is missing")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("directory should NOT have been removed, but stat failed: %v", statErr)
	}
}

func TestPurgeServiceDataDir_RemovesWhenMarkerPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "service.yaml"), []byte("server: x\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := purgeServiceDataDir(dir); err != nil {
		t.Fatalf("purgeServiceDataDir: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("directory should have been removed")
	}
}

func TestPurgeServiceDataDir_MissingDirIsNoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := purgeServiceDataDir(dir); err != nil {
		t.Fatalf("purging an already-gone directory should be a no-op, got: %v", err)
	}
}

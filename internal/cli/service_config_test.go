// internal/cli/service_config_test.go
package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAgentServiceConfig_SaveLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := agentServiceConfig{
		Server:    "boxy-server:9091",
		Providers: []string{"docker", "hyperv"},
		Token:     "raw-bootstrap-token",
		Name:      "agent-1",
		DataDir:   filepath.Join(dir, ".boxy-agent"),
		LogFile:   filepath.Join(dir, ".boxy-agent", "service.log"),
	}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	got, err := loadAgentServiceConfig(path)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if got.Server != cfg.Server || got.Token != cfg.Token || got.DataDir != cfg.DataDir {
		t.Fatalf("round-tripped config = %+v, want %+v", got, cfg)
	}
}

func TestAgentServiceConfig_TokenIsNotStoredAsPlaintextOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := agentServiceConfig{Server: "s:9091", Providers: []string{"docker"}, Token: "super-secret-token", DataDir: dir, LogFile: filepath.Join(dir, "service.log")}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-token") {
		t.Fatal("service config file must not contain the raw token — it must be base64(EncryptToken(...))-encoded on disk")
	}
}

func TestScrubAgentServiceConfigToken_ClearsTokenButKeepsRestOfConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")
	cfg := agentServiceConfig{Server: "s:9091", Providers: []string{"docker"}, Token: "burn-me", DataDir: dir, LogFile: filepath.Join(dir, "service.log")}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	if err := scrubAgentServiceConfigToken(path); err != nil {
		t.Fatalf("scrubAgentServiceConfigToken: %v", err)
	}

	got, err := loadAgentServiceConfig(path)
	if err != nil {
		t.Fatalf("loadAgentServiceConfig: %v", err)
	}
	if got.Token != "" {
		t.Fatalf("Token = %q after scrub, want empty", got.Token)
	}
	if got.Server != cfg.Server || got.DataDir != cfg.DataDir {
		t.Fatalf("scrub must not touch other fields: got %+v", got)
	}
}

func TestServeServiceConfig_SaveLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	cfg := serveServiceConfig{
		ConfigPath: filepath.Join(dir, "boxy.yaml"),
		Listen:     ":9090",
		UI:         true,
		GRPCListen: ":9091",
		LogFile:    filepath.Join(dir, "service.log"),
	}
	if err := saveServeServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	got, err := loadServeServiceConfig(path)
	if err != nil {
		t.Fatalf("loadServeServiceConfig: %v", err)
	}
	// serveServiceConfig has a []string field (GRPCCertSANs), so it isn't
	// comparable with == / != — use reflect.DeepEqual instead (add
	// "reflect" to this file's imports).
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round-tripped config = %+v, want %+v", got, cfg)
	}
}

func TestResolveAbs_RelativePathBecomesAbsolute(t *testing.T) {
	got, err := resolveAbs("relative/path")
	if err != nil {
		t.Fatalf("resolveAbs: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("resolveAbs(%q) = %q, want an absolute path", "relative/path", got)
	}
}

func TestResolveAbs_EmptyStringStaysEmpty(t *testing.T) {
	got, err := resolveAbs("")
	if err != nil {
		t.Fatalf("resolveAbs: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveAbs(\"\") = %q, want empty (optional fields like --ca-cert may be unset)", got)
	}
}

func TestAgentServiceConfig_EnforcesPermissionsOnRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")

	// Pre-create a file with loose permissions (0o644 = rw-r--r--)
	if err := os.WriteFile(path, []byte("old config"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}
	// Ensure the file is actually loose even under a restrictive umask.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod before rewrite: %v", err)
		}
	}

	// Verify the file has loose permissions before the rewrite
	statBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before rewrite: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := statBefore.Mode() & os.ModePerm; got != 0o644 {
			t.Fatalf("before rewrite, file mode = %#o, want %#o", got, 0o644)
		}
	}
	// Rewrite the file via saveAgentServiceConfig
	cfg := agentServiceConfig{
		Server:    "s:9091",
		Providers: []string{"docker"},
		Token:     "secret-token",
		DataDir:   dir,
		LogFile:   filepath.Join(dir, "service.log"),
	}
	if err := saveAgentServiceConfig(path, cfg); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	// Verify the file now has restricted permissions (0o600)
	statAfter, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after rewrite: %v", err)
	}

	// On Unix-like systems, we can check the exact permission bits.
	// On Windows, permission bits are mostly ignored; only check that the
	// file is accessible (os.Chmod succeeded without error).
	// We only assert the exact mode on systems that honor permission bits.
	const expectedMode os.FileMode = 0o600
	if runtime.GOOS != "windows" {
		actualMode := statAfter.Mode() & os.ModePerm
		if actualMode != expectedMode {
			t.Fatalf("after rewrite, file mode = %#o, want %#o (permissions not enforced on rewrite)", actualMode, expectedMode)
		}
	}

	// On all platforms, verify the file was successfully rewritten
	// (content should be YAML, not the old test content)
	newContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after rewrite: %v", err)
	}
	if string(newContent) == "old config" {
		t.Fatal("file was not rewritten; saveAgentServiceConfig failed to update content")
	}

	_ = statBefore // use statBefore to silence unused variable warning if it exists
}

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func newStatusTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for _, path := range []string{"/api/v1/pools", "/api/v1/sandboxes"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]any{})
		})
	}
	return httptest.NewServer(mux)
}

func TestClientConfigRoundTripAndPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := writeClientConfig(clientConfig{Server: "https://boxy.example:9090"}); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}
	got, err := loadClientConfig()
	if err != nil {
		t.Fatalf("loadClientConfig: %v", err)
	}
	if got.Server != "https://boxy.example:9090" {
		t.Fatalf("Server = %q, want configured endpoint", got.Server)
	}

	path, err := clientConfigPath()
	if err != nil {
		t.Fatalf("clientConfigPath: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("client config: %v", err)
	}
}

func TestLoadClientConfigRejectsUnknownFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := clientConfigPath()
	if err != nil {
		t.Fatalf("clientConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("server: https://boxy.example:9090\nprofile: prod\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadClientConfig(); err == nil {
		t.Fatal("loadClientConfig error = nil, want unknown-field error")
	}
}

func TestResolveClientServerPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := writeClientConfig(clientConfig{Server: "https://global.example:9090"}); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}
	t.Setenv("BOXY_SERVER", "https://env.example:9090")

	cmd := &cobra.Command{}
	cmd.Flags().String("server", "", "")
	if err := cmd.Flags().Set("server", "https://flag.example:9090"); err != nil {
		t.Fatalf("set server flag: %v", err)
	}
	got, err := resolveClientServer(cmd, "https://flag.example:9090")
	if err != nil {
		t.Fatalf("resolveClientServer(flag): %v", err)
	}
	if got != "https://flag.example:9090" {
		t.Fatalf("flag server = %q", got)
	}

	cmd = &cobra.Command{}
	got, err = resolveClientServer(cmd, "")
	if err != nil {
		t.Fatalf("resolveClientServer(env): %v", err)
	}
	if got != "https://env.example:9090" {
		t.Fatalf("env server = %q", got)
	}

	t.Setenv("BOXY_SERVER", "")
	got, err = resolveClientServer(cmd, "")
	if err != nil {
		t.Fatalf("resolveClientServer(global): %v", err)
	}
	if got != "https://global.example:9090" {
		t.Fatalf("global server = %q", got)
	}
}

func TestResolveClientServerSkipsAgentTransport(t *testing.T) {
	root := &cobra.Command{Use: "boxy"}
	agent := &cobra.Command{Use: "agent"}
	serve := &cobra.Command{Use: "serve"}
	root.AddCommand(agent)
	agent.AddCommand(serve)
	if !isAgentTransportCommand(serve) {
		t.Fatal("boxy agent serve should be classified as agent transport")
	}
	if isAgentTransportCommand(agent) {
		t.Fatal("boxy agent should not be classified as agent transport")
	}
}

func TestRootResolvesGlobalServerForStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := newStatusTestServer(t)
	defer server.Close()
	if err := writeClientConfig(clientConfig{Server: server.URL}); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
}

func TestResolveServerAddrHonorsEnvironmentProjectAndGlobalPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := writeClientConfig(clientConfig{Server: "https://global.example:9090"}); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}
	project := filepath.Join(t.TempDir(), "boxy.yaml")
	if err := os.WriteFile(project, []byte("server:\n  listen: ':19090'\n"), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("server", "", "")

	got, err := resolveServerAddr(statusOpts{configPath: project}, cmd)
	if err != nil {
		t.Fatalf("resolveServerAddr(project): %v", err)
	}
	if got != "https://127.0.0.1:19090" {
		t.Fatalf("project server = %q, want https://127.0.0.1:19090", got)
	}

	t.Setenv("BOXY_SERVER", "https://env.example:9090")
	got, err = resolveServerAddr(statusOpts{configPath: project}, cmd)
	if err != nil {
		t.Fatalf("resolveServerAddr(environment): %v", err)
	}
	if got != "https://env.example:9090" {
		t.Fatalf("environment server = %q, want env endpoint", got)
	}

	t.Setenv("BOXY_SERVER", "")
	got, err = resolveServerAddr(statusOpts{}, cmd)
	if err != nil {
		t.Fatalf("resolveServerAddr(global): %v", err)
	}
	if got != "https://global.example:9090" {
		t.Fatalf("global server = %q, want global endpoint", got)
	}
}

func TestResolveServerAddrFallsBackToLocalDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BOXY_SERVER", "")
	cmd := &cobra.Command{}
	cmd.Flags().String("server", "", "")

	got, err := resolveServerAddr(statusOpts{}, cmd)
	if err != nil {
		t.Fatalf("resolveServerAddr(default): %v", err)
	}
	if got != "127.0.0.1:9090" {
		t.Fatalf("default server = %q, want 127.0.0.1:9090", got)
	}
}

func TestConfigClientSetServerCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"config", "client", "set-server", "boxy.example:9090"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config client set-server: %v", err)
	}
	got, err := loadClientConfig()
	if err != nil {
		t.Fatalf("loadClientConfig: %v", err)
	}
	if got.Server != "https://boxy.example:9090" {
		t.Fatalf("Server = %q, want normalized HTTPS endpoint", got.Server)
	}
}

func TestNormalizeClientServerRejectsInvalidURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://boxy.example:9090", "https://", "https://boxy.example/path"} {
		if _, err := normalizeClientServer(raw); err == nil {
			t.Errorf("normalizeClientServer(%q) error = nil", raw)
		}
	}
	if got, err := normalizeClientServer("boxy.example:9090/"); err != nil || got != "https://boxy.example:9090" {
		t.Fatalf("normalizeClientServer(bare) = %q, %v", got, err)
	}
}

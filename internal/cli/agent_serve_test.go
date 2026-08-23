package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/agentserver"
	"github.com/Geogboe/boxy/internal/pool"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// startAgentTestDaemon stands up the real server side of the agent
// transport — private CA, mTLS gRPC listener, AgentTransport service —
// exactly as boxy serve wires it, on an ephemeral port.
func startAgentTestDaemon(t *testing.T, st store.Store, registry *pool.AgentRegistry, serverDir string) (addr string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	grpcSrv, _, err := buildAgentGRPCServer(st, registry, nil, serverDir, ln.Addr().String(), time.Second, false, nil)
	if err != nil {
		t.Fatalf("buildAgentGRPCServer: %v", err)
	}
	go func() { _ = grpcSrv.Serve(ln) }()
	t.Cleanup(grpcSrv.Stop)

	return ln.Addr().String()
}

// waitForAgent polls the registry until exactly one agent appears (or the
// timeout hits), returning its summary.
func waitForAgent(t *testing.T, registry *pool.AgentRegistry) pool.AgentSummary {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if agents := registry.List(); len(agents) == 1 {
			return agents[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the agent to register")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAgentServe_TokenRegistrationThenCertReconnect(t *testing.T) {
	serverDir := t.TempDir()
	agentDir := t.TempDir()

	st := store.NewMemoryStore()
	registry := pool.NewAgentRegistry()
	addr := startAgentTestDaemon(t, st, registry, serverDir)

	raw, _, err := agentserver.MintToken(context.Background(), st, "e2e-test", time.Hour)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	opts := agentServeOpts{
		server:    addr,
		providers: []string{"devfactory"},
		token:     raw,
		name:      "e2e-agent",
		caCert:    filepath.Join(serverDir, "ca.crt"),
		dataDir:   agentDir,
	}

	// Phase 1: first connection registers with the token and receives
	// mTLS credentials.
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { defer close(done1); _ = runAgentServe(ctx1, opts) }()

	agent := waitForAgent(t, registry)
	if agent.Name != "e2e-agent" || !agent.Available {
		t.Fatalf("unexpected agent summary: %+v", agent)
	}

	for _, f := range []string{agentClientCertFile, agentClientKeyFile, agentCACertFile} {
		// Credentials are persisted asynchronously in OnRegistered, which
		// runs before the registry insert server-side but on the agent
		// side of the handshake — poll briefly.
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(agentDir, f)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("issued credential %s was not persisted to %s", f, agentDir)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	tokens, err := st.ListAgentTokens(context.Background())
	if err != nil {
		t.Fatalf("ListAgentTokens: %v", err)
	}
	if len(tokens) != 1 || !tokens[0].Used() {
		t.Fatalf("expected the token to be marked used, got %+v", tokens)
	}

	cancel1()
	select {
	case <-done1:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the first agent session to stop")
	}
	registry.Deregister(agent.ID) // simulate operator cleanup between runs so waitForAgent sees the re-registration

	// Phase 2: a fresh process (no token) reconnects using only the
	// persisted mTLS credentials.
	opts.token = ""
	opts.caCert = "" // must not be needed anymore: the persisted CA wins
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan struct{})
	go func() { defer close(done2); _ = runAgentServe(ctx2, opts) }()

	reconnected := waitForAgent(t, registry)
	if reconnected.ID != agent.ID {
		t.Fatalf("expected the reconnect to keep the same agent identity: first %q, reconnect %q", agent.ID, reconnected.ID)
	}

	cancel2()
	select {
	case <-done2:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the second agent session to stop")
	}
}

func TestAgentServe_RequiresTokenOrCredentials(t *testing.T) {
	opts := agentServeOpts{
		server:    "127.0.0.1:1", // never dialed
		providers: []string{"devfactory"},
		dataDir:   t.TempDir(),
	}
	if err := runAgentServe(context.Background(), opts); err == nil {
		t.Fatal("expected an error with no token and no persisted credentials")
	}
}

func TestAgentServe_RequiresCACertForFirstConnection(t *testing.T) {
	opts := agentServeOpts{
		server:    "127.0.0.1:1", // never dialed
		providers: []string{"devfactory"},
		token:     "${BOXY_TEST_REGISTRATION_TOKEN}",
		dataDir:   t.TempDir(),
	}
	if err := runAgentServe(context.Background(), opts); err == nil {
		t.Fatal("expected an error when --ca-cert is missing for a token-based first connection")
	}
}

// TestPersistAgentCredentials_ReappliesPermissionsOnRewrite guards the same
// bug class as issue #158 (os.WriteFile's mode argument is only applied by
// the OS when it creates a new file — on rewrite of a pre-existing file, it
// silently keeps whatever permissions the file already had). This path
// persists the agent's private key (client.key) and runs on every
// successful registration per RemoteClientConfig.OnRegistered's doc
// comment, including reconnects over already-persisted credentials, so the
// rewrite case is the normal case here, not an edge case.
func TestPersistAgentCredentials_ReappliesPermissionsOnRewrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dataDir := t.TempDir()
	resp := &boxyagentv1.RegisterResponse{
		AgentId:              "agent-1",
		ClientCertificatePem: []byte("cert-1"),
		ClientPrivateKeyPem:  []byte("key-1"),
		CaCertificatePem:     []byte("ca-1"),
	}
	if err := persistAgentCredentials(dataDir, resp); err != nil {
		t.Fatalf("persistAgentCredentials (first): %v", err)
	}

	keyPath := filepath.Join(dataDir, agentClientKeyFile)
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("loosen permissions: %v", err)
	}

	resp2 := &boxyagentv1.RegisterResponse{
		AgentId:              "agent-1",
		ClientCertificatePem: []byte("cert-2"),
		ClientPrivateKeyPem:  []byte("key-2"),
		CaCertificatePem:     []byte("ca-2"),
	}
	if err := persistAgentCredentials(dataDir, resp2); err != nil {
		t.Fatalf("persistAgentCredentials (second, rewrite): %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat client.key: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("client.key permissions = %04o, want %04o", got, want)
	}
}

func TestRunAgentServe_ServiceConfig_LoadsOptsFromFile(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".boxy-agent")
	cfgPath := filepath.Join(dir, "service.yaml")

	if err := saveAgentServiceConfig(cfgPath, agentServiceConfig{
		Server:    "127.0.0.1:1", // deliberately unreachable — this test only checks opts resolution, not a real connection
		Providers: []string{"docker"},
		DataDir:   agentDir,
		Insecure:  true,
	}); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	opts, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if opts.server != "127.0.0.1:1" || len(opts.providers) != 1 || opts.providers[0] != "docker" || opts.dataDir != agentDir || !opts.insecure {
		t.Fatalf("resolved opts = %+v, unexpected", opts)
	}
}

func TestRunAgentServe_NoServiceConfigAndNoServer_ErrorsClearly(t *testing.T) {
	_, err := resolveAgentServeOpts(agentServeOpts{})
	if err == nil {
		t.Fatal("expected an error when neither --server nor --service-config is given")
	}
}

func TestSelectAgentProviderInstancesFromConfig(t *testing.T) {
	instances := []providersdk.Instance{
		{Name: "docker-local", Type: "docker", Config: map[string]any{"host": "unix:///tmp/docker.sock"}},
		{Name: "hyperv-local", Type: "hyperv"},
	}
	selected, err := selectAgentProviderInstances(instances, nil)
	if err != nil {
		t.Fatalf("select all: %v", err)
	}
	if len(selected) != 2 || selected[0].Name != "docker-local" {
		t.Fatalf("selected = %+v, want both configured instances", selected)
	}
	selected, err = selectAgentProviderInstances(instances, []string{"docker"})
	if err != nil {
		t.Fatalf("select docker: %v", err)
	}
	if len(selected) != 1 || selected[0].Type != "docker" {
		t.Fatalf("selected = %+v, want docker instance", selected)
	}
}

func TestSelectAgentProviderInstancesRejectsDuplicateType(t *testing.T) {
	instances := []providersdk.Instance{
		{Name: "docker-a", Type: "docker"},
		{Name: "docker-b", Type: "docker"},
	}
	if _, err := selectAgentProviderInstances(instances, nil); err == nil {
		t.Fatal("duplicate provider type error = nil")
	}
	if _, err := selectAgentProviderInstances(instances, []string{"docker"}); err == nil {
		t.Fatal("duplicate selected provider type error = nil")
	}
}

func TestNormalizeProviderStringsTrimsAndDeduplicates(t *testing.T) {
	got := normalizeProviderStrings([]string{" docker ", "", "docker", "hyperv", " hyperv "})
	want := []string{"docker", "hyperv"}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeProviderStrings = %v, want %v", got, want)
	}
}

func TestResolveAgentServeOptsRejectsDuplicateServiceProviderConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.yaml")
	if err := saveAgentServiceConfig(path, agentServiceConfig{
		Server: "host:9091",
		ProviderConfigs: []providersdk.Instance{
			{Name: "docker-a", Type: "docker"},
			{Name: "docker-b", Type: "docker"},
		},
	}); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}
	if _, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: path}); err == nil {
		t.Fatal("resolveAgentServeOpts duplicate service configs error = nil")
	}
}

func TestResolveAgentServeOptsLoadsProviderConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boxy.yaml")
	if err := os.WriteFile(path, []byte("providers:\n  - name: docker-local\n    type: docker\n    config:\n      host: unix:///tmp/docker.sock\npools: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	opts, err := resolveAgentServeOpts(agentServeOpts{server: "host:9091", configPath: path})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if len(opts.providers) != 1 || opts.providers[0] != "docker" {
		t.Fatalf("providers = %v, want docker", opts.providers)
	}
	if len(opts.providerConfigs) != 1 || opts.providerConfigs[0].Config["host"] != "unix:///tmp/docker.sock" {
		t.Fatalf("providerConfigs = %+v, want decoded docker config", opts.providerConfigs)
	}
	if want := filepath.Dir(path); opts.providerConfigsBaseDir != want {
		t.Fatalf("providerConfigsBaseDir = %q, want %q", opts.providerConfigsBaseDir, want)
	}
}

func TestResolveAgentServeOptsSetsProviderConfigsBaseDirFromServiceConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.yaml")
	if err := saveAgentServiceConfig(path, agentServiceConfig{
		Server: "host:9091",
		ProviderConfigs: []providersdk.Instance{
			{Name: "docker-local", Type: "docker"},
		},
	}); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	opts, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: path})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if want := filepath.Dir(path); opts.providerConfigsBaseDir != want {
		t.Fatalf("providerConfigsBaseDir = %q, want %q", opts.providerConfigsBaseDir, want)
	}
}

// TestResolveAgentServeOptsPrefersPersistedProviderConfigsBaseDir proves the
// fallback above (filepath.Dir(serviceConfigPath)) only applies to a
// service.yaml with no recorded ProviderConfigsBaseDir. When
// ProviderConfigsBaseDir IS set — as `agent service install --config`
// always sets it — it must win, even though it points somewhere entirely
// different from service.yaml's own directory.
func TestResolveAgentServeOptsPrefersPersistedProviderConfigsBaseDir(t *testing.T) {
	originalConfigDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "service.yaml") // deliberately a different directory
	if err := saveAgentServiceConfig(path, agentServiceConfig{
		Server: "host:9091",
		ProviderConfigs: []providersdk.Instance{
			{Name: "docker-local", Type: "docker"},
		},
		ProviderConfigsBaseDir: originalConfigDir,
	}); err != nil {
		t.Fatalf("saveAgentServiceConfig: %v", err)
	}

	opts, err := resolveAgentServeOpts(agentServeOpts{serviceConfigPath: path})
	if err != nil {
		t.Fatalf("resolveAgentServeOpts: %v", err)
	}
	if opts.providerConfigsBaseDir != originalConfigDir {
		t.Fatalf("providerConfigsBaseDir = %q, want persisted %q, not service.yaml's own directory %q", opts.providerConfigsBaseDir, originalConfigDir, filepath.Dir(path))
	}
}

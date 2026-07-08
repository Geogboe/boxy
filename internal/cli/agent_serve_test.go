package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/agentserver"
	"github.com/Geogboe/boxy/internal/pool"
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

	grpcSrv, _, err := buildAgentGRPCServer(st, registry, serverDir, ln.Addr().String(), time.Second, false)
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
		token:     "some-token",
		dataDir:   t.TempDir(),
	}
	if err := runAgentServe(context.Background(), opts); err == nil {
		t.Fatal("expected an error when --ca-cert is missing for a token-based first connection")
	}
}

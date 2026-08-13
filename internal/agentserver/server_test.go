package agentserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Geogboe/boxy/internal/pool"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/pki"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// testServerVersion is the version every test server is constructed with.
// Connect's #167 version check runs before authenticate, so every
// RegisterRequest literal in this file — including ones exercising
// token-rejection paths — must set AgentVersion to this same value, or the
// version check rejects the request before it ever reaches the token/cert
// validation the test actually means to exercise.
const testServerVersion = "v-test"

// newTestServer wires up a Server against fresh in-memory dependencies and
// starts it listening on an in-process bufconn — no real network socket,
// no TLS (registration-logic tests only; mTLS enforcement itself is
// covered by pkg/pki's real cert-chain-verification tests).
func newTestServer(t *testing.T) (*Server, store.Store, boxyagentv1.AgentTransportServiceClient, func()) {
	t.Helper()
	return newTestServerWithForceOrphaner(t, nil)
}

// newTestServerWithForceOrphaner is newTestServer plus an explicit
// ResourceForceOrphaner, for tests exercising Revoke's
// --force-orphan-resources sweep.
func newTestServerWithForceOrphaner(t *testing.T, fo ResourceForceOrphaner) (*Server, store.Store, boxyagentv1.AgentTransportServiceClient, func()) {
	t.Helper()

	st := store.NewMemoryStore()
	registry := pool.NewAgentRegistry()
	ca, err := pki.EnsureCA(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	srv := New(st, registry, ca, 50*time.Millisecond, fo, testServerVersion)

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	boxyagentv1.RegisterAgentTransportServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		grpcServer.Stop()
	}
	return srv, st, boxyagentv1.NewAgentTransportServiceClient(conn), cleanup
}

func mintToken(t *testing.T, st store.Store, raw string, expiresIn time.Duration) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.PutAgentToken(context.Background(), model.AgentRegistrationToken{
		ID:        model.AgentTokenID(raw),
		TokenHash: hashToken(raw),
		CreatedAt: now,
		ExpiresAt: now.Add(expiresIn),
	}); err != nil {
		t.Fatalf("PutAgentToken: %v", err)
	}
}

func TestConnect_TokenRegistrationHappyPath(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-good", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: "tok-good",
			AgentName:         "test-agent",
			ProviderTypes:     []string{"docker"},
			AgentVersion:      testServerVersion,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	resp := msg.GetRegistered()
	if resp == nil {
		t.Fatalf("expected a RegisterResponse, got %#v", msg)
	}
	if resp.GetAgentId() == "" {
		t.Fatal("expected a non-empty agent id")
	}
	if len(resp.GetClientCertificatePem()) == 0 || len(resp.GetClientPrivateKeyPem()) == 0 {
		t.Fatal("expected client cert and private key material on a token-based registration")
	}

	// The token must now be marked used.
	tok, err := st.GetAgentToken(context.Background(), "tok-good")
	if err != nil {
		t.Fatalf("GetAgentToken: %v", err)
	}
	if !tok.Used() {
		t.Fatal("expected the token to be marked used after successful registration")
	}

	// The agent must now be resolvable through the registry.
	if _, ok := srv.registry.Get(resp.GetAgentId()); !ok {
		t.Fatal("expected the agent to be registered after successful registration")
	}
}

// TestConnect_VersionMismatchRejected covers #167: a RegisterRequest whose
// agent_version doesn't exactly match the server's own version must be
// rejected before authentication runs, so a version-skewed agent doesn't
// burn its single-use token just to be told to upgrade.
func TestConnect_VersionMismatchRejected(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-good", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: "tok-good",
			AgentName:         "test-agent",
			ProviderTypes:     []string{"docker"},
			AgentVersion:      "v-other",
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("expected an error for a mismatched agent version")
	}
	// The peer isn't authenticated yet at this point in Connect, so the
	// rejection sent over the wire must not hand an unauthenticated caller
	// the server's exact version string — only the server-side log gets
	// that detail.
	if strings.Contains(recvErr.Error(), "v-test") || strings.Contains(recvErr.Error(), "v-other") {
		t.Fatalf("rejection error leaked a version string to an unauthenticated peer: %v", recvErr)
	}

	// The token must still be unused — a version-mismatched agent hasn't
	// authenticated, so it shouldn't cost its single registration attempt.
	tok, err := st.GetAgentToken(context.Background(), "tok-good")
	if err != nil {
		t.Fatalf("GetAgentToken: %v", err)
	}
	if tok.Used() {
		t.Fatal("expected the token to remain unused after a version-mismatch rejection")
	}
}

// TestConnect_BlankVersionRejected covers the deliberate strictness called
// out in New's doc comment: an agent built before agent_version existed
// (or that otherwise sends a blank version) is rejected exactly like any
// other mismatch, not treated as a wildcard.
func TestConnect_BlankVersionRejected(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-good", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: "tok-good",
			AgentName:         "test-agent",
			ProviderTypes:     []string{"docker"},
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected an error for a blank agent version")
	}
}

// TestConnect_TriggersReconciliationSweep verifies the #133 wiring end to
// end: a successful registration must cause the server to send a
// ListCommand down the stream (pool.ReconcileAgent auditing this agent),
// and a resource the agent reports that the store never tracked must be
// adopted.
func TestConnect_TriggersReconciliationSweep(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-good", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: "tok-good",
			AgentName:         "test-agent",
			ProviderTypes:     []string{"docker"},
			AgentVersion:      testServerVersion,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	agentID := msg.GetRegistered().GetAgentId()
	if agentID == "" {
		t.Fatal("expected a non-empty agent id")
	}

	// The reconciliation sweep starts once Serve() is pumping the stream
	// (see server.go's Connect), so the next frame in is the ListCommand
	// it issues, not anything test-driven.
	cmdMsg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv reconciliation command: %v", err)
	}
	cmd := cmdMsg.GetCommand()
	if cmd == nil || cmd.GetList() == nil {
		t.Fatalf("expected the server to issue a ListCommand for reconciliation, got %#v", cmdMsg)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Result{Result: &boxyagentv1.CommandResult{
			CommandId: cmd.GetCommandId(),
			Outcome: &boxyagentv1.CommandResult_List{List: &boxyagentv1.ListResult{
				Resources: []*boxyagentv1.ResourceStatusResult{
					{Id: "orphan-container", State: "running"},
				},
			}},
		}},
	}); err != nil {
		t.Fatalf("send list result: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := st.GetResource(context.Background(), "orphan-container")
		if err == nil {
			if res.Provider.AgentID != agentID {
				t.Fatalf("adopted resource has AgentID %q, want %q", res.Provider.AgentID, agentID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan-container was never adopted into the store: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnect_UnknownTokenRejected(t *testing.T) {
	_, _, client, cleanup := newTestServer(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{RegistrationToken: "no-such-token", AgentVersion: testServerVersion}},
	})
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected an error for an unknown registration token")
	}
}

func TestConnect_ExpiredTokenRejected(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-expired", -time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{RegistrationToken: "tok-expired", AgentVersion: testServerVersion}},
	})
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected an error for an expired registration token")
	}
}

func TestConnect_UsedTokenRejectedOnSecondAttempt(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-reuse", time.Hour)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	stream1, err := client.Connect(ctx1)
	if err != nil {
		t.Fatalf("Connect (first): %v", err)
	}
	_ = stream1.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{RegistrationToken: "tok-reuse", AgentVersion: testServerVersion}},
	})
	if _, err := stream1.Recv(); err != nil {
		t.Fatalf("expected the first registration to succeed: %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	stream2, err := client.Connect(ctx2)
	if err != nil {
		t.Fatalf("Connect (second): %v", err)
	}
	_ = stream2.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{RegistrationToken: "tok-reuse", AgentVersion: testServerVersion}},
	})
	if _, err := stream2.Recv(); err == nil {
		t.Fatal("expected the second attempt to redeem the same token to be rejected")
	}
}

func TestConnect_DeletedTokenRejected(t *testing.T) {
	_, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-revoked", time.Hour)
	if err := st.DeleteAgentToken(context.Background(), "tok-revoked"); err != nil {
		t.Fatalf("DeleteAgentToken: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{RegistrationToken: "tok-revoked", AgentVersion: testServerVersion}},
	})
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected a deleted (revoked) token to be rejected")
	}
}

func TestConnect_HeartbeatMarksAvailability(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, "tok-hb", time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: "tok-hb",
			ProviderTypes:     []string{"docker"},
			AgentVersion:      testServerVersion,
		}},
	})
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	agentID := msg.GetRegistered().GetAgentId()

	// Freshly registered: available immediately, before any heartbeat monitor tick.
	if _, err := srv.registry.Resolve("docker", agentID); err != nil {
		t.Fatalf("expected the freshly registered agent to be available: %v", err)
	}

	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	go srv.RunHeartbeatMonitor(monitorCtx)

	// No heartbeat sent: after enough missed intervals, must become unavailable.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := srv.registry.Resolve("docker", agentID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the agent to be marked unavailable after missing heartbeats")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Sending a heartbeat must flip it back to available.
	_ = stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Heartbeat{Heartbeat: &boxyagentv1.Heartbeat{AgentId: agentID, ProviderTypes: []string{"docker"}}},
	})
	deadline = time.Now().Add(2 * time.Second)
	for {
		if _, err := srv.registry.Resolve("docker", agentID); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the agent to become available again after a heartbeat")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stubAgent is a minimal agentsdk.Agent implementation used only to
// populate the registry for Revoke tests — none of its CRUD methods are
// exercised.
type stubAgent struct {
	id string
}

func (a *stubAgent) Info() agentsdk.AgentInfo { return agentsdk.AgentInfo{ID: a.id} }
func (a *stubAgent) Create(ctx context.Context, provider providersdk.Type, cfg any) (*providersdk.Resource, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *stubAgent) Read(ctx context.Context, provider providersdk.Type, id string) (*providersdk.ResourceStatus, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *stubAgent) Update(ctx context.Context, provider providersdk.Type, id string, op providersdk.Operation) (*providersdk.Result, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *stubAgent) Delete(ctx context.Context, provider providersdk.Type, id string) error {
	return fmt.Errorf("not implemented")
}
func (a *stubAgent) Allocate(ctx context.Context, provider providersdk.Type, id string) (map[string]any, error) {
	return nil, fmt.Errorf("not implemented")
}

// fakeForceOrphaner is a test double for the ResourceForceOrphaner seam. If
// registry is set, it records — at the moment it's invoked, in
// stillRegisteredAtSweep — whether the agent was still present in the
// registry. Revoke discards any error this returns (best-effort sweep), so
// a test verifying the Deregister-before-sweep ordering must assert this
// recorded state directly rather than relying on a returned error.
type fakeForceOrphaner struct {
	registry               *pool.AgentRegistry
	calls                  []struct{ agentID, reason string }
	stillRegisteredAtSweep bool
	n                      int
	err                    error
}

func (f *fakeForceOrphaner) ForceOrphanAgentResources(ctx context.Context, agentID, reason string) (int, error) {
	f.calls = append(f.calls, struct{ agentID, reason string }{agentID, reason})
	if f.registry != nil {
		_, f.stillRegisteredAtSweep = f.registry.Get(agentID)
	}
	return f.n, f.err
}

func TestRevoke_ForceOrphanResourcesTrue_SweepsAfterDeregister(t *testing.T) {
	registry := pool.NewAgentRegistry()
	if err := registry.Register(&stubAgent{id: "agent-gone"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	fo := &fakeForceOrphaner{registry: registry, n: 2}

	srv := New(store.NewMemoryStore(), registry, mustTestCA(t), time.Second, fo, testServerVersion)
	if err := srv.Revoke(context.Background(), "agent-gone", "host decommissioned", true); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(fo.calls) != 1 || fo.calls[0].agentID != "agent-gone" || fo.calls[0].reason != "host decommissioned" {
		t.Fatalf("calls = %+v, want one call for agent-gone/host decommissioned", fo.calls)
	}
	// The safety-critical assertion: by the time the sweep ran, Deregister
	// must already have removed the agent from the registry — this is the
	// precondition pool.AgentProvisioner.ForceOrphan depends on.
	if fo.stillRegisteredAtSweep {
		t.Fatal("sweep observed the agent still registered — it must run after registry.Deregister")
	}
}

func TestRevoke_ForceOrphanResourcesFalse_DoesNotSweep(t *testing.T) {
	registry := pool.NewAgentRegistry()
	if err := registry.Register(&stubAgent{id: "agent-gone"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	fo := &fakeForceOrphaner{registry: registry}

	srv := New(store.NewMemoryStore(), registry, mustTestCA(t), time.Second, fo, testServerVersion)
	if err := srv.Revoke(context.Background(), "agent-gone", "reason", false); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(fo.calls) != 0 {
		t.Fatalf("calls = %+v, want none when forceOrphanResources is false", fo.calls)
	}
}

func TestRevoke_ForceOrphanResourcesTrue_NilForceOrphanerLogsAndSucceeds(t *testing.T) {
	registry := pool.NewAgentRegistry()
	srv := New(store.NewMemoryStore(), registry, mustTestCA(t), time.Second, nil, testServerVersion)
	if err := srv.Revoke(context.Background(), "agent-gone", "reason", true); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
}

func mustTestCA(t *testing.T) *pki.CA {
	t.Helper()
	ca, err := pki.EnsureCA(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	return ca
}

func TestAuthenticateWithCert_RejectsRevokedIdentity(t *testing.T) {
	dir := t.TempDir()
	ca, err := pki.EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	certPEM, _, serial, err := pki.IssueAgentCert(ca, "agent-revoked")
	if err != nil {
		t.Fatalf("IssueAgentCert: %v", err)
	}
	cert := parseTestCert(t, certPEM)

	st := store.NewMemoryStore()
	srv := New(st, pool.NewAgentRegistry(), ca, time.Second, nil, testServerVersion)

	ctx := contextWithPeerCert(cert)

	// Not yet revoked: authentication should succeed.
	if _, _, _, err := srv.authenticateWithCert(ctx); err != nil {
		t.Fatalf("expected authentication to succeed before revocation: %v", err)
	}

	if err := st.PutRevokedAgentIdentity(context.Background(), model.RevokedAgentIdentity{
		ID:         "rev-1",
		AgentID:    "agent-revoked",
		CertSerial: serial,
	}); err != nil {
		t.Fatalf("PutRevokedAgentIdentity: %v", err)
	}

	if _, _, _, err := srv.authenticateWithCert(ctx); err == nil {
		t.Fatal("expected authentication to fail once the cert's serial is revoked")
	}
}

func TestAuthenticateWithCert_UsesCommonNameAsAgentID(t *testing.T) {
	dir := t.TempDir()
	ca, err := pki.EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	certPEM, _, _, err := pki.IssueAgentCert(ca, "agent-xyz")
	if err != nil {
		t.Fatalf("IssueAgentCert: %v", err)
	}
	cert := parseTestCert(t, certPEM)

	srv := New(store.NewMemoryStore(), pool.NewAgentRegistry(), ca, time.Second, nil, testServerVersion)
	agentID, certOut, keyOut, err := srv.authenticateWithCert(contextWithPeerCert(cert))
	if err != nil {
		t.Fatalf("authenticateWithCert: %v", err)
	}
	if agentID != "agent-xyz" {
		t.Fatalf("agentID = %q, want agent-xyz", agentID)
	}
	if certOut != nil || keyOut != nil {
		t.Fatal("a cert-based reconnect must not re-issue cert material")
	}
}

func parseTestCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("expected a PEM block in the test cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func contextWithPeerCert(cert *x509.Certificate) context.Context {
	p := &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}},
		},
	}
	return peer.NewContext(context.Background(), p)
}

// Package agentserver implements the server side of the AgentTransport gRPC
// service: registration (single-use token or mTLS client cert), heartbeat
// tracking, and command dispatch to connected remote agents. See
// docs/adr/0005-remote-agent-transport-and-registration.md.
package agentserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/Geogboe/boxy/internal/pki"
	"github.com/Geogboe/boxy/internal/pool"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// DefaultMissedHeartbeatLimit is how many consecutive missed heartbeat
// intervals mark an agent unavailable for new provisioning.
const DefaultMissedHeartbeatLimit = 3

// reconciliationTimeout bounds the #133 post-registration reconciliation
// sweep so a slow or hung List call can't hold resources indefinitely; the
// sweep is best-effort and logs rather than fails the connection either way.
const reconciliationTimeout = 30 * time.Second

// Server implements the generated AgentTransportServiceServer.
type Server struct {
	boxyagentv1.UnimplementedAgentTransportServiceServer

	store    store.Store
	registry *pool.AgentRegistry
	ca       *pki.CA

	heartbeatInterval    time.Duration
	missedHeartbeatLimit int

	logger *slog.Logger
	now    func() time.Time

	mu           sync.Mutex
	remoteAgents map[string]*agentsdk.RemoteAgent
	forceStop    map[string]chan struct{}
}

// New constructs a Server. heartbeatInterval should match the value handed
// to connecting agents in RegisterResponse.
func New(st store.Store, registry *pool.AgentRegistry, ca *pki.CA, heartbeatInterval time.Duration) *Server {
	return &Server{
		store:                st,
		registry:             registry,
		ca:                   ca,
		heartbeatInterval:    heartbeatInterval,
		missedHeartbeatLimit: DefaultMissedHeartbeatLimit,
		now:                  time.Now,
		remoteAgents:         make(map[string]*agentsdk.RemoteAgent),
		forceStop:            make(map[string]chan struct{}),
	}
}

// log returns s.logger, falling back to slog.Default() — same pattern as
// pkg/policycontroller.Controller's Logger field.
func (s *Server) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

// ListAgents returns a snapshot of every registered agent, for the
// GET /api/v1/agents endpoint and `boxy agent list`.
func (s *Server) ListAgents() []pool.AgentSummary {
	return s.registry.List()
}

// DefaultTokenTTL is how long a freshly minted registration token stays
// redeemable when no explicit TTL is given.
const DefaultTokenTTL = time.Hour

// MintToken creates a new single-use registration token: the raw secret is
// returned exactly once (for the operator to hand to `boxy agent serve
// --token ...`) and only its hash is persisted.
func MintToken(ctx context.Context, st store.Store, label string, ttl time.Duration) (raw string, tok model.AgentRegistrationToken, err error) {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", model.AgentRegistrationToken{}, fmt.Errorf("generate token secret: %w", err)
	}
	raw = hex.EncodeToString(secret)

	now := time.Now().UTC()
	tok = model.AgentRegistrationToken{
		ID:        model.AgentTokenID(uuid.NewString()),
		TokenHash: hashToken(raw),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Label:     label,
	}
	if err := st.PutAgentToken(ctx, tok); err != nil {
		return "", model.AgentRegistrationToken{}, fmt.Errorf("persist token: %w", err)
	}
	return raw, tok, nil
}

// Connect implements the AgentTransportService.Connect bidi-streaming RPC.
// The first frame must be a RegisterRequest (token-based for a first-time
// registration, or bare for a cert-authenticated reconnect); every frame
// after that is handled by the resulting RemoteAgent's own Serve loop.
func (s *Server) Connect(stream boxyagentv1.AgentTransportService_ConnectServer) error {
	ctx := stream.Context()

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive register request: %w", err)
	}
	reg := first.GetRegister()
	if reg == nil {
		return fmt.Errorf("first frame must be a RegisterRequest")
	}

	agentID, certPEM, keyPEM, err := s.authenticate(ctx, reg)
	if err != nil {
		s.log().Warn("agent registration rejected", "error", err)
		return fmt.Errorf("authenticate: %w", err)
	}

	info := agentsdk.AgentInfo{
		ID:        agentID,
		Name:      reg.GetAgentName(),
		Providers: toProviderTypes(reg.GetProviderTypes()),
	}
	remote := agentsdk.NewRemoteAgent(info, stream)
	forceStop := make(chan struct{})

	s.mu.Lock()
	s.remoteAgents[agentID] = remote
	s.forceStop[agentID] = forceStop
	s.mu.Unlock()

	if err := s.registry.Register(remote); err != nil {
		s.cleanupConnection(agentID, remote)
		return fmt.Errorf("register agent %q: %w", agentID, err)
	}
	s.log().Info("agent connected", "agent_id", agentID, "name", info.Name, "providers", info.Providers, "new_registration", certPEM != nil)
	// On any exit from Connect (clean disconnect, error, or a Revoke-forced
	// stop), the agent's providers stop being offered for new provisioning
	// immediately. Deregistering entirely is Revoke's job, not a plain
	// disconnect's — see cleanupConnection.
	defer func() {
		s.registry.SetAvailable(agentID, false)
		s.cleanupConnection(agentID, remote)
		s.log().Info("agent disconnected", "agent_id", agentID)
	}()

	resp := &boxyagentv1.RegisterResponse{
		AgentId:                  agentID,
		HeartbeatIntervalSeconds: int32(s.heartbeatInterval.Seconds()),
	}
	if certPEM != nil {
		resp.ClientCertificatePem = certPEM
		resp.ClientPrivateKeyPem = keyPEM
		resp.CaCertificatePem = s.ca.CertPEM
	}
	if err := stream.Send(&boxyagentv1.ServerMessage{Payload: &boxyagentv1.ServerMessage_Registered{Registered: resp}}); err != nil {
		return fmt.Errorf("send register response: %w", err)
	}

	// remote.Serve() blocks on stream.Recv(), which cannot be interrupted
	// directly from another goroutine — so Revoke signals forceStop and
	// relies on this handler returning to end the RPC, which tears down
	// the transport and eventually unblocks the orphaned Serve() goroutine
	// too (its own Close() is idempotent, guarded by sync.Once).
	serveDone := make(chan error, 1)
	go func() { serveDone <- remote.Serve() }()

	// The #133 reconciliation sweep needs Serve() already pumping the
	// stream (List is itself a command sent down it), so it can only start
	// here, not before. Runs on every successful registration, not just
	// reconnects — see pool.ReconcileAgent's doc comment. Bounded and
	// logged-only: reconciliation trouble must never take down agent
	// connectivity.
	go func() {
		rctx, cancel := context.WithTimeout(ctx, reconciliationTimeout)
		defer cancel()
		if err := pool.ReconcileAgent(rctx, s.store, s.registry, agentID, s.log()); err != nil {
			s.log().Warn("post-registration reconciliation failed", "agent_id", agentID, "error", err)
		}
	}()

	select {
	case err := <-serveDone:
		return err
	case <-forceStop:
		remote.Close()
		return fmt.Errorf("agent %q connection revoked", agentID)
	}
}

func (s *Server) cleanupConnection(agentID string, remote *agentsdk.RemoteAgent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteAgents[agentID] == remote {
		delete(s.remoteAgents, agentID)
		delete(s.forceStop, agentID)
	}
}

// Revoke deregisters agentID, records a deny-list entry keyed by its
// current certificate serial (looked up even if the agent is currently
// disconnected), and — if it has a live connection — actively tears down
// that connection rather than merely removing the registry entry. Used by
// `boxy agent revoke <id>`.
func (s *Server) Revoke(ctx context.Context, agentID, reason string) error {
	identity, err := s.store.GetAgentIdentity(ctx, agentID)
	switch {
	case err == nil:
		if putErr := s.store.PutRevokedAgentIdentity(ctx, model.RevokedAgentIdentity{
			ID:         model.AgentIdentityID(uuid.NewString()),
			AgentID:    agentID,
			CertSerial: identity.CertSerial,
			RevokedAt:  s.now().UTC(),
			Reason:     reason,
		}); putErr != nil {
			return fmt.Errorf("put revoked agent identity: %w", putErr)
		}
	case errors.Is(err, store.ErrNotFound):
		// No known identity (e.g. never successfully registered) — still
		// proceed to deregister/disconnect below.
	default:
		return fmt.Errorf("get agent identity: %w", err)
	}

	s.registry.Deregister(agentID)

	s.mu.Lock()
	if remote, ok := s.remoteAgents[agentID]; ok {
		remote.Close()
	}
	if stop, ok := s.forceStop[agentID]; ok {
		close(stop)
		delete(s.forceStop, agentID)
	}
	s.mu.Unlock()

	s.log().Warn("agent revoked", "agent_id", agentID, "reason", reason)
	return nil
}

// RunHeartbeatMonitor periodically marks each connected agent available or
// unavailable for new provisioning based on how recently it last sent a
// Heartbeat, without touching already-allocated resources. Mirrors
// internal/pool/manager.go's provisionBackoffState: in-memory only, resets
// on daemon restart. Blocks until ctx is done; run it in its own goroutine.
func (s *Server) RunHeartbeatMonitor(ctx context.Context) {
	if s.heartbeatInterval <= 0 {
		return
	}
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkHeartbeats()
		}
	}
}

func (s *Server) checkHeartbeats() {
	s.mu.Lock()
	agents := make(map[string]*agentsdk.RemoteAgent, len(s.remoteAgents))
	for id, a := range s.remoteAgents {
		agents[id] = a
	}
	s.mu.Unlock()

	threshold := s.heartbeatInterval * time.Duration(s.missedHeartbeatLimit)
	for id, a := range agents {
		s.registry.SetAvailable(id, time.Since(a.LastSeen()) <= threshold)
	}
}

// authenticate handles both registration paths: a fresh, token-based
// registration (mints a new agent ID and client cert) or a cert-based
// reconnect (identity comes from the already mTLS-verified peer
// certificate). certPEM/keyPEM are non-nil only for the token-based path.
func (s *Server) authenticate(ctx context.Context, reg *boxyagentv1.RegisterRequest) (agentID string, certPEM, keyPEM []byte, err error) {
	if reg.GetRegistrationToken() != "" {
		return s.authenticateWithToken(ctx, reg.GetRegistrationToken())
	}
	return s.authenticateWithCert(ctx)
}

func (s *Server) authenticateWithToken(ctx context.Context, rawToken string) (string, []byte, []byte, error) {
	hash := hashToken(rawToken)

	tokens, err := s.store.ListAgentTokens(ctx)
	if err != nil {
		return "", nil, nil, fmt.Errorf("list agent tokens: %w", err)
	}

	var matched *model.AgentRegistrationToken
	for i := range tokens {
		if subtle.ConstantTimeCompare([]byte(tokens[i].TokenHash), []byte(hash)) == 1 {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return "", nil, nil, fmt.Errorf("invalid registration token")
	}

	now := s.now().UTC()
	if matched.Used() {
		return "", nil, nil, fmt.Errorf("registration token already used")
	}
	if matched.Expired(now) {
		return "", nil, nil, fmt.Errorf("registration token expired")
	}

	// Mark used before issuing anything, so the token can never be
	// redeemed twice even under concurrent misuse.
	matched.UsedAt = &now
	if err := s.store.PutAgentToken(ctx, *matched); err != nil {
		return "", nil, nil, fmt.Errorf("mark token used: %w", err)
	}

	agentID := uuid.NewString()
	certPEM, keyPEM, serial, err := pki.IssueAgentCert(s.ca, agentID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("issue agent cert: %w", err)
	}
	if err := s.store.PutAgentIdentity(ctx, model.AgentIdentity{AgentID: agentID, CertSerial: serial, IssuedAt: now}); err != nil {
		return "", nil, nil, fmt.Errorf("persist agent identity: %w", err)
	}

	return agentID, certPEM, keyPEM, nil
}

func (s *Server) authenticateWithCert(ctx context.Context) (string, []byte, []byte, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", nil, nil, fmt.Errorf("no peer info on connection")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", nil, nil, fmt.Errorf("connection is not authenticated via TLS")
	}
	if len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return "", nil, nil, fmt.Errorf("no verified client certificate presented")
	}
	cert := tlsInfo.State.VerifiedChains[0][0]
	agentID := cert.Subject.CommonName
	serial := cert.SerialNumber.String()

	revoked, err := s.store.IsAgentIdentityRevoked(ctx, serial)
	if err != nil {
		return "", nil, nil, fmt.Errorf("check revocation: %w", err)
	}
	if revoked {
		return "", nil, nil, fmt.Errorf("agent identity revoked")
	}

	return agentID, nil, nil, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func toProviderTypes(in []string) []providersdk.Type {
	out := make([]providersdk.Type, len(in))
	for i, t := range in {
		out[i] = providersdk.Type(t)
	}
	return out
}

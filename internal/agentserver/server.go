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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/Geogboe/boxy/internal/pool"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/pki"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

// DefaultMissedHeartbeatLimit is how many consecutive missed heartbeat
// intervals mark an agent unavailable for new provisioning.
const DefaultMissedHeartbeatLimit = 3

// ResourceForceOrphaner force-orphans every resource attributed to a
// permanently-gone agent. Implemented by pool.Manager. A narrow seam so
// agentserver does not need pool.Manager's full surface.
type ResourceForceOrphaner interface {
	ForceOrphanAgentResources(ctx context.Context, agentID, reason string) (int, error)
}

// Server implements the generated AgentTransportServiceServer.
type Server struct {
	boxyagentv1.UnimplementedAgentTransportServiceServer

	store         store.Store
	registry      *pool.AgentRegistry
	ca            *pki.CA
	forceOrphaner ResourceForceOrphaner
	version       string

	heartbeatInterval    time.Duration
	missedHeartbeatLimit int

	logger *slog.Logger
	now    func() time.Time

	mu           sync.Mutex
	remoteAgents map[string]*agentsdk.RemoteAgent
	forceStop    map[string]chan struct{}
}

// New constructs a Server. heartbeatInterval should match the value handed
// to connecting agents in RegisterResponse. forceOrphaner may be nil (e.g.
// in tests that don't exercise `boxy agent revoke --force-orphan-resources`);
// Revoke logs and skips the sweep in that case rather than panicking.
// version is this server binary's version string; Connect rejects any
// agent whose RegisterRequest.agent_version doesn't match it exactly (see
// #167), including a blank agent_version from an agent built before that
// field existed — deliberately strict, since a silent "unknown version
// always accepted" exception would defeat the point of the check.
func New(st store.Store, registry *pool.AgentRegistry, ca *pki.CA, heartbeatInterval time.Duration, forceOrphaner ResourceForceOrphaner, version string) *Server {
	return &Server{
		store:                st,
		registry:             registry,
		ca:                   ca,
		forceOrphaner:        forceOrphaner,
		version:              version,
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
// registration, or token-less for a cert-authenticated reconnect — see
// authenticate) with an agent_version matching this server's own version
// (see #167); every frame after that is handled by the resulting
// RemoteAgent's own Serve loop.
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

	// Checked before authenticate so a version-skewed agent doesn't burn a
	// single-use registration token (or, on a cert-based reconnect, doesn't
	// spend a store lookup checking revocation) just to be told to upgrade.
	// The rejection sent back over the wire deliberately omits the actual
	// version strings: unlike every other rejection reason below this point,
	// this one fires before any token or mTLS identity has been checked, so
	// the peer isn't known to be a legitimate agent yet — the full detail
	// (useful for an operator diagnosing a real skewed agent) goes to the
	// server log instead, not to whatever opened the stream.
	if reg.GetAgentVersion() != s.version {
		s.log().Warn("agent registration rejected: version mismatch", "agent_name", reg.GetAgentName(), "agent_version", reg.GetAgentVersion(), "server_version", s.version)
		return fmt.Errorf("agent version does not match server version; upgrade the agent (or the server) so both sides match")
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
	// here, not before. Runs immediately on every successful registration,
	// not just reconnects, then repeatedly on the connection's heartbeat
	// cadence for as long as the connection lasts — see #174's periodic
	// defense-in-depth sweep. ctx is the connection-scoped context already
	// used by this handler's own select below, so the loop stops naturally
	// on disconnect; each pass stays bounded by
	// pool.DefaultReconciliationPassTimeout, logged-only on failure:
	// reconciliation trouble must never take down agent connectivity.
	go pool.RunAgentReconciliation(ctx, s.store, s.registry, agentID, s.heartbeatInterval, pool.DefaultReconciliationPassTimeout, s.log())

	select {
	case err := <-serveDone:
		return err
	case <-forceStop:
		remote.Close()
		return fmt.Errorf("agent %q connection revoked", agentID)
	}
}

// ResolveGuestBootstrapCredential returns the server-owned bootstrap secret
// for a resource, but only to the mTLS-authenticated agent that owns it. The
// resource's recorded OriginPool and Provider.AgentID are the authority; no
// caller-supplied pool or agent claims are trusted.
func (s *Server) ResolveGuestBootstrapCredential(ctx context.Context, req *boxyagentv1.ResolveGuestBootstrapCredentialRequest) (*boxyagentv1.ResolveGuestBootstrapCredentialResponse, error) {
	if req == nil || strings.TrimSpace(req.GetResourceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "resource_id is required")
	}
	agentID, _, _, err := s.authenticateWithCert(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authenticated agent certificate is required")
	}

	resource, err := s.store.GetResource(ctx, model.ResourceID(req.GetResourceId()))
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get resource")
	}
	if resource.Provider.AgentID == "" || resource.Provider.AgentID != agentID {
		return nil, status.Error(codes.PermissionDenied, "agent does not own resource")
	}
	password, err := s.store.GetPoolGuestCredential(ctx, resource.OriginPool)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.FailedPrecondition, "no guest bootstrap credential is configured for the resource pool")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get pool guest credential")
	}

	return &boxyagentv1.ResolveGuestBootstrapCredentialResponse{
		Username: guestUsername(resource),
		Password: password,
	}, nil
}

func guestUsername(resource model.Resource) string {
	if username, ok := resource.Properties["guest_user"].(string); ok && strings.TrimSpace(username) != "" {
		return username
	}
	if guestOS, ok := resource.Properties["guest_os"].(string); ok && strings.EqualFold(guestOS, "linux") {
		return "admin"
	}
	return "Administrator"
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
// that connection rather than merely removing the registry entry. If
// forceOrphanResources is set, it also sweeps every resource still
// attributed to agentID out of Boxy's bookkeeping once deregistration has
// made the agent verifiably absent from the registry — see
// pool.AgentProvisioner.ForceOrphan's precondition. Used by
// `boxy agent revoke <id> [--force-orphan-resources]`.
func (s *Server) Revoke(ctx context.Context, agentID, reason string, forceOrphanResources bool) error {
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

	// forceOrphanResources sweeps only after Deregister above has run, so
	// Registry.Get(agentID) already returns false by the time
	// AgentProvisioner.ForceOrphan checks it — that ordering is what makes
	// the precondition hold. A sweep failure is logged, not returned:
	// revocation itself already succeeded (the agent is deregistered and
	// disconnected), and the sweep is best-effort cleanup that can be
	// retried by re-running the command — mirroring this file's existing
	// "best-effort, logs rather than fails" precedent for the #133
	// reconciliation sweep (see pool.DefaultReconciliationPassTimeout's doc
	// comment).
	if forceOrphanResources {
		if s.forceOrphaner == nil {
			s.log().Warn("force-orphan-resources requested but not configured; no resources swept", "agent_id", agentID)
		} else {
			n, err := s.forceOrphaner.ForceOrphanAgentResources(ctx, agentID, reason)
			if err != nil {
				s.log().Warn("force-orphan resources incomplete", "agent_id", agentID, "orphaned", n, "error", err)
			} else {
				s.log().Warn("force-orphaned agent resources", "agent_id", agentID, "count", n)
			}
		}
	}

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

package store

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
)

// MemoryStore is an in-memory Store implementation for scaffolding and tests.
type MemoryStore struct {
	mu sync.Mutex

	pools                  map[model.PoolName]model.Pool
	poolGuestCredentials   map[model.PoolName]string
	resources              map[model.ResourceID]model.Resource
	sandboxes              map[model.SandboxID]model.Sandbox
	agentTokens            map[model.AgentTokenID]model.AgentRegistrationToken
	apiKeys                map[model.APIKeyID]model.APIKey
	sessions               map[model.SessionID]model.Session
	localAdmin             *model.LocalAdminAccount
	revokedAgentIdentities map[model.AgentIdentityID]model.RevokedAgentIdentity
	agentIdentities        map[string]model.AgentIdentity
	events                 map[string]lifecycle.Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		pools:                  make(map[model.PoolName]model.Pool),
		poolGuestCredentials:   make(map[model.PoolName]string),
		resources:              make(map[model.ResourceID]model.Resource),
		sandboxes:              make(map[model.SandboxID]model.Sandbox),
		agentTokens:            make(map[model.AgentTokenID]model.AgentRegistrationToken),
		apiKeys:                make(map[model.APIKeyID]model.APIKey),
		sessions:               make(map[model.SessionID]model.Session),
		revokedAgentIdentities: make(map[model.AgentIdentityID]model.RevokedAgentIdentity),
		agentIdentities:        make(map[string]model.AgentIdentity),
		events:                 make(map[string]lifecycle.Record),
	}
}

func (s *MemoryStore) GetPool(ctx context.Context, name model.PoolName) (model.Pool, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pools[name]
	if !ok {
		return model.Pool{}, ErrNotFound
	}
	return p, nil
}

func (s *MemoryStore) PutPool(ctx context.Context, pool model.Pool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if pool.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	s.pools[pool.Name] = pool
	return nil
}

func (s *MemoryStore) PutPoolGuestCredential(ctx context.Context, poolName model.PoolName, credential string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}
	if strings.TrimSpace(credential) == "" {
		return fmt.Errorf("pool guest credential is required")
	}
	s.poolGuestCredentials[poolName] = credential
	return nil
}

func (s *MemoryStore) GetPoolGuestCredential(ctx context.Context, poolName model.PoolName) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.poolGuestCredentials[poolName]
	if !ok {
		return "", ErrNotFound
	}
	return credential, nil
}

// DeletePoolGuestCredential removes a legacy plaintext pool credential. New
// runtime code stores credentials through pkg/secrets; this method exists for
// the explicit migration command only.
func (s *MemoryStore) DeletePoolGuestCredential(ctx context.Context, poolName model.PoolName) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.poolGuestCredentials[poolName]; !ok {
		return ErrNotFound
	}
	delete(s.poolGuestCredentials, poolName)
	return nil
}

func (s *MemoryStore) ListPoolGuestCredentials(ctx context.Context) (map[model.PoolName]string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[model.PoolName]string, len(s.poolGuestCredentials))
	for pool, credential := range s.poolGuestCredentials {
		out[pool] = credential
	}
	return out, nil
}

func (s *MemoryStore) GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.resources[id]
	if !ok {
		return model.Resource{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) PutResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	s.resources[res.ID] = res
	return nil
}

func (s *MemoryStore) DeleteResource(_ context.Context, id model.ResourceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.resources[id]; !ok {
		return ErrNotFound
	}
	delete(s.resources, id)
	return nil
}

func (s *MemoryStore) GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sandboxes[id]
	if !ok {
		return model.Sandbox{}, ErrNotFound
	}
	return sb, nil
}

func (s *MemoryStore) CreateSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	if _, exists := s.sandboxes[sb.ID]; exists {
		return fmt.Errorf("sandbox already exists: %s", sb.ID)
	}
	s.sandboxes[sb.ID] = sb
	return nil
}

func (s *MemoryStore) PutSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	s.sandboxes[sb.ID] = sb
	return nil
}

func (s *MemoryStore) DeleteSandbox(_ context.Context, id model.SandboxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sandboxes[id]; !ok {
		return ErrNotFound
	}
	delete(s.sandboxes, id)
	return nil
}

func (s *MemoryStore) ListPools(_ context.Context) ([]model.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Pool, 0, len(s.pools))
	for _, p := range s.pools {
		out = append(out, p)
	}
	return out, nil
}

func (s *MemoryStore) ListResources(_ context.Context) ([]model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Resource, 0, len(s.resources))
	for _, r := range s.resources {
		out = append(out, r)
	}
	return out, nil
}

func (s *MemoryStore) ListSandboxes(_ context.Context) ([]model.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Sandbox, 0, len(s.sandboxes))
	for _, sb := range s.sandboxes {
		out = append(out, sb)
	}
	return out, nil
}

func (s *MemoryStore) GetAgentToken(_ context.Context, id model.AgentTokenID) (model.AgentRegistrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.agentTokens[id]
	if !ok {
		return model.AgentRegistrationToken{}, ErrNotFound
	}
	return tok, nil
}

func (s *MemoryStore) PutAgentToken(_ context.Context, tok model.AgentRegistrationToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.ID == "" {
		return fmt.Errorf("agent token id is required")
	}
	s.agentTokens[tok.ID] = tok
	return nil
}

func (s *MemoryStore) DeleteAgentToken(_ context.Context, id model.AgentTokenID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agentTokens[id]; !ok {
		return ErrNotFound
	}
	delete(s.agentTokens, id)
	return nil
}

func (s *MemoryStore) GetAPIKey(_ context.Context, id model.APIKeyID) (model.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.apiKeys[id]
	if !ok {
		return model.APIKey{}, ErrNotFound
	}
	return key, nil
}

func (s *MemoryStore) PutAPIKey(_ context.Context, key model.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key.ID == "" {
		return fmt.Errorf("api key id is required")
	}
	if key.Hash == "" {
		return fmt.Errorf("api key hash is required")
	}
	if !key.Role.Valid() {
		return fmt.Errorf("api key role %q is invalid", key.Role)
	}
	s.apiKeys[key.ID] = key
	return nil
}

func (s *MemoryStore) DeleteAPIKey(_ context.Context, id model.APIKeyID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apiKeys[id]; !ok {
		return ErrNotFound
	}
	delete(s.apiKeys, id)
	return nil
}

func (s *MemoryStore) ListAPIKeys(_ context.Context) ([]model.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.APIKey, 0, len(s.apiKeys))
	for _, key := range s.apiKeys {
		out = append(out, key)
	}
	return out, nil
}

func (s *MemoryStore) GetSession(_ context.Context, id model.SessionID) (model.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return model.Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) PutSession(_ context.Context, session model.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return fmt.Errorf("session id is required")
	}
	if session.Hash == "" {
		return fmt.Errorf("session hash is required")
	}
	s.sessions[session.ID] = session
	return nil
}

func (s *MemoryStore) DeleteSession(_ context.Context, id model.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return ErrNotFound
	}
	delete(s.sessions, id)
	return nil
}

func (s *MemoryStore) ListSessions(_ context.Context) ([]model.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, session)
	}
	return out, nil
}

func (s *MemoryStore) GetLocalAdmin(_ context.Context) (model.LocalAdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localAdmin == nil {
		return model.LocalAdminAccount{}, ErrNotFound
	}
	return *s.localAdmin, nil
}

func (s *MemoryStore) PutLocalAdmin(_ context.Context, account model.LocalAdminAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account.Username == "" {
		return fmt.Errorf("local admin username is required")
	}
	if account.PasswordHash == "" {
		return fmt.Errorf("local admin password hash is required")
	}
	s.localAdmin = &account
	return nil
}

func (s *MemoryStore) ListAgentTokens(_ context.Context) ([]model.AgentRegistrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.AgentRegistrationToken, 0, len(s.agentTokens))
	for _, tok := range s.agentTokens {
		out = append(out, tok)
	}
	return out, nil
}

func (s *MemoryStore) PutRevokedAgentIdentity(_ context.Context, rev model.RevokedAgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rev.ID == "" {
		return fmt.Errorf("revoked agent identity id is required")
	}
	s.revokedAgentIdentities[rev.ID] = rev
	return nil
}

func (s *MemoryStore) IsAgentIdentityRevoked(_ context.Context, certSerial string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rev := range s.revokedAgentIdentities {
		if rev.CertSerial == certSerial {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) ListRevokedAgentIdentities(_ context.Context) ([]model.RevokedAgentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.RevokedAgentIdentity, 0, len(s.revokedAgentIdentities))
	for _, rev := range s.revokedAgentIdentities {
		out = append(out, rev)
	}
	return out, nil
}

func (s *MemoryStore) PutAgentIdentity(_ context.Context, identity model.AgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if identity.AgentID == "" {
		return fmt.Errorf("agent id is required")
	}
	s.agentIdentities[identity.AgentID] = identity
	return nil
}

func (s *MemoryStore) GetAgentIdentity(_ context.Context, agentID string) (model.AgentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, ok := s.agentIdentities[agentID]
	if !ok {
		return model.AgentIdentity{}, ErrNotFound
	}
	return identity, nil
}
